package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/VizzleTF/owlab/internal/compose"
	"github.com/VizzleTF/owlab/internal/config"
	"github.com/VizzleTF/owlab/internal/dockercli"
	"github.com/VizzleTF/owlab/internal/engine"
	"github.com/VizzleTF/owlab/internal/qemu"
)

type checkResult int

const (
	pass checkResult = iota
	warn
	fail
	info
)

func (c checkResult) mark() string {
	switch c {
	case pass:
		return "ok  "
	case warn:
		return "warn"
	case fail:
		return "FAIL"
	default:
		return "    "
	}
}

type check struct {
	name   string
	result checkResult
	detail string
}

// doctor reports what this machine can and cannot do, before anything is
// built. It deliberately loads no config and requires no daemon, because the
// times it is most needed are the times nothing else works.
func (a *app) doctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	var checks []check
	add := func(name string, r checkResult, format string, v ...any) {
		checks = append(checks, check{name, r, fmt.Sprintf(format, v...)})
	}

	add("host", info, "%s/%s", runtime.GOOS, runtime.GOARCH)

	// docker
	//
	// A missing engine is no longer the end of the report: a project whose
	// routers are all fidelity vm never touches Docker, and the checks below
	// are exactly the ones such a developer came here for.
	var eng engine.Info
	haveDocker := false
	if err := dockercli.Check(ctx); err != nil {
		add("docker", warn, "%v (only fidelity vm will work)", err)
	} else {
		add("docker", pass, "cli and compose v2 present")
		eng = engine.Detect(ctx)
		if eng.Err != nil {
			add("engine", warn, "%v (only fidelity vm will work)", eng.Err)
		} else {
			haveDocker = true
			add("engine", pass, "%s", eng.Describe())
		}
	}

	// The default architecture, and whether it needs emulation.
	hostArch := config.HostArch()
	add("arch", info, "native OpenWrt arch for this host is %s", hostArch)

	if haveDocker {
		// Bind-mount ownership: only native Linux keeps host uid/gid, which is
		// the one case where a mounted authorized_keys is rejected by dropbear.
		if eng.BindMountsAreHostOwned() {
			add("bind mounts", info, "keep host uid/gid — owlab copies the ssh key into place rather than mounting it")
		} else {
			add("bind mounts", info, "presented as owned by the container user")
		}
	}

	checks = append(checks, vmChecks(ctx, hostArch)...)

	// ssh keys: what will be installed, or why nothing will be.
	if keys, src := compose.PublicKeys(); len(keys) > 0 {
		names := make([]string, 0, len(keys))
		for _, k := range keys {
			names = append(names, filepath.Base(k))
		}
		add("ssh keys", pass, "%s (from %s)", strings.Join(names, ", "), src)
	} else {
		add("ssh keys", warn,
			"no id_*.pub found in ~/.ssh — ssh will fall back to password auth. "+
				"Run `ssh-keygen -t ed25519`, or set OWLAB_PUBKEY to an existing key.")
	}

	if cwd, err := os.Getwd(); err == nil {
		// Sources on a Windows drive under WSL: inotify does not propagate and
		// filesystem access is slow enough to notice. Asked of the host
		// directly rather than of the engine, because a project running
		// entirely on QEMU never detects one and would otherwise be told
		// nothing.
		if engine.InWSL() && strings.HasPrefix(filepath.ToSlash(cwd), "/mnt/") {
			add("workspace", warn,
				"%s is on a Windows drive; file watching will not see changes and IO is slow. "+
					"Move the project into the WSL filesystem (e.g. ~/src).", cwd)
		} else {
			add("workspace", pass, "%s", cwd)
		}

		// CRLF line endings break every shell script the image runs, with an
		// error ("bad interpreter: /bin/sh^M") that does not name the cause.
		if bad := findCRLF(cwd); bad != "" {
			add("line endings", fail,
				"%s has CRLF line endings; scripts with CRLF fail in the container with "+
					"\"bad interpreter\". Add a .gitattributes with `* text=auto eol=lf` and "+
					"run `git add --renormalize .`", bad)
		} else {
			add("line endings", pass, "no CRLF found in shell scripts")
		}
	}

	// The config, if there is one. Reported through the same resolution the
	// other commands use, so `owlab --config x doctor` checks x rather than
	// whatever happens to be above the working directory.
	{
		if path, err := resolveConfig(a.configPath); err == nil {
			cfg, err := config.Load(path)
			if err != nil {
				add("config", fail, "%v", err)
			} else {
				add("config", pass, "%s — %d router(s): %s",
					path, len(cfg.Routers), strings.Join(cfg.RouterIDs(), ", "))
				// A build command that is not there fails every sync, and the
				// shell's "not found" does not say where the string came from.
				if b := cfg.Project.Build; b != "" {
					script := strings.Fields(b)[0]
					if _, err := os.Stat(filepath.Join(cfg.Dir, script)); err == nil {
						add("build", pass, "%s", b)
					} else if _, err := exec.LookPath(script); err == nil {
						add("build", pass, "%s", b)
					} else {
						add("build", fail, "project.build runs %q, which is neither a file in %s nor on PATH", script, cfg.Dir)
					}
				}
				checks = append(checks, configChecks(ctx, cfg)...)
				checks = append(checks, dnsEgressChecks(ctx, cfg)...)
			}
		} else {
			add("config", info, "no %s here; run owlab from a project that has one", config.FileName)
		}
	}

	return report(checks)
}

// dnsEgressChecks reports whether a container can send DNS queries to a
// resolver of its own choosing.
//
// Worth a check of its own because the failure is silent and the error it
// eventually produces names the wrong thing. Some engines — OrbStack, measured
// — do not forward outbound UDP port 53 at all, while TCP leaves normally. A
// package that ships its own resolver then cannot look anything up: podkop's
// sing-box exits with
//
//	initial rule-set: ... lookup github.com: context deadline exceeded
//
// which reads as "GitHub is unreachable" when GitHub over https is fine. The
// fix is always the same — point the package's bootstrap resolver at the
// engine's own, which does answer — so the useful thing owlab can do is say
// which resolver that is.
//
// Probed through a router that is already running rather than by starting a
// container, so that `doctor` stays fast and never pulls an image.
func dnsEgressChecks(ctx context.Context, cfg *config.Config) []check {
	var container string
	for i := range cfg.Routers {
		r := &cfg.Routers[i]
		if r.Fidelity == config.VM {
			continue
		}
		name := compose.ContainerName(cfg.Project.Name, r.ID)
		out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", name).Output()
		if err == nil && strings.TrimSpace(string(out)) == "true" {
			container = name
			break
		}
	}
	if container == "" {
		return nil
	}

	// busybox nslookup takes the server as its second argument, and `timeout`
	// bounds a query that would otherwise sit through several retries.
	script := `timeout 4 nslookup openwrt.org 1.1.1.1 >/dev/null 2>&1 && echo direct-ok
awk '$1 == "nameserver" { print "engine " $2; exit }' /etc/resolv.conf`
	out, err := exec.CommandContext(ctx, "docker", "exec", container, "/bin/sh", "-c", script).Output()
	if err != nil {
		return nil
	}

	direct := strings.Contains(string(out), "direct-ok")
	engineNS := ""
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "engine "); ok {
			engineNS = rest
		}
	}

	if direct {
		return []check{{"dns egress", pass,
			"containers can query any resolver over UDP; a package that brings its own works unconfigured"}}
	}
	detail := "this engine does not forward outbound UDP port 53 — a package with its own resolver " +
		"(podkop, https-dns-proxy, AdGuard Home) resolves nothing and reports it as the destination being " +
		"unreachable. Point its bootstrap resolver at the engine's"
	if engineNS != "" {
		detail += " (" + engineNS + ")"
	}
	detail += "; TCP, and so DoH and DoT, are unaffected."
	return []check{{"dns egress", warn, detail}}
}

// vmChecks reports what the VM tier can do on this machine.
//
// The host architecture is checked first and reported on its own, because it
// is the one thing the developer cannot fix by installing something: no
// accelerator runs a foreign CPU, so an x86 router on an ARM laptop is slow no
// matter what else is present.
func vmChecks(ctx context.Context, hostArch string) []check {
	t, err := config.LookupTarget(hostArch)
	if err != nil || !t.SupportsVM() {
		return []check{{"fidelity vm", warn,
			fmt.Sprintf("this host's native arch (%s) has no bootable image upstream; vm routers would have to run emulated (arches that work: %s)",
				hostArch, strings.Join(config.VMArches(), ", "))}}
	}

	d := qemu.Inspect(ctx, t)
	if d.Err != nil {
		return []check{{"fidelity vm", warn, d.Err.Error()}}
	}

	out := []check{}
	version := d.Version
	if version == "" {
		version = d.Binary
	}
	if d.Accel.Native {
		out = append(out, check{"fidelity vm", pass,
			fmt.Sprintf("%s with -accel %s — a %s router boots natively", version, d.Accel.Name, t.Arch)})
	} else {
		out = append(out, check{"fidelity vm", warn,
			fmt.Sprintf("%s falls back to tcg: %s. It works, but expect minutes rather than seconds.",
				version, d.Accel.Reason)})
	}
	if d.Firmware != "" {
		out = append(out, check{"vm firmware", info, d.Firmware})
	}
	out = append(out, check{"vm images", info, qemu.CacheDir()})
	return out
}

func configChecks(ctx context.Context, cfg *config.Config) []check {
	var out []check
	for i := range cfg.Routers {
		r := &cfg.Routers[i]
		name := "router " + r.ID

		switch {
		case r.Fidelity == config.VM:
			out = append(out, check{name, info,
				fmt.Sprintf("vm, booted from %s", r.VMImageName())})
		case r.FromTarball():
			out = append(out, check{name, info,
				fmt.Sprintf("built from a rootfs tarball (%s)", r.RootfsTarballURL())})
		default:
			out = append(out, check{name, info,
				fmt.Sprintf("%s, platform %s", r.BaseImage(), r.Platform())})
		}

		// Emulation is legal but slow, and slow-for-a-reason is much easier
		// to accept than slow-for-no-visible-reason.
		if r.Arch != config.HostArch() {
			out = append(out, check{name, warn,
				fmt.Sprintf("arch %s is not native to this host (%s) — it will run under emulation",
					r.Arch, config.HostArch())})
		}

		// A snapshot rootfs and a snapshot feed are rebuilt daily and
		// independently, so they drift apart within a day.
		if strings.EqualFold(r.Release, "snapshot") || strings.EqualFold(r.Release, "master") {
			out = append(out, check{name, warn,
				"snapshot images and snapshot feeds are rebuilt daily and independently; " +
					"package installs will start failing as they drift. Pin a point release for repeatable work."})
		}

		// A port already taken fails only at `up`, and the message docker
		// gives ("Bind for 0.0.0.0:2225 failed: port is already allocated")
		// names the port but not who holds it or which router wanted it.
		//
		// A running VM holds its own ports through QEMU, which is the normal
		// state of a started lab rather than a conflict to report — and unlike
		// a container there is no `docker ps` entry to recognise it by.
		if r.Fidelity == config.VM && qemu.New(cfg, r).Running() {
			continue
		}
		for _, p := range []struct {
			kind string
			port int
		}{{"http", r.Ports.HTTP}, {"ssh", r.Ports.SSH}} {
			busy, by := portInUse(ctx, p.port)
			if !busy {
				continue
			}
			// This router's own container holding its own port is the normal
			// state of a running lab, not a problem to report.
			if strings.Contains(by, compose.ContainerName(cfg.Project.Name, r.ID)) {
				continue
			}
			out = append(out, check{name, warn,
				fmt.Sprintf("host %s port %d is already in use%s — `owlab up` will fail until it is free or the config changes",
					p.kind, p.port, by)})
		}
	}
	return out
}

// portInUse reports whether something already listens on a host port, and
// names the container when it is one of ours.
func portInUse(ctx context.Context, port int) (bool, string) {
	// 0.0.0.0, not 127.0.0.1. Docker publishes to all interfaces, and on
	// macOS binding a specific address does NOT conflict with an existing
	// wildcard bind — so probing the loopback address reports every port as
	// free while `up` still fails with "address already in use".
	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err == nil {
		ln.Close()
		return false, ""
	}
	// Best effort at naming the holder; a container from another owlab
	// project is the overwhelmingly common case.
	out, cerr := exec.CommandContext(ctx, "docker", "ps", "--filter", "publish="+strconv.Itoa(port),
		"--format", "{{.Names}}").Output()
	if cerr == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return true, " by container " + strings.ReplaceAll(name, "\n", ", ")
		}
	}
	return true, ""
}

// findCRLF looks for CRLF in the shell scripts most likely to be executed
// inside a container. It stops at the first hit: one is enough to report.
func findCRLF(root string) string {
	var found string
	limit := 2000
	seen := 0
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(p)
			if base == ".git" || base == "node_modules" || base == ".owlab" {
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > limit {
			return filepath.SkipAll
		}
		if !strings.HasSuffix(p, ".sh") && filepath.Base(filepath.Dir(p)) != "uci-defaults" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if strings.Contains(string(b), "\r\n") {
			found = p
		}
		return nil
	})
	return found
}

func report(checks []check) error {
	worst := pass
	for _, c := range checks {
		fmt.Printf("%s  %-16s %s\n", c.result.mark(), c.name, c.detail)
		if c.result == fail {
			worst = fail
		} else if c.result == warn && worst != fail {
			worst = warn
		}
	}
	fmt.Println()
	switch worst {
	case fail:
		return fmt.Errorf("doctor found problems that will stop owlab from working")
	case warn:
		fmt.Println("doctor found nothing fatal; the warnings above are limits of this machine, not errors.")
	default:
		fmt.Println("all good.")
	}
	return nil
}
