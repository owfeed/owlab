package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/VizzleTF/owlab/internal/config"
	"github.com/VizzleTF/owlab/internal/dockercli"
	"github.com/VizzleTF/owlab/internal/engine"
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
	if err := dockercli.Check(ctx); err != nil {
		add("docker", fail, "%v", err)
		return report(checks)
	}
	add("docker", pass, "cli and compose v2 present")

	eng := engine.Detect(ctx)
	if eng.Err != nil {
		add("engine", fail, "%v", eng.Err)
		return report(checks)
	}
	add("engine", pass, "%s", eng.Describe())

	// The default architecture, and whether it needs emulation.
	hostArch := config.HostArch()
	add("arch", info, "native OpenWrt arch for this host is %s", hostArch)

	// fidelity full
	if ok, reason := eng.HostKernelWiFi(); ok {
		add("fidelity full", pass, "this engine can host mac80211_hwsim radios")
	} else {
		add("fidelity full", warn, "%s", reason)
	}

	// Bind-mount ownership: only native Linux keeps host uid/gid, which is
	// the one case where a mounted authorized_keys is rejected by dropbear.
	if eng.BindMountsAreHostOwned() {
		add("bind mounts", info, "keep host uid/gid — owlab copies the ssh key into place rather than mounting it")
	} else {
		add("bind mounts", info, "presented as owned by the container user")
	}

	// Sources on a Windows drive under WSL: inotify does not propagate and
	// filesystem access is slow enough to notice.
	if cwd, err := os.Getwd(); err == nil {
		if eng.WSL && strings.HasPrefix(filepath.ToSlash(cwd), "/mnt/") {
			add("workspace", warn,
				"%s is on a Windows drive; file watching will not see changes and IO is slow. "+
					"Move the project into the WSL filesystem (e.g. ~/src).", cwd)
		} else {
			add("workspace", pass, "%s", cwd)
		}
	}

	// CRLF line endings break every shell script the image runs, with an
	// error ("bad interpreter: /bin/sh^M") that does not name the cause.
	if cwd, err := os.Getwd(); err == nil {
		if bad := findCRLF(cwd); bad != "" {
			add("line endings", fail,
				"%s has CRLF line endings; scripts with CRLF fail in the container with "+
					"\"bad interpreter\". Add a .gitattributes with `* text=auto eol=lf` and "+
					"run `git add --renormalize .`", bad)
		} else {
			add("line endings", pass, "no CRLF found in shell scripts")
		}
	}

	// The config, if there is one.
	if cwd, err := os.Getwd(); err == nil {
		if path, err := config.Find(cwd); err == nil {
			cfg, err := config.Load(path)
			if err != nil {
				add("config", fail, "%v", err)
			} else {
				add("config", pass, "%s — %d router(s): %s",
					path, len(cfg.Routers), strings.Join(cfg.RouterIDs(), ", "))
				checks = append(checks, configChecks(cfg)...)
			}
		} else {
			add("config", info, "no %s here; run owlab from a project that has one", config.FileName)
		}
	}

	return report(checks)
}

func configChecks(cfg *config.Config) []check {
	var out []check
	for i := range cfg.Routers {
		r := &cfg.Routers[i]
		name := "router " + r.ID

		if r.FromTarball() {
			out = append(out, check{name, info,
				fmt.Sprintf("built from a rootfs tarball (%s)", r.RootfsTarballURL())})
		} else {
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
	}
	return out
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
