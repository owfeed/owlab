package main

import (
	"archive/tar"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/VizzleTF/owlab/internal/compose"
	"github.com/VizzleTF/owlab/internal/config"
	"github.com/VizzleTF/owlab/internal/qemu"
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
	if err := a.checkFidelity(ctx, routers); err != nil {
		return err
	}
	containers, vms := splitTiers(routers)

	if len(containers) > 0 {
		proj, err := compose.Prepare(a.cfg, a.eng)
		if err != nil {
			return err
		}
		names := routerIDs(containers)

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
	}

	for _, r := range vms {
		if err := a.startVM(ctx, r, *rebuild, *verbose); err != nil {
			return err
		}
	}

	if *noWait {
		return a.printReady(routers, nil)
	}
	ready := a.waitForLuCI(ctx, routers)
	return a.printReady(routers, ready)
}

// startVM boots one fidelity-vm router and sets it up if it is new.
//
// Provisioning is a property of the DISK, not of the run: a VM keeps its
// filesystem across `owlab down`, so the packages and the overlay are
// installed once and every later start is a plain boot. `--rebuild` is what
// throws the disk away, and it is the only factory reset this tier has.
func (a *app) startVM(ctx context.Context, r *config.Router, rebuild, verbose bool) error {
	v := qemu.New(a.cfg, r)
	if v.Running() && !rebuild {
		return nil
	}
	if rebuild && v.Running() {
		if err := v.Stop(ctx); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "== %s (vm, %s %s on %s)\n", r.ID, r.Title(), r.Release, r.Arch)
	if err := v.Start(ctx, qemu.StartOptions{
		Rebuild:  rebuild,
		Progress: os.Stderr,
		Verbose:  verbose,
	}); err != nil {
		return fmt.Errorf("%s: %w", r.ID, err)
	}
	if v.Provisioned() {
		return nil
	}
	if err := v.Provision(ctx, os.Stderr); err != nil {
		return fmt.Errorf("%s: %w", r.ID, err)
	}
	return nil
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
	containers, vms := splitTiers(routers)

	// VMs first, and gracefully: the guest is asked to power itself off so
	// its overlay is unmounted cleanly. A container has no such filesystem to
	// lose.
	for _, r := range vms {
		v := qemu.New(a.cfg, r)
		if !v.Running() && !*purge {
			continue
		}
		fmt.Fprintf(os.Stderr, "stopping %s\n", r.ID)
		if *purge {
			// --purge on a VM means the disks too. Without it the router
			// keeps everything installed on it, which is the point of the
			// tier — `down` is a shutdown, not a reset.
			if err := v.Destroy(ctx); err != nil {
				return err
			}
			continue
		}
		if err := v.Stop(ctx); err != nil {
			return err
		}
	}

	if len(containers) == 0 {
		return nil
	}
	proj, err := compose.Prepare(a.cfg, a.eng)
	if err != nil {
		return err
	}

	// Selecting specific routers means stopping just those; `down` with no
	// ids tears the whole project down, including its network.
	if len(ids) > 0 {
		args := append([]string{"rm", "-sf"}, routerIDs(containers)...)
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
	return a.interactive(ctx, r)
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
	run, err := a.execFor(r)
	if err != nil {
		return err
	}
	// Joined and handed to sh -c rather than exec'd directly, so that pipes
	// and redirection in the command work the way the developer typed them.
	return run(ctx, strings.Join(cmd, " "), nil, os.Stdout)
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
		Config:    a.cfg,
		Exec:      a.execFor,
		Verbose:   *verbose,
		SkipBuild: *noBuild,
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

	// A path to a package file on the host is not a name the router can
	// resolve, so those are copied in first. This is what makes the output of
	// `owlab build` directly installable.
	var localFiles []string
	var names []string
	for _, p := range pkgs {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			localFiles = append(localFiles, p)
			continue
		}
		names = append(names, p)
	}

	var failed []string
	for _, r := range routers {
		run, err := a.execFor(r)
		if err != nil {
			fmt.Fprintf(os.Stderr, "! %s: %v\n", r.ID, err)
			failed = append(failed, r.ID)
			continue
		}

		var installArgs []string
		installArgs = append(installArgs, names...)
		for _, f := range localFiles {
			dest := "/tmp/" + filepath.Base(f)
			body, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			// Pushed as a tar on stdin rather than `docker cp`, because that
			// is the one file-transfer both tiers share: a VM is reached over
			// ssh, and dropbear ships no sftp-server for scp to use.
			archive, err := tarFile(dest, body)
			if err != nil {
				return err
			}
			if err := run(ctx, "tar -C / -xf -", archive, os.Stderr); err != nil {
				failed = append(failed, r.ID)
				continue
			}
			installArgs = append(installArgs, dest)
		}
		if len(installArgs) == 0 {
			continue
		}

		// apk and opkg differ by release, and a router's own package manager
		// is the only correct one to use. Local files need the untrusted flag
		// on apk: they carry no signature the router's keyring knows.
		cmd := "opkg update >/dev/null 2>&1; opkg install " + strings.Join(installArgs, " ")
		if r.PackageManager() == config.APK {
			flags := ""
			if len(localFiles) > 0 {
				flags = "--allow-untrusted "
			}
			cmd = "apk add " + flags + strings.Join(installArgs, " ")
		}
		fmt.Printf("== %s (%s)\n", r.ID, r.PackageManager())
		if err := run(ctx, cmd, nil, os.Stdout); err != nil {
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
		run, err := a.execFor(r)
		if err != nil {
			continue
		}
		_ = run(ctx, "rm -f /tmp/luci-indexcache*; rm -rf /tmp/luci-modulecache; /etc/init.d/rpcd reload",
			nil, io.Discard)
	}
	return nil
}

// tarFile wraps one file in a tar stream rooted at /.
func tarFile(dest string, body []byte) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name:   strings.TrimPrefix(dest, "/"),
		Mode:   0o644,
		Size:   int64(len(body)),
		Format: tar.FormatGNU,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(body); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	containers, vms := splitTiers(routers)

	// A VM's log is its serial console, captured to a file. That is the same
	// stream a container's log is — everything the router printed from the
	// first kernel line onward — so the two are shown the same way and the
	// difference stays an implementation detail.
	for _, r := range vms {
		v := qemu.New(a.cfg, r)
		if len(routers) > 1 {
			fmt.Printf("== %s\n", r.ID)
		}
		if err := tailFile(ctx, v.ConsolePath(), *tail, *follow && len(routers) == 1); err != nil {
			fmt.Fprintf(os.Stderr, "! %s: %v\n", r.ID, err)
		}
	}
	if len(containers) == 0 {
		return nil
	}

	proj, err := compose.Prepare(a.cfg, a.eng)
	if err != nil {
		return err
	}
	logArgs := []string{"logs", "--tail", *tail}
	if *follow {
		logArgs = append(logArgs, "-f")
	}
	logArgs = append(logArgs, routerIDs(containers)...)
	return a.docker.Compose(ctx, proj.ComposePath, logArgs...)
}

// tailFile prints the end of a file, optionally following it.
func tailFile(ctx context.Context, path, tail string, follow bool) error {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no console log yet — has it been started?")
		}
		return err
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if n, convErr := strconv.Atoi(tail); convErr == nil && n < len(lines) {
		lines = lines[len(lines)-n:]
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	if !follow {
		return nil
	}

	// Polling rather than inotify, for the same reason sync --watch polls:
	// this has to behave the same on macOS, Linux and Windows, and a serial
	// console produces a line every few seconds at most.
	offset := int64(len(body))
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return err
		}
		more, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return err
		}
		if len(more) > 0 {
			offset += int64(len(more))
			fmt.Print(string(more))
		}
	}
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
	if a.cfg.HasContainers() {
		fmt.Printf("engine   %s\n", a.eng.Describe())
	} else {
		// No container routers means the engine was never detected, and
		// printing an empty field reads like a failed probe.
		fmt.Printf("engine   qemu only (no container routers)\n")
	}
	login := "root, empty password"
	if pw := compose.RootPassword(); pw != "" {
		login = "root / " + pw
	}
	fmt.Printf("login    %s\n\n", login)

	fmt.Printf("%-14s %-12s %-9s %-18s %-9s %-8s %s\n",
		"ROUTER", "RELEASE", "PKGMGR", "ARCH", "FIDELITY", "STATE", "LUCI")
	for _, r := range routers {
		state := a.containerState(ctx, r.ID)
		url := fmt.Sprintf("http://localhost:%d", r.Ports.HTTP)
		if r.Fidelity == config.VM {
			state = qemu.New(a.cfg, r).State()
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

// routerIDs lists router ids.
func routerIDs(routers []*config.Router) []string {
	out := make([]string, 0, len(routers))
	for _, r := range routers {
		out = append(out, r.ID)
	}
	return out
}

// checkFidelity refuses tiers this machine cannot deliver, rather than
// starting something that will quietly be less than what was asked for.
func (a *app) checkFidelity(ctx context.Context, routers []*config.Router) error {
	for _, r := range routers {
		switch r.Fidelity {
		case config.VM:
			// Probed, not assumed: the emulator and the firmware are the two
			// things a machine can be missing, and finding out at boot time
			// means a half-created VM directory and a confusing QEMU error.
			d := qemu.Inspect(ctx, r.Target())
			if d.Err != nil {
				return fmt.Errorf("router %q asks for fidelity vm: %w", r.ID, d.Err)
			}
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
		pending[r.ID] = r
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
		url := fmt.Sprintf("http://localhost:%d", r.Ports.HTTP)
		mark := " "
		if ready != nil && !ready[r.ID] {
			mark = "!"
			stalled = append(stalled, r.ID)
		}
		fmt.Printf("%s %-14s %s   ssh -p %d root@localhost\n", mark, r.ID, url, r.Ports.SSH)
	}
	// Say how to log in. LuCI asks on the first page load, and "leave it
	// blank" is not something a developer should have to discover.
	if pw := compose.RootPassword(); pw != "" {
		fmt.Printf("\n  log in as root / %s\n", pw)
	} else {
		fmt.Printf("\n  log in as root, empty password   (set OWLAB_ROOT_PASSWORD to require one)\n")
	}

	// ssh keys. Silence here is the failure mode worth avoiding: without a
	// key, `ssh root@localhost` prompts for a password nobody was told about,
	// and the reason is a file that does not exist on the host.
	if keys, src := compose.PublicKeys(); len(keys) > 0 {
		names := make([]string, 0, len(keys))
		for _, k := range keys {
			names = append(names, filepath.Base(k))
		}
		fmt.Printf("  ssh keys installed: %s   (from %s)\n", strings.Join(names, ", "), src)
	} else {
		fmt.Fprintf(os.Stderr,
			"\n! no ssh public key found — `ssh root@localhost` will ask for a password.\n"+
				"!   Generate one:   ssh-keygen -t ed25519\n"+
				"!   Or point owlab at an existing key:  export OWLAB_PUBKEY=/path/to/key.pub\n"+
				"!   Then re-run `owlab up` to install it.\n")
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
