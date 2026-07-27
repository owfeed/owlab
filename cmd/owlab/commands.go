package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/VizzleTF/owlab/internal/compose"
	"github.com/VizzleTF/owlab/internal/config"
	syncpkg "github.com/VizzleTF/owlab/internal/sync"
)

// parseMixed lets flags appear before or after router ids, because
// `owlab up owrt2512 --rebuild` is what people type and having it silently
// treat --rebuild as a router name would be worse than either.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
}

func (a *app) up(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	rebuild := fs.Bool("rebuild", false, "rebuild images from scratch, ignoring the layer cache")
	noWait := fs.Bool("no-wait", false, "return as soon as the containers start, without waiting for LuCI")
	verbose := fs.Bool("v", false, "echo the docker commands being run")
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	a.docker.Verbose = *verbose

	routers, err := a.cfg.Select(ids)
	if err != nil {
		return err
	}
	if err := a.checkFidelity(routers); err != nil {
		return err
	}

	proj, err := compose.Prepare(a.cfg, a.eng)
	if err != nil {
		return err
	}

	names := routerIDs(routers, config.VM)
	if len(names) == 0 {
		return fmt.Errorf("nothing to start: every selected router is fidelity vm, which is not implemented yet")
	}

	buildArgs := append([]string{"build"}, names...)
	if *rebuild {
		buildArgs = append([]string{"build", "--no-cache", "--pull"}, names...)
	}
	if err := a.docker.Compose(ctx, proj.ComposePath, buildArgs...); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	upArgs := append([]string{"up", "-d", "--remove-orphans"}, names...)
	if err := a.docker.Compose(ctx, proj.ComposePath, upArgs...); err != nil {
		return fmt.Errorf("start failed: %w", err)
	}

	if *noWait {
		return a.printReady(routers, nil)
	}
	ready := a.waitForLuCI(ctx, routers)
	return a.printReady(routers, ready)
}

func (a *app) down(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	purge := fs.Bool("purge", false, "also remove the built images")
	verbose := fs.Bool("v", false, "echo the docker commands being run")
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	a.docker.Verbose = *verbose

	routers, err := a.cfg.Select(ids)
	if err != nil {
		return err
	}
	proj, err := compose.Prepare(a.cfg, a.eng)
	if err != nil {
		return err
	}

	// Selecting specific routers means stopping just those; `down` with no
	// ids tears the whole project down, including its network.
	if len(ids) > 0 {
		names := routerIDs(routers, config.VM)
		args := append([]string{"rm", "-sf"}, names...)
		return a.docker.Compose(ctx, proj.ComposePath, args...)
	}
	downArgs := []string{"down", "--remove-orphans"}
	if *purge {
		downArgs = append(downArgs, "--rmi", "local")
	}
	return a.docker.Compose(ctx, proj.ComposePath, downArgs...)
}

func (a *app) shell(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("shell", flag.ContinueOnError)
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	r, err := a.single(ids, "shell")
	if err != nil {
		return err
	}
	name := a.containerName(r.ID)
	// -l so the login profile runs and the prompt looks like the router's.
	return a.docker.Run(ctx, "exec", "-it", name, "/bin/sh", "-l")
}

func (a *app) exec(ctx context.Context, args []string) error {
	// Everything after -- is the command, verbatim.
	var ids []string
	var cmd []string
	for i, s := range args {
		if s == "--" {
			ids = args[:i]
			cmd = args[i+1:]
			break
		}
	}
	if cmd == nil {
		return fmt.Errorf("usage: owlab exec <router> -- <command>")
	}
	if len(cmd) == 0 {
		return fmt.Errorf("no command given after --")
	}
	r, err := a.single(ids, "exec")
	if err != nil {
		return err
	}
	// Joined and handed to sh -c rather than exec'd directly, so that pipes
	// and redirection in the command work the way the developer typed them.
	return a.docker.Run(ctx, "exec", a.containerName(r.ID), "/bin/sh", "-c", strings.Join(cmd, " "))
}

// sync copies the project's source tree into running routers.
func (a *app) sync(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	watch := fs.Bool("watch", false, "keep running, re-syncing whenever a source file changes")
	interval := fs.Duration("interval", time.Second, "how often to poll for changes when watching")
	verbose := fs.Bool("v", false, "list every file synced")
	noBuild := fs.Bool("no-build", false, "skip project.build")
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}

	routers, err := a.cfg.Select(ids)
	if err != nil {
		return err
	}
	opts := syncpkg.Options{
		Config:        a.cfg,
		ContainerName: a.containerName,
		Verbose:       *verbose,
		SkipBuild:     *noBuild,
	}

	report := func(results []syncpkg.Result) {
		var failed int
		for _, res := range results {
			if res.Err != nil {
				fmt.Fprintf(os.Stderr, "! %-14s %v\n", res.Router, res.Err)
				failed++
				continue
			}
			fmt.Printf("  %-14s %d files, %s\n", res.Router, res.Files, humanBytes(res.Bytes))
		}
		if failed > 0 && failed == len(results) {
			fmt.Fprintf(os.Stderr, "\nAre the routers running? Try `owlab status`.\n")
		}
	}

	if *watch {
		report(syncpkg.Run(ctx, opts, routers))
		return syncpkg.Watch(ctx, opts, routers, *interval, report)
	}

	results := syncpkg.Run(ctx, opts, routers)
	report(results)
	for _, res := range results {
		if res.Err != nil {
			return fmt.Errorf("sync failed on %s", res.Router)
		}
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n > 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n > 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// install adds packages to a running router.
//
// This is the throwaway half of package selection: `packages:` in
// owlab.yaml is what the image is built with and survives a rebuild,
// while this is for trying something out. The distinction matters because a
// container holds no volumes — `owlab up --rebuild` is a factory reset, and
// anything installed this way is gone.
func (a *app) install(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	all := fs.Bool("all", false, "install on every router instead of one")
	verbose := fs.Bool("v", false, "echo the docker commands being run")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	a.docker.Verbose = *verbose

	var routers []*config.Router
	var pkgs []string

	if *all {
		routers, _ = a.cfg.Select(nil)
		pkgs = rest
	} else {
		// First positional is the router when it names one; otherwise every
		// positional is a package and the router must be unambiguous.
		if len(rest) > 0 {
			if r, ok := a.cfg.Router(rest[0]); ok {
				routers = []*config.Router{r}
				pkgs = rest[1:]
			}
		}
		if routers == nil {
			r, err := a.single(nil, "install")
			if err != nil {
				return fmt.Errorf("%w\n\nusage: owlab install <router> <package>... (or --all)", err)
			}
			routers = []*config.Router{r}
			pkgs = rest
		}
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("no packages given\n\nusage: owlab install <router> <package>... (or --all)")
	}

	var failed []string
	for _, r := range routers {
		if r.Fidelity == config.VM {
			continue
		}
		// apk and opkg differ by release, and a router's own package manager
		// is the only correct one to use.
		cmd := "opkg update >/dev/null 2>&1; opkg install " + strings.Join(pkgs, " ")
		if r.PackageManager() == config.APK {
			cmd = "apk add " + strings.Join(pkgs, " ")
		}
		fmt.Printf("== %s (%s)\n", r.ID, r.PackageManager())
		if err := a.docker.Run(ctx, "exec", a.containerName(r.ID), "/bin/sh", "-c", cmd); err != nil {
			failed = append(failed, r.ID)
		}
	}
	if len(failed) > 0 {
		// Not fatal: feeds differ between releases and between a fork and
		// upstream, and "missing on 24.10" is information, not an error.
		fmt.Fprintf(os.Stderr, "\n! install failed on: %s\n", strings.Join(failed, ", "))
	}

	// LuCI caches its dispatch tree and module list; a new app is invisible
	// until both are dropped. This is exactly luci.mk's own postinst.
	for _, r := range routers {
		if r.Fidelity == config.VM {
			continue
		}
		_ = a.docker.Run(ctx, "exec", a.containerName(r.ID), "/bin/sh", "-c",
			"rm -f /tmp/luci-indexcache*; rm -rf /tmp/luci-modulecache; /etc/init.d/rpcd reload")
	}
	return nil
}

func (a *app) logs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("f", false, "follow the log")
	tail := fs.String("tail", "all", "number of lines to show from the end")
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	routers, err := a.cfg.Select(ids)
	if err != nil {
		return err
	}
	proj, err := compose.Prepare(a.cfg, a.eng)
	if err != nil {
		return err
	}
	logArgs := []string{"logs", "--tail", *tail}
	if *follow {
		logArgs = append(logArgs, "-f")
	}
	logArgs = append(logArgs, routerIDs(routers, config.VM)...)
	return a.docker.Compose(ctx, proj.ComposePath, logArgs...)
}

func (a *app) status(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	routers, err := a.cfg.Select(ids)
	if err != nil {
		return err
	}

	fmt.Printf("project  %s\n", a.cfg.Project.Name)
	fmt.Printf("engine   %s\n\n", a.eng.Describe())

	fmt.Printf("%-14s %-12s %-9s %-18s %-9s %-8s %s\n",
		"ROUTER", "RELEASE", "PKGMGR", "ARCH", "FIDELITY", "STATE", "LUCI")
	for _, r := range routers {
		state := a.containerState(ctx, r.ID)
		url := fmt.Sprintf("http://localhost:%d", r.Ports.HTTP)
		if r.Fidelity == config.VM {
			state = "n/a"
		}
		fmt.Printf("%-14s %-12s %-9s %-18s %-9s %-8s %s\n",
			r.ID,
			string(r.Distro)[:3]+" "+r.Release,
			string(r.PackageManager()),
			r.Arch,
			string(r.Fidelity),
			state,
			url,
		)
	}
	return nil
}

func (a *app) open(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	r, err := a.single(ids, "open")
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://localhost:%d", r.Ports.HTTP)
	fmt.Println(url)
	return openBrowser(url)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// single resolves exactly one router, defaulting to the only one defined.
func (a *app) single(ids []string, cmd string) (*config.Router, error) {
	switch len(ids) {
	case 1:
		r, ok := a.cfg.Router(ids[0])
		if !ok {
			return nil, fmt.Errorf("no router %q in %s (have: %s)",
				ids[0], config.FileName, strings.Join(a.cfg.RouterIDs(), ", "))
		}
		return r, nil
	case 0:
		if len(a.cfg.Routers) == 1 {
			return &a.cfg.Routers[0], nil
		}
		return nil, fmt.Errorf("%s needs a router id (have: %s)",
			cmd, strings.Join(a.cfg.RouterIDs(), ", "))
	default:
		return nil, fmt.Errorf("%s takes one router, got %d", cmd, len(ids))
	}
}

func (a *app) containerName(id string) string {
	return compose.ProjectName(a.cfg.Project.Name) + "-" + id
}

func (a *app) containerState(ctx context.Context, id string) string {
	out, err := a.docker.Quiet(ctx, "inspect", "-f", "{{.State.Status}}", a.containerName(id))
	if err != nil {
		return "-"
	}
	return strings.TrimSpace(out)
}

// routerIDs lists router ids, skipping any with the excluded fidelity.
func routerIDs(routers []*config.Router, skip config.Fidelity) []string {
	out := make([]string, 0, len(routers))
	for _, r := range routers {
		if r.Fidelity == skip {
			continue
		}
		out = append(out, r.ID)
	}
	return out
}

// checkFidelity refuses tiers this machine cannot deliver, rather than
// starting something that will quietly be less than what was asked for.
func (a *app) checkFidelity(routers []*config.Router) error {
	for _, r := range routers {
		switch r.Fidelity {
		case config.Full:
			ok, reason := a.eng.HostKernelWiFi()
			if !ok {
				return fmt.Errorf(
					"router %q asks for fidelity full, which needs real mac80211_hwsim radios, but %s.\n\n"+
						"Use fidelity basic (LuCI's wireless pages still render from config), or\n"+
						"fidelity vm, which runs a real OpenWrt kernel under QEMU on this host.",
					r.ID, reason)
			}
		case config.VM:
			return fmt.Errorf("router %q asks for fidelity vm, which is not implemented yet", r.ID)
		}
	}
	return nil
}

// waitForLuCI polls each router's forwarded HTTP port until it answers.
//
// A router is "up" when uhttpd serves, not when the container starts: procd
// has to run the whole boot sequence, apply uci-defaults and start services,
// which takes a few seconds on first boot.
func (a *app) waitForLuCI(ctx context.Context, routers []*config.Router) map[string]bool {
	ready := map[string]bool{}
	deadline := time.Now().Add(90 * time.Second)
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	pending := map[string]*config.Router{}
	for _, r := range routers {
		if r.Fidelity != config.VM {
			pending[r.ID] = r
		}
	}
	if len(pending) > 0 {
		fmt.Fprintf(os.Stderr, "waiting for LuCI")
	}
	for len(pending) > 0 && time.Now().Before(deadline) {
		if ctx.Err() != nil {
			break
		}
		for id, r := range pending {
			addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(r.Ports.HTTP))
			resp, err := client.Get("http://" + addr + "/")
			if err == nil {
				resp.Body.Close()
				ready[id] = true
				delete(pending, id)
			}
		}
		if len(pending) > 0 {
			fmt.Fprint(os.Stderr, ".")
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
	fmt.Fprintln(os.Stderr)
	return ready
}

func (a *app) printReady(routers []*config.Router, ready map[string]bool) error {
	var stalled []string
	for _, r := range routers {
		if r.Fidelity == config.VM {
			continue
		}
		url := fmt.Sprintf("http://localhost:%d", r.Ports.HTTP)
		mark := " "
		if ready != nil && !ready[r.ID] {
			mark = "!"
			stalled = append(stalled, r.ID)
		}
		fmt.Printf("%s %-14s %s   ssh -p %d root@localhost\n", mark, r.ID, url, r.Ports.SSH)
	}
	if len(stalled) > 0 {
		fmt.Fprintf(os.Stderr,
			"\n! %s did not answer on HTTP in time. Check `owlab logs %s`.\n",
			strings.Join(stalled, ", "), stalled[0])
	}
	return nil
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
