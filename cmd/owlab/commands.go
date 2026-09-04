package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"owfeed.org/owlab/internal/compose"
	"owfeed.org/owlab/internal/config"
	"owfeed.org/owlab/internal/pkgmgr"
	"owfeed.org/owlab/internal/qemu"
	syncpkg "owfeed.org/owlab/internal/sync"
	"owfeed.org/owlab/internal/tarx"
	"owfeed.org/owlab/internal/upstream"
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
	resolveBaseImages(ctx, containers)

	if len(containers) > 0 {
		proj, err := compose.Prepare(a.cfg, a.eng)
		if err != nil {
			return err
		}
		names := routerIDs(containers)

		buildArgs := []string{"build"}
		if *rebuild {
			buildArgs = append(buildArgs, "--no-cache", "--pull")
		}
		buildArgs = append(buildArgs, names...)
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

	var ready map[string]bool
	if !*noWait {
		ready = a.waitForLuCI(ctx, routers)
	}
	if err := a.printReady(routers, ready); err != nil {
		return err
	}
	// After the table, because it is what should be left on screen — and
	// after the build, because the build no longer stops for it.
	return a.reportMissingExtras(ctx, containers)
}

// extrasFailedPath is where an image build records the extra_packages it could
// not install. See the extras layer in images/Dockerfile.
//
// Read back off the router rather than scraped out of the build log, for two
// reasons: the log is thousands of lines of buildkit progress that nobody
// reads, and a router started from a cached image never printed one at all.
const extrasFailedPath = "/etc/owlab/extras-failed"

// missingExtras asks one router which of its extra_packages did not install.
//
// Empty for almost every router: the file exists only when a build had
// something to record.
func missingExtras(ctx context.Context, run syncpkg.Exec) []string {
	var buf bytes.Buffer
	// A missing file is the normal case, not an error to propagate — and any
	// other failure here (a router that stopped, an engine that went away) is
	// already reported by whatever else is talking to the same router.
	if err := run(ctx, "cat "+extrasFailedPath+" 2>/dev/null || true", nil, &buf); err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// reportMissingExtras names, per router, the extra_packages its image was
// built without.
//
// This is the half of issue #12 that is not "stop cancelling the other
// routers". One bad file no longer fails the build, and tolerance with no
// report is silence — which is worse than the failure it replaced: the
// developer named a file, the router does not have it, and nothing but a line
// in the middle of a buildkit log would say so. Hence the non-zero exit as
// well: a script that runs `owlab up` before its own checks must not read
// "lab is up" as "lab is what the config describes".
func (a *app) reportMissingExtras(ctx context.Context, routers []*config.Router) error {
	var bad []string
	for _, r := range routers {
		if len(r.Extra) == 0 {
			continue
		}
		run, err := a.execFor(r)
		if err != nil {
			continue
		}
		missing := missingExtras(ctx, run)
		if len(missing) == 0 {
			continue
		}
		bad = append(bad, r.ID)
		fmt.Fprintf(os.Stderr, "\n! %s is running WITHOUT %s\n", r.ID, strings.Join(missing, ", "))
	}
	if len(bad) == 0 {
		return nil
	}
	fmt.Fprintf(os.Stderr,
		"!   Every other router built and started; only these packages are missing.\n"+
			"!   The package manager said why during the build — one router at a time shows it again:\n"+
			"!     owlab up --rebuild %s\n", bad[0])
	return errExtrasMissing
}

// errExtrasMissing is the exit status of an `owlab up` that started every
// router and could not put a named package on one of them. A plain sentinel:
// the routers and packages have already been printed one line each, and an
// "owlab: extra_packages missing" summary on top would bury which is which.
var errExtrasMissing = errors.New("extra_packages missing")

// resolveBaseImages asks the registry whether each router's upstream image
// exists, and switches the ones that do not onto the rootfs tarball.
//
// Only on a build path. The two are published on different schedules — the
// tarball when the release is built, the container image whenever the job that
// mirrors it runs — so a freshly pinned release can have one and not the
// other, and owlab can build from either.
func resolveBaseImages(ctx context.Context, routers []*config.Router) {
	for _, r := range routers {
		if r.FromTarball() || r.Image != "" {
			continue
		}
		if !upstream.HasContainerImage(ctx, r) {
			fmt.Fprintf(os.Stderr,
				"owlab: %s has no published container image yet; building %s from the rootfs tarball\n",
				r.BaseImage(), r.ID)
			r.UseTarball()
		}
	}
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
	// Render, not Prepare: stopping a router needs the compose file and
	// nothing in the build context, and `owlab down` must work with no network.
	proj, err := compose.Render(a.cfg, a.eng)
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
	feed := fs.String("feed", "", "package feed to add first: the index URL for apk, the directory URL for opkg")
	feedKey := fs.String("feed-key", "", "the feed's public key file (for opkg the filename must be the key id)")
	feedName := fs.String("feed-name", "owlab-feed", "name the feed is registered under")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	a.docker.Verbose = *verbose

	if (*feed == "") != (*feedKey == "") {
		return fmt.Errorf("--feed and --feed-key go together: a feed with no key installs nothing, " +
			"and a key with no feed points at nothing")
	}

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
	// resolve, so those are pushed in first. This is what makes the output of
	// `owlab build` directly installable.
	//
	// Read here, before any router is touched: an unreadable file is a mistake
	// in what was typed, not a property of one router, and reporting it before
	// half the lab has been changed is the useful order.
	var locals []localPackage
	var names []string
	for _, p := range pkgs {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			names = append(names, p)
			continue
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		// Pushed as a tar on stdin rather than `docker cp`, because that is the
		// one file-transfer both tiers share: a VM is reached over ssh, and
		// dropbear ships no sftp-server for scp to use.
		archive, err := tarx.OneFile("/tmp/"+filepath.Base(p), body, 0o644)
		if err != nil {
			return err
		}
		locals = append(locals, localPackage{dest: "/tmp/" + filepath.Base(p), archive: archive})
	}

	// The feed's key, read once. Same transport as a package file, for the same
	// reason: a VM has no scp to fall back on.
	var feedKeyArchive []byte
	var feedKeyDest string
	if *feedKey != "" {
		body, err := os.ReadFile(*feedKey)
		if err != nil {
			return err
		}
		feedKeyDest = "/tmp/" + filepath.Base(*feedKey)
		if feedKeyArchive, err = tarx.OneFile(feedKeyDest, body, 0o644); err != nil {
			return err
		}
	}

	var failed []string
	markFailed := func(id string) {
		if !slices.Contains(failed, id) {
			failed = append(failed, id)
		}
	}
	for _, r := range routers {
		run, err := a.execFor(r)
		if err != nil {
			fmt.Fprintf(os.Stderr, "! %s: %v\n", r.ID, err)
			markFailed(r.ID)
			continue
		}

		pm := r.PackageManager()
		installArgs := append([]string(nil), names...)
		pushed := 0
		pushErr := false
		for _, l := range locals {
			// The other release line's format, filtered before the push so the
			// router is never handed bytes it cannot read. It matters beyond
			// the wasted transfer: every file goes into one command, so an
			// .ipk on an apk router fails the .apk beside it — and apk says
			// only `v2 package format error`, which names no manager.
			if !pkgmgr.Installable(pm, l.dest) {
				fmt.Fprintln(os.Stderr, pkgmgr.SkipNote(pm, l.dest))
				continue
			}
			if err := run(ctx, "tar -C / -xf -", l.archive, os.Stderr); err != nil {
				// The router never received the file, so there is nothing to
				// install from it. Carrying on would run a package manager
				// against a path that is not there and report its confusion
				// instead of this one.
				fmt.Fprintf(os.Stderr, "! %s: pushing %s: %v\n", r.ID, l.dest, err)
				markFailed(r.ID)
				pushErr = true
				break
			}
			installArgs = append(installArgs, l.dest)
			pushed++
		}
		if pushErr {
			continue
		}
		// Everything named was for the other package manager. Said out loud,
		// because a package manager invoked with no arguments succeeds, and a
		// silent success here reads exactly like an install that worked.
		if len(installArgs) == 0 {
			fmt.Printf("== %s (%s)\n   nothing to install\n", r.ID, pm)
			continue
		}

		// A feed is added before anything is installed, so a name given on the
		// command line resolves out of it.
		//
		// Untrusted stays keyed to whether a local FILE is being installed. A
		// package that came from a feed must be installed without that flag or
		// the test proves nothing: the whole point of installing by name is that
		// the index's signature is what makes it acceptable, and
		// --allow-untrusted would accept it whether or not that held.
		var cmd string
		if feedKeyArchive != nil {
			if err := run(ctx, "tar -C / -xf -", feedKeyArchive, os.Stderr); err != nil {
				fmt.Fprintf(os.Stderr, "! %s: pushing the feed key: %v\n", r.ID, err)
				markFailed(r.ID)
				continue
			}
			url := pkgmgr.ResolveHost(*feed, r.Fidelity == config.VM)
			cmd = pkgmgr.AddFeed(pm, *feedName, shQuote(url), feedKeyDest)
		}

		cmd += pkgmgr.Install(pm, installArgs, pkgmgr.Options{
			Update: true,
			// Keyed to what this router actually received, not to what the
			// command line held: with every local file filtered out, only feed
			// names are left, and --allow-untrusted there would accept a
			// package whose index signature never checked out.
			Untrusted: pushed > 0,
		})
		fmt.Printf("== %s (%s)\n", r.ID, pm)
		if err := run(ctx, cmd, nil, os.Stdout); err != nil {
			markFailed(r.ID)
		}
	}
	if len(failed) > 0 {
		// Not fatal: feeds differ between releases and between a fork and
		// upstream, and "missing on 24.10" is information, not an error.
		fmt.Fprintf(os.Stderr, "\n! install failed on: %s\n", strings.Join(failed, ", "))
	}

	// LuCI caches its dispatch tree and module list; a new app is invisible
	// until both are dropped. The same script a sync runs, for the same
	// reason — a package can register a theme without selecting it either way.
	script := syncpkg.ReloadScript(a.cfg.Project.Theme)
	for _, r := range routers {
		run, err := a.execFor(r)
		if err != nil {
			continue
		}
		_ = run(ctx, script, nil, io.Discard)
	}
	return nil
}

// localPackage is a package file taken from the host, already wrapped in the
// tar stream that puts it on a router.
type localPackage struct {
	dest    string
	archive []byte
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

	proj, err := compose.Render(a.cfg, a.eng)
	if err != nil {
		return err
	}
	if *follow {
		logArgs := append([]string{"logs", "--tail", *tail, "-f"}, routerIDs(containers)...)
		return a.docker.Compose(ctx, proj.ComposePath, logArgs...)
	}

	logArgs := append([]string{"logs", "--tail", *tail}, routerIDs(containers)...)
	console, err := a.docker.Quiet(ctx, append([]string{"compose", "-f", proj.ComposePath}, logArgs...)...)
	if err == nil && strings.TrimSpace(console) != "" {
		fmt.Print(console)
		return nil
	}

	// Nothing on the container's stream is the normal case, not a broken one:
	// procd logs through syslogd, which writes to a ring buffer rather than to
	// the console, so a perfectly healthy router prints nothing there. Ask the
	// router itself instead — which is also the log with the useful content.
	n := 0
	if *tail != "all" {
		n, _ = strconv.Atoi(*tail)
	}
	for _, r := range containers {
		body := a.routerLog(ctx, r, n)
		if body == "" {
			continue
		}
		if len(routers) > 1 {
			fmt.Printf("== %s\n", r.ID)
		}
		fmt.Println(body)
	}
	return nil
}

// routerLog is a router's own syslog, falling back to whatever its tier
// captured when there is no router left to ask. Zero lines means all of them.
func (a *app) routerLog(ctx context.Context, r *config.Router, lines int) string {
	trim := func(s string) string {
		s = strings.TrimRight(s, "\n")
		if strings.TrimSpace(s) == "" {
			return ""
		}
		return s
	}

	if run, err := a.execFor(r); err == nil {
		cmd := "logread"
		if lines > 0 {
			cmd = fmt.Sprintf("logread | tail -n %d", lines)
		}
		var buf strings.Builder
		if err := run(ctx, cmd, nil, &buf); err == nil {
			if s := trim(buf.String()); s != "" {
				return s
			}
		}
	}

	if r.Fidelity == config.VM {
		body, err := os.ReadFile(qemu.New(a.cfg, r).ConsolePath())
		if err != nil {
			return ""
		}
		return trim(lastLines(string(body), lines))
	}
	args := []string{"logs", compose.ContainerName(a.cfg.Project.Name, r.ID)}
	if lines > 0 {
		args = []string{"logs", "--tail", strconv.Itoa(lines), compose.ContainerName(a.cfg.Project.Name, r.ID)}
	}
	out, err := a.docker.Quiet(ctx, args...)
	if err != nil {
		return ""
	}
	return trim(out)
}

// lastLines keeps the end of a body. Zero keeps all of it.
func lastLines(body string, n int) string {
	if n <= 0 {
		return body
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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
	asJSON := fs.Bool("json", false, "write the status as JSON")
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	routers, err := a.cfg.Select(ids)
	if err != nil {
		return err
	}

	if *asJSON {
		return a.statusJSON(ctx, routers)
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
		fmt.Printf("%-14s %-12s %-9s %-18s %-9s %-8s %s\n",
			r.ID,
			string(r.Distro)[:3]+" "+r.Release,
			string(r.PackageManager()),
			r.Arch,
			string(r.Fidelity),
			a.tierFor(r).State(ctx),
			r.LuCIURL(),
		)
	}
	return nil
}

// statusJSON is the same table, for a script.
//
// Everything a script would otherwise scrape out of the human output, plus the
// two things it cannot see there: the port numbers separately from the URL, and
// the container name, which is what any `docker` command it wants to run next
// needs.
func (a *app) statusJSON(ctx context.Context, routers []*config.Router) error {
	type routerStatus struct {
		ID         string `json:"id"`
		Distro     string `json:"distro"`
		Release    string `json:"release"`
		Arch       string `json:"arch"`
		PkgManager string `json:"package_manager"`
		Fidelity   string `json:"fidelity"`
		State      string `json:"state"`
		Container  string `json:"container,omitempty"`
		LuCI       string `json:"luci"`
		HTTPPort   int    `json:"http_port"`
		SSHPort    int    `json:"ssh_port"`
	}
	out := make([]routerStatus, 0, len(routers))
	for _, r := range routers {
		s := routerStatus{
			ID:         r.ID,
			Distro:     string(r.Distro),
			Release:    r.Release,
			Arch:       r.Arch,
			PkgManager: string(r.PackageManager()),
			Fidelity:   string(r.Fidelity),
			State:      a.tierFor(r).State(ctx),
			LuCI:       r.LuCIURL(),
			HTTPPort:   r.Ports.HTTP,
			SSHPort:    r.Ports.SSH,
		}
		// A VM has no container, and an empty string there would read like one
		// that could not be named.
		if r.Fidelity != config.VM {
			s.Container = compose.ContainerName(a.cfg.Project.Name, r.ID)
		}
		out = append(out, s)
	}

	engine := "qemu only (no container routers)"
	if a.cfg.HasContainers() {
		engine = a.eng.Describe()
	}
	doc := struct {
		Schema  string         `json:"schema"`
		Owlab   string         `json:"owlab"`
		Project string         `json:"project"`
		Engine  string         `json:"engine"`
		Routers []routerStatus `json:"routers"`
	}{"owlab.status/v1", Version(), a.cfg.Project.Name, engine, out}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
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
	url := r.LuCIURL()
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
		if r.Fidelity != config.VM {
			continue
		}
		// Probed, not assumed: the emulator and the firmware are the two things
		// a machine can be missing, and finding out at boot time means a
		// half-created VM directory and a confusing QEMU error.
		if d := qemu.Inspect(ctx, r.Target()); d.Err != nil {
			return fmt.Errorf("router %q asks for fidelity vm: %w", r.ID, d.Err)
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
		url := r.LuCIURL()
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

// shQuote wraps a value in single quotes for POSIX sh.
//
// Feed URLs come from a flag and land inside a generated script, so a value
// containing a quote must not be able to end the string and continue as
// commands.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
