// Package pkgmgr builds the shell that installs packages on a router.
//
// A router's own package manager is the only correct one to use, and which one
// that is depends on the release: OpenWrt switched from opkg to apk in 25.12,
// and a fork can track 25.12 while still building with opkg. Both tiers hit
// this — the container over `docker exec`, the VM over ssh — and both install
// the same three kinds of thing (feed packages, local files, out-of-feed
// downloads). Writing the commands here rather than at each call site is what
// keeps the two tiers installing identically.
package pkgmgr

import (
	"path/filepath"
	"strings"

	"owfeed.org/owlab/internal/config"
)

// HostToken is what a feed URL carries in place of an address when the feed is
// being served by the machine running owlab.
//
// There is no literal that is right everywhere, which is why this is a token and
// not documentation telling people what to type. A container reaches the host at
// the bridge gateway, which is 172.17.0.1 on the default bridge of a Linux CI
// runner, something else on a user-defined bridge, and nothing at all under
// Docker Desktop. A VM never sees any of those, because QEMU's user-mode stack
// puts the host at the LAN address owlab hands it. Every pipeline that has tried
// to serve a feed off the runner has hardcoded 172.17.0.1 and then broken on the
// first developer machine.
//
// So the caller writes the token, and owlab substitutes the answer for the tier
// the router actually runs on:
//
//	owlab test --feed 'http://{host}:8080/packages/noarch/packages.adb' --feed-key feed.pem
const HostToken = "{host}"

const (
	// dockerHostAlias resolves through the extra_hosts entry every generated
	// service carries, where it is mapped to Docker's own host-gateway.
	dockerHostAlias = "host.docker.internal"

	// slirpHostAddr is where QEMU's user-mode stack answers for the host on the
	// LAN network owlab configures: net=192.168.1.0/24, host=192.168.1.254.
	// Fixed by owlab, so it is a constant here rather than a lookup.
	slirpHostAddr = "192.168.1.254"
)

// ResolveHost substitutes HostToken for the address this tier reaches the host on.
//
// Left alone when the token is absent, so a URL naming a real feed passes through
// untouched and no caller has to know whether it is dealing with one or the other.
func ResolveHost(url string, vm bool) string {
	if !strings.Contains(url, HostToken) {
		return url
	}
	host := dockerHostAlias
	if vm {
		host = slirpHostAddr
	}
	return strings.ReplaceAll(url, HostToken, host)
}

// Options are the differences between one install and another.
type Options struct {
	// Update refreshes the package index first. Wanted for anything named by
	// feed, pointless for a file already on the router.
	Update bool

	// Untrusted accepts a package carrying no signature the router's keyring
	// knows, which is every locally built or downloaded one. apk refuses them
	// outright without it; opkg has no equivalent and ignores this.
	Untrusted bool

	// Overwrite lets a package take a file another package already owns.
	//
	// Needed by out-of-feed packages that pull a replacement dependency:
	// luci-app-openclash needs dnsmasq-full, which collides with the dnsmasq
	// already in the image over /etc/init.d/dnsmasq.
	Overwrite bool

	// Tolerant installs one package at a time and reports the ones this
	// release's feed does not have, instead of failing the lot.
	//
	// Feeds differ between releases and between a fork and upstream, so a name
	// missing on 24.10 should cost that package and not the whole router. The
	// skipped names are printed, because silently getting fewer packages than
	// were asked for is the failure this is meant to make visible.
	Tolerant bool
}

// AddFeed is the shell that points a router at a package feed: the public key
// into the manager's keyring, the repository line into its config.
//
// This is what makes a feed testable end to end. Installing a built file proves
// the package works; installing it by name, out of a signed index, proves the
// thing a subscriber will actually do — and it is the only way to find out that
// an index is unreadable, a URL redirects, or a key does not match before a
// subscriber does.
//
// keyFile is the name the key is written under, and for opkg that name is
// load-bearing: opkg looks a key up by its id, so the file has to be called what
// the feed published it as. apk does not care and matches on the key itself.
//
// The two managers disagree about everything here — apk takes the URL of the
// index FILE, opkg the URL of the DIRECTORY containing it — which is exactly the
// sort of difference that belongs in this package rather than at a call site.
func AddFeed(pm config.PackageManager, name, url, keyFile string) string {
	var b strings.Builder
	if pm == config.APK {
		b.WriteString("mkdir -p /etc/apk/keys /etc/apk/repositories.d\n")
		b.WriteString("cp " + keyFile + " /etc/apk/keys/\n")
		b.WriteString("printf '%s\\n' " + url + " > /etc/apk/repositories.d/" + name + ".list\n")
		return b.String()
	}
	b.WriteString("mkdir -p /etc/opkg/keys\n")
	b.WriteString("cp " + keyFile + " /etc/opkg/keys/\n")
	// Appended, not written: customfeeds.conf is where a router's own extra
	// feeds live, and replacing it would take them with it.
	b.WriteString("printf 'src/gz %s %s\\n' " + name + " " + url + " >> /etc/opkg/customfeeds.conf\n")
	return b.String()
}

// UpdateShell is the shell that refreshes the package index, and the reason
// this package has an opinion about a feed that does not answer.
//
// Both managers exit non-zero when one configured feed is unreachable, and
// every caller runs them under `set -eu`, so a single dead feed took the whole
// install down before the per-package loop below it ever ran — the loop that
// is deliberately written to tolerate a package this release's feed does not
// carry. The condition is routine rather than exotic: a rootfs image pins its
// kmods index by kernel hash (kmods/6.18.33-1-<hash>/packages.adb), the feed
// keeps only the last handful of kernel builds, and an image older than that
// window 404s on that one sub-index on every run. Retrying does not help; it
// is a 404, not a stall.
//
// What counts as survivable is measured, not reasoned. On
// openwrt/rootfs:x86_64-25.12.4 (2026-09-07), with one unreachable feed added
// beside the eight the image ships:
//
//	ERROR: wget: exited with error 8
//	WARNING: updating and opening .../kmods/6.12.99-1-dead/packages.adb: unexpected end of file
//	 [https://.../targets/x86/64/packages/packages.adb]   <- one per feed that answered, 8 of them
//	1 unavailable, 0 stale; 11279 distinct packages available
//	rc=1
//
// and on the same image with every feed pointed at an unresolvable host:
//
//	8 unavailable, 0 stale; 136 distinct packages available
//	rc=8
//
// So the package count in the summary line is not, on its own, the signal it
// looks like: it counts the installed database too and stays well clear of
// zero when nothing at all was reached. What separates the two runs is whether
// any feed was read, and apk says that in the bracketed lines — eight above,
// none below. Both are required here: a feed was read, and the summary says
// packages are available.
//
// opkg is affected identically (measured on openwrt/rootfs:x86_64-24.10.8 the
// same day: rc=1 with one bad feed among seven good ones, rc=7 with the
// network cut) and marks each feed it read with "Updated list of available
// packages in /var/opkg-lists/<name>". It prints no summary line at all, so on
// that side the count of feeds read is the whole test.
//
// The output is captured rather than silenced, which is the other half of the
// fix: a clean refresh prints nothing, and a partial one replays every line
// the manager wrote, so the log names the feed that did not answer. Tolerating
// a partial refresh silently would leave the developer to guess why a package
// went missing.
//
// images/Dockerfile runs this same shell in the container tier and is held to
// it by TestDockerfileRefreshesTheIndexTheSameWay — Docker cannot call Go, so
// the text is duplicated there, and the two must not drift.
func UpdateShell(pm config.PackageManager) string {
	cmd, marker := "opkg update", "^Updated list of available packages"
	feeds := "/etc/opkg/distfeeds.conf and /etc/opkg/customfeeds.conf"
	// Only apk prints a summary line, so only apk is asked for one.
	summary, dead := "", `[ "$owlab_read" -eq 0 ]`
	partial := `echo "owlab: opkg update: partial refresh, $owlab_read feed(s) read; the feed above did not answer and its packages will be missing" >&2`
	if pm == config.APK {
		cmd, marker = "apk update", `^ \[`
		feeds = "/etc/apk/repositories and /etc/apk/repositories.d"
		summary = `owlab_have=$(printf '%s\n' "$owlab_upd" | sed -n 's/^[0-9][0-9]* unavailable, [0-9][0-9]* stale; \([0-9][0-9]*\) distinct packages available$/\1/p')` + "\n"
		dead = `[ "$owlab_read" -eq 0 ] || [ "${owlab_have:-0}" -eq 0 ]`
		partial = `echo "owlab: apk update: partial refresh, $owlab_read feed(s) read, $owlab_have packages available; the feed above did not answer and its packages will be missing" >&2`
	}
	return "" +
		// `x=$(cmd) && a || b` rather than an if, because a bare failing
		// assignment under `set -e` ends the script right here, which is the
		// bug this function exists to fix.
		`owlab_upd=$(` + cmd + ` 2>&1) && owlab_rc=0 || owlab_rc=$?` + "\n" +
		// grep -c exits 1 when it counts nothing — the very case that has to
		// survive — and still prints the 0.
		`owlab_read=$(printf '%s\n' "$owlab_upd" | grep -c '` + marker + `') || true` + "\n" +
		summary +
		`if [ "$owlab_rc" -ne 0 ]; then printf '%s\n' "$owlab_upd" >&2; fi` + "\n" +
		`if [ "$owlab_rc" -ne 0 ] && { ` + dead + `; }; then echo "owlab: ` + cmd + ` failed: no feed answered. Check the router's network and the feeds in ` + feeds + `" >&2; exit "$owlab_rc"; fi` + "\n" +
		`if [ "$owlab_rc" -ne 0 ]; then ` + partial + `; fi` + "\n"
}

// Install is the shell that installs pkgs, or "true" when there is nothing to
// install — a script fragment that is always safe to concatenate.
//
// pkgs may name feed packages or absolute paths to files already pushed onto
// the router; both managers accept either in the same position.
func Install(pm config.PackageManager, pkgs []string, o Options) string {
	if len(pkgs) == 0 {
		return "true"
	}
	add := "opkg install"
	if pm == config.APK {
		add = "apk add"
	}

	var flags []string
	if o.Untrusted && pm == config.APK {
		flags = append(flags, "--allow-untrusted")
	}
	if o.Overwrite {
		flags = append(flags, "--force-overwrite")
	}
	if len(flags) > 0 {
		add += " " + strings.Join(flags, " ")
	}

	var b strings.Builder
	if o.Update {
		b.WriteString(UpdateShell(pm))
	}
	if o.Tolerant {
		b.WriteString("for p in " + strings.Join(pkgs, " ") + "; do\n")
		b.WriteString("  " + add + " \"$p\" >/dev/null 2>&1 || echo \"owlab: skip (not in this feed): $p\"\n")
		b.WriteString("done\n")
		return b.String()
	}
	b.WriteString(add + " " + strings.Join(pkgs, " ") + "\n")
	return b.String()
}

// Installable reports whether pm can read this package file.
//
// The decision is the file extension and nothing else: a name that is neither
// format — a script, a tarball, an archive a fixture unpacks — is not something
// owlab was asked to judge, and refusing it here would break installs that work.
//
// Only the two package formats are ruled on, because only they are silently
// wrong. apk hands an .ipk to its own parser and reports
// `v2 package format error`, which describes a byte sequence and never mentions
// that this router uses the other manager.
func Installable(pm config.PackageManager, file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".apk":
		return pm == config.APK
	case ".ipk":
		return pm == config.OPKG
	}
	return true
}

// SkipNote is the single line naming a file this router was not given.
//
// Said out loud on purpose. A glob that matched only the other release line's
// format leaves nothing to install, and an install of nothing succeeds — so
// without this line the run reports success and then fails every assertion
// after it, with no reason anywhere on screen.
func SkipNote(pm config.PackageManager, file string) string {
	return "owlab: skipping " + filepath.Base(file) + " (this router uses " + string(pm) + ")"
}

// FilterFiles splits package files into the ones pm can install and the ones it
// cannot.
//
// One glob covering both release lines is the natural way to test a package
// that ships in both formats, and it is exactly the layout `owfeed build`
// leaves behind — `dist/noarch/*.apk` for 25.12 beside `dist/all/*.ipk` for
// 24.10. The glob is expanded once on the host, so before this every router got
// every match: the wrong-format file failed, and because all of them go into a
// single command it took the compatible one down with it.
//
// Callers filter before pushing, so no router is handed bytes it has no use for.
func FilterFiles(pm config.PackageManager, files []string) (keep, skipped []string) {
	for _, f := range files {
		if Installable(pm, f) {
			keep = append(keep, f)
			continue
		}
		skipped = append(skipped, f)
	}
	return keep, skipped
}
