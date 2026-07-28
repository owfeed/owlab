package qemu

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	owlab "github.com/VizzleTF/owlab"
	"github.com/VizzleTF/owlab/internal/compose"
	"github.com/VizzleTF/owlab/internal/config"
)

// Provision turns a freshly booted stock router into this project's router.
//
// It is the VM tier's answer to the Dockerfile, and it does the same work in
// the same order — packages, then the owlab overlay, then the uci-defaults
// that apply it. What it cannot do is bake the result into an image, so the
// disk itself is the cache: the marker written at the end is what makes every
// later `owlab up` a plain boot.
//
// The one step with no container equivalent is extroot, and it comes first
// because everything after it writes to the filesystem it moves.
func (v *VM) Provision(ctx context.Context, progress io.Writer) error {
	say := func(format string, a ...any) {
		if progress != nil {
			fmt.Fprintf(progress, "  "+format+"\n", a...)
		}
	}

	ssh := v.SSH()
	if err := ssh.Wait(ctx, 5*time.Minute); err != nil {
		return fmt.Errorf("%s never came up: %w\n\nSee the console: owlab logs %s", v.Router.ID, err, v.Router.ID)
	}

	st := v.loadState()
	st.Release = v.Router.Release
	st.Distro = string(v.Router.Distro)
	st.Arch = v.Router.Arch
	st.Image = v.Router.VMImageName()

	if v.Router.VM.Disk != "" && !st.Extroot {
		say("moving /overlay onto the %s disk", v.Router.VM.Disk)
		if err := v.setupExtroot(ctx); err != nil {
			return err
		}
		st.Extroot = true
		if err := v.saveState(st); err != nil {
			return err
		}
		// The switch only takes effect through block-mount's preinit, which
		// runs before the overlay is mounted and therefore only at boot.
		say("rebooting onto the new overlay")
		if err := v.reboot(ctx); err != nil {
			return err
		}
	}

	say("installing %d packages", len(v.Router.Packages))
	if err := v.installPackages(ctx, progress); err != nil {
		return err
	}
	if len(v.Router.Extra) > 0 {
		say("installing %d out-of-feed packages", len(v.Router.Extra))
		if err := v.installExtra(ctx, progress); err != nil {
			return err
		}
	}

	if v.Router.VM.Radios > 0 {
		say("adding %d mac80211_hwsim radios", v.Router.VM.Radios)
		if err := v.setupRadios(ctx); err != nil {
			return err
		}
	}

	// A reboot BEFORE the overlay, and it is not cosmetic.
	//
	// Everything above was installed onto a system that had already finished
	// booting, and a good deal of OpenWrt reads its inputs exactly once, at
	// boot:
	//
	//   * procd starts the services a package enabled — wpad among them — at
	//     boot, and never notices one that appeared afterwards.
	//   * netifd loads /lib/netifd/wireless/* at startup, so wifi-scripts
	//     installed later leaves `wifi up` reporting "Command failed: Not
	//     found" against a netifd with no wireless support loaded.
	//   * ubusd reads /usr/share/acl.d/ at startup. wpad's ACL is what lets
	//     hostapd — which runs as the unprivileged `network` user — publish
	//     its ubus object at all, and without that object the wireless setup
	//     script waits forever and the radios stay `pending: true`.
	//   * kmodloader loads /etc/modules.d/* at boot, which is when the hwsim
	//     radios come into existence.
	//
	// Each of those was measured on a router that looked fully provisioned,
	// and not one of them reports anything that names the cause.
	//
	// Before the overlay rather than after, because the fixtures have to see
	// the result: 60-wifi.sh asks whether real phys are present and configures
	// them if they are, and run one reboot earlier it would find none and
	// write the invented radios a container gets.
	say("rebooting so the services these packages enabled actually start")
	if err := v.reboot(ctx); err != nil {
		return err
	}

	say("applying the owlab overlay and fixtures")
	if err := v.applyOverlay(ctx); err != nil {
		return err
	}

	st.Provisioned = true
	return v.saveState(st)
}

// setupRadios installs mac80211_hwsim and asks for it at boot.
//
// A module file rather than a modprobe: the radios have to exist before netifd
// looks for them, and /etc/modules.d is how OpenWrt says that. The count is
// the module's own parameter, so two radios here are two phys — a dual-band
// router — rather than one phy pretending to be two bands.
func (v *VM) setupRadios(ctx context.Context) error {
	script := v.pkgInstallCmd([]string{"kmod-mac80211-hwsim"}) + `
printf 'mac80211_hwsim radios=%d\n' ` + fmt.Sprint(v.Router.VM.Radios) + ` > /etc/modules.d/mac80211-hwsim
`
	if out, err := v.SSH().Output(ctx, script); err != nil {
		// Not fatal: a target whose kmods feed has no hwsim should cost the
		// router its radios, not its existence.
		return fmt.Errorf("installing mac80211_hwsim: %s", strings.TrimSpace(out))
	}
	return nil
}

// setupExtroot points /overlay at the second disk.
//
// This is stock OpenWrt extroot — the same procedure and the same fstab entry
// a router uses to move its overlay onto a USB stick — and it is here for a
// hard reason. An upstream combined image carries a fixed overlay (87 MB on
// armsr) created without a resize inode, so growing it from inside the running
// system stops after one block group: measured, an online resize2fs on a
// 900 MB partition yielded 122 MB. A separate disk has no such history.
func (v *VM) setupExtroot(ctx context.Context) error {
	script := `set -e
` + v.pkgInstallCmd([]string{"block-mount", "e2fsprogs"}) + `
mkfs.ext4 -q -F -L owlab-overlay /dev/vdb
mount /dev/vdb /mnt
tar -C /overlay -cf - . | tar -C /mnt -xf -
umount /mnt
uci -q delete fstab.owlab_overlay || true
uci set fstab.owlab_overlay=mount
uci set fstab.owlab_overlay.target=/overlay
uci set fstab.owlab_overlay.device=/dev/vdb
uci set fstab.owlab_overlay.fstype=ext4
uci set fstab.owlab_overlay.options=rw,noatime
uci set fstab.owlab_overlay.enabled=1
uci commit fstab
`
	if _, err := v.SSH().Output(ctx, script); err != nil {
		return fmt.Errorf("extroot setup failed: %w", err)
	}
	return nil
}

// reboot restarts the guest and waits for it to answer again.
func (v *VM) reboot(ctx context.Context) error {
	// The connection dies mid-command, so the error says nothing.
	_, _ = v.SSH().Output(ctx, "reboot")

	// Wait for the router to actually go away before waiting for it to come
	// back. Without this the ssh that is still listening answers immediately
	// and the reboot is declared finished before it started.
	gone := time.Now().Add(30 * time.Second)
	for time.Now().Before(gone) {
		if !v.SSH().Reachable() {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return v.SSH().Wait(ctx, 5*time.Minute)
}

// pkgInstallCmd is the shell to install a list of feed packages.
func (v *VM) pkgInstallCmd(pkgs []string) string {
	if len(pkgs) == 0 {
		return "true"
	}
	list := strings.Join(pkgs, " ")
	if v.Router.PackageManager() == config.APK {
		return "apk update >/dev/null; apk add " + list
	}
	return "opkg update >/dev/null; opkg install " + list
}

// installPackages installs the router's package list.
//
// One at a time, with failures reported rather than fatal — the same rule the
// Dockerfile uses, and for the same reason: feeds differ between releases and
// between a fork and upstream, and one name missing on 24.10 should cost that
// package, not the whole router.
func (v *VM) installPackages(ctx context.Context, progress io.Writer) error {
	var b strings.Builder
	if v.Router.PackageManager() == config.APK {
		b.WriteString("apk update >/dev/null 2>&1\n")
		b.WriteString("for p in " + strings.Join(v.Router.Packages, " ") + "; do\n")
		b.WriteString("  apk add \"$p\" >/dev/null 2>&1 || echo \"owlab: skip (not in this feed): $p\"\n")
		b.WriteString("done\n")
	} else {
		b.WriteString("opkg update >/dev/null 2>&1\n")
		b.WriteString("for p in " + strings.Join(v.Router.Packages, " ") + "; do\n")
		b.WriteString("  opkg install \"$p\" >/dev/null 2>&1 || echo \"owlab: skip (not in this feed): $p\"\n")
		b.WriteString("done\n")
	}
	out, err := v.SSH().Output(ctx, b.String())
	if progress != nil && strings.TrimSpace(out) != "" {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fmt.Fprintf(progress, "  %s\n", line)
		}
	}
	return err
}

// installExtra installs packages named by URL.
//
// Downloaded on the host and pushed in, exactly as the container tier does,
// because a stock rootfs has no curl and its busybox wget cannot do TLS. These
// are installed WITHOUT SIGNATURE VERIFICATION — trusted by URL only.
func (v *VM) installExtra(ctx context.Context, progress io.Writer) error {
	cacheDir := filepath.Join(v.Config.Dir, compose.WorkDirName, "cache")
	pm := v.Router.PackageManager()

	for _, e := range v.Router.Extra {
		url := e.URLFor(pm)
		if url == "" {
			if progress != nil {
				fmt.Fprintf(progress, "  skip %s: nothing published for %s\n", e.Name, pm)
			}
			continue
		}
		local, err := compose.Fetch(url, cacheDir)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name, err)
		}
		body, err := os.ReadFile(local)
		if err != nil {
			return err
		}
		remote := "/tmp/" + filepath.Base(local)

		// Streamed through a tar rather than scp: scp against dropbear needs
		// an sftp-server the stock image does not ship, and tar over the same
		// ssh channel that runs everything else has no such requirement.
		archive, err := tarOneFile(remote, body)
		if err != nil {
			return err
		}
		if err := v.SSH().Run(ctx, "tar -C / -xf -", archive, io.Discard, os.Stderr); err != nil {
			return fmt.Errorf("%s: pushing to the router: %w", e.Name, err)
		}
		// --force-overwrite for the same reason the image build needs it:
		// these packages pull dependencies that replace a file the stock
		// image already owns (luci-app-openclash needs dnsmasq-full, which
		// collides with dnsmasq over /etc/init.d/dnsmasq).
		install := "opkg install --force-overwrite " + remote
		if pm == config.APK {
			install = "apk add --allow-untrusted --force-overwrite " + remote
		}
		out, err := v.SSH().Output(ctx, install+"; rm -f "+remote)
		if err != nil {
			// Fatal, unlike a feed package: this one was named by URL, so it
			// exists and was asked for by name. Reporting success without it
			// hands the developer a router quietly missing what they are
			// testing against.
			return fmt.Errorf("%s: %s", e.Name, strings.TrimSpace(out))
		}
	}
	return nil
}

// applyOverlay pushes owlab's rootfs overlay and runs the uci-defaults in it.
//
// The same files the image build copies in, from the same embedded copy, so a
// vm router and a basic router differ in what runs underneath them and in
// nothing else. The uci-defaults are run by hand because procd only runs them
// on a boot where they are new, and these arrive on a router that has already
// booted.
func (v *VM) applyOverlay(ctx context.Context) error {
	archive, err := overlayArchive(v.Config, v.Router)
	if err != nil {
		return err
	}
	if err := v.SSH().Run(ctx, "tar -C / -xf -", archive, io.Discard, os.Stderr); err != nil {
		return fmt.Errorf("pushing the overlay: %w", err)
	}

	script := `set -e
chmod +x /etc/uci-defaults/9*_owlab-* /usr/share/owlab/fixtures/*.sh /etc/rc.local 2>/dev/null || true
uci -q set system.@system[0].hostname='` + v.Router.Hostname + `'
uci -q commit system
/etc/init.d/system reload >/dev/null 2>&1 || true
sh /etc/uci-defaults/95_owlab-base
sh /etc/uci-defaults/96_owlab-fixtures
/etc/init.d/network reload >/dev/null 2>&1 || true
# The firewall is already running here, unlike on a container's first boot
# where uci-defaults precede every service. Without this reload the
# flow-offloading change 95_owlab-base just made would not take effect until
# the next reboot.
/etc/init.d/firewall reload >/dev/null 2>&1 || true
/etc/init.d/uhttpd restart >/dev/null 2>&1 || true
/etc/init.d/rpcd reload >/dev/null 2>&1 || true
sh /etc/rc.local >/dev/null 2>&1 || true
# The wifi fixture has just written /etc/config/wireless against the real
# phys; nothing has told netifd to act on it yet.
[ -n "$(ls /sys/class/ieee80211/ 2>/dev/null)" ] && wifi up >/dev/null 2>&1
true
`
	if _, err := v.SSH().Output(ctx, script); err != nil {
		return fmt.Errorf("applying the overlay: %w", err)
	}
	return nil
}

// overlayArchive is owlab's rootfs overlay, plus the per-router files the
// Dockerfile would otherwise write as build args.
func overlayArchive(cfg *config.Config, r *config.Router) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	src, err := fs.Sub(owlab.BuildContext(), "rootfs-extra")
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || p == "." {
			return nil
		}
		// The container entrypoint has no business on a VM: it exists to read
		// the address Docker put on eth0 and to fix up a container's DNS, and
		// here procd's own boot does all of that properly.
		if strings.HasPrefix(p, "usr/lib/owlab/") {
			return nil
		}
		body, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return writeFile(tw, p, body, 0o755)
	})
	if err != nil {
		return nil, err
	}

	// The three files the Dockerfile writes from build args.
	if err := writeFile(tw, "etc/owlab/fixtures", []byte(strings.Join(r.Fixtures, " ")+"\n"), 0o644); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "etc/owlab/theme", []byte(cfg.Project.Theme), 0o644); err != nil {
		return nil, err
	}
	if keys := authorizedKeys(); len(keys) > 0 {
		if err := writeFile(tw, "etc/dropbear/authorized_keys", keys, 0o600); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// authorizedKeys is the developer's public keys, concatenated.
func authorizedKeys() []byte {
	paths, _ := compose.PublicKeys()
	var b bytes.Buffer
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(body))
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.Bytes()
}

func tarOneFile(dest string, body []byte) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := writeFile(tw, strings.TrimPrefix(dest, "/"), body, 0o644); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeFile(tw *tar.Writer, name string, body []byte, mode int64) error {
	hdr := &tar.Header{
		Name:   path.Clean(name),
		Mode:   mode,
		Size:   int64(len(body)),
		Format: tar.FormatGNU,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}
