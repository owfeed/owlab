package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	checkpkg "github.com/VizzleTF/owlab/internal/check"
	"github.com/VizzleTF/owlab/internal/compose"
	"github.com/VizzleTF/owlab/internal/config"
	"github.com/VizzleTF/owlab/internal/pkgmgr"
	"github.com/VizzleTF/owlab/internal/qemu"
	syncpkg "github.com/VizzleTF/owlab/internal/sync"
	"github.com/VizzleTF/owlab/internal/tarx"
)

// test is a whole CI run as one command: start the routers, install the
// package, ask the router whether it worked, tear everything down, exit 0 or 1.
//
// It exists because the alternative is what every maintainer writes by hand —
// up, then install, then a curl that gets a 403 because LuCI needs a session,
// then a teardown that does not run when the curl failed. Each of those steps
// is easy, the assembly is not, and the assembly is the same for everybody.
func (a *app) test(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var releases, installs, asserts, packages stringList
	fs.Var(&releases, "release", "release to test against, repeatable or space/comma separated (synthesizes routers; no owlab.yaml needed)")
	fs.Var(&installs, "install", "package file or feed name to install, repeatable; globs are expanded")
	feedURL := fs.String("feed", "", "package feed to add before installing: the index URL for apk, the directory URL for opkg")
	feedKey := fs.String("feed-key", "", "the feed's public key file (for opkg the filename must be the key id)")
	feedName := fs.String("feed-name", "owlab-feed", "name the feed is registered under")
	fs.Var(&asserts, "assert", "assertion to run on every router, repeatable")
	fs.Var(&packages, "packages", "packages for synthesized routers (+name adds to the stock set)")
	distro := fs.String("distro", "", "distribution for synthesized routers: openwrt or immortalwrt")
	arch := fs.String("arch", "", "architecture for synthesized routers (default: this host's)")
	fixtures := fs.String("fixtures", "", "fixture profiles for synthesized routers, e.g. \"none\" or \"all\"")
	syncSrc := fs.Bool("sync", false, "sync the project sources in before asserting, as `owlab sync` does")
	keep := fs.Bool("keep", false, "leave the routers running afterwards")
	rebuild := fs.Bool("rebuild", false, "rebuild images from scratch")
	asJSON := fs.Bool("json", false, "write a machine-readable report to stdout")
	timeout := fs.Duration("timeout", 20*time.Minute, "budget for the whole run")
	verbose := fs.Bool("v", false, "echo the docker commands being run")
	ids, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	a.docker.Verbose = *verbose

	checks, err := checkpkg.ParseAll(asserts)
	if err != nil {
		return err
	}

	// Progress goes to stderr whenever the report is machine-readable, so that
	// `owlab test --json | jq` is a pipe and not a parse error. Docker's own
	// stdout moves with it: BuildKit prints its layer progress there.
	out := os.Stdout
	if *asJSON {
		out = os.Stderr
		a.docker.Out = os.Stderr
	}
	say := func(format string, v ...any) { fmt.Fprintf(out, format, v...) }

	if err := a.resolveTestConfig(releases.values(), *distro, *arch, *fixtures, packages); err != nil {
		return err
	}
	routers, err := a.cfg.Select(ids)
	if err != nil {
		return err
	}
	if err := a.checkFidelity(ctx, routers); err != nil {
		return err
	}

	// A feed needs both halves: a URL with no key installs nothing, a key with
	// no URL points at nothing.
	if (*feedURL == "") != (*feedKey == "") {
		return fmt.Errorf("--feed and --feed-key go together")
	}
	var feedSpec *feedSource
	if *feedURL != "" {
		feedSpec = &feedSource{Name: *feedName, URL: *feedURL, Key: *feedKey}
	}

	files, err := resolveInstalls(a.cfg.Dir, installs)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	rep := testReport{
		Schema:  "owlab.test/v1",
		Owlab:   Version(),
		Project: a.cfg.Project.Name,
		OK:      true,
	}

	containers, vms := splitTiers(routers)
	resolveBaseImages(ctx, containers)

	// Deferred, and deliberately not conditional on success. A failed CI run
	// that leaves containers holding 8080 and 2222 makes the *next* run fail
	// for an unrelated reason, and on a self-hosted runner that is a morning
	// gone.
	defer func() {
		if *keep {
			say("\nrouters left running (--keep). Stop them with `owlab down`.\n")
			return
		}
		// A fresh context: the one above may already be cancelled, which is
		// exactly when tearing down matters most.
		stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		for _, r := range vms {
			_ = qemu.New(a.cfg, r).Stop(stop)
		}
		if len(containers) > 0 {
			if proj, err := compose.Render(a.cfg, a.eng); err == nil {
				_ = a.docker.Compose(stop, proj.ComposePath, "down", "--remove-orphans", "-t", "5")
			}
		}
	}()

	if len(containers) > 0 {
		proj, err := compose.Prepare(a.cfg, a.eng)
		if err != nil {
			return err
		}
		names := routerIDs(containers)
		say("building %s\n", strings.Join(names, ", "))

		buildArgs := []string{"build"}
		if *rebuild {
			buildArgs = append(buildArgs, "--no-cache", "--pull")
		}
		buildArgs = append(buildArgs, names...)
		if err := a.docker.Compose(ctx, proj.ComposePath, buildArgs...); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
		if err := a.docker.Compose(ctx, proj.ComposePath, append([]string{"up", "-d", "--remove-orphans"}, names...)...); err != nil {
			return fmt.Errorf("start failed: %w", err)
		}
	}
	for _, r := range vms {
		if err := a.startVM(ctx, r, *rebuild, *verbose); err != nil {
			return err
		}
	}

	ready := a.waitForLuCI(ctx, routers)

	for _, r := range routers {
		rr := routerReport{
			ID:         r.ID,
			Distro:     string(r.Distro),
			Release:    r.Release,
			Arch:       r.Arch,
			Fidelity:   string(r.Fidelity),
			PkgManager: string(r.PackageManager()),
			LuCI:       r.LuCIURL(),
			OK:         true,
		}
		say("\n== %s   %s %s, %s, %s\n", r.ID, r.Title(), r.Release, r.Arch, r.PackageManager())

		record := func(res checkpkg.Result) bool {
			rr.Steps = append(rr.Steps, res)
			if !res.OK {
				rr.OK = false
			}
			mark := "ok  "
			if !res.OK {
				mark = "FAIL"
			}
			detail := res.Detail
			if detail != "" {
				detail = "   " + detail
			}
			say("   %s  %-46s%s\n", mark, res.Check, detail)
			return res.OK
		}

		// Booting is itself a check, and the one that fails most often: a
		// package whose uci-defaults exits non-zero can stop procd's boot
		// before uhttpd ever starts.
		up := checkpkg.Result{Check: "boot", Kind: "up", OK: ready[r.ID]}
		if !up.OK {
			up.Detail = "LuCI did not answer on " + rr.LuCI + " — see `owlab logs " + r.ID + "`"
		}
		if record(up) {
			run, err := a.execFor(r)
			if err != nil {
				record(checkpkg.Result{Check: "reach", Kind: "up", Detail: err.Error()})
			} else {
				if len(files) > 0 || len(installs) > 0 {
					record(a.testInstall(ctx, r, run, files, installs, feedSpec))
				}
				if *syncSrc && rr.OK {
					record(a.testSync(ctx, r))
				}
				if rr.OK {
					target := &checkpkg.Target{
						Router:     r.ID,
						BaseURL:    r.LuCIURL(),
						Password:   compose.RootPassword(),
						PkgManager: string(r.PackageManager()),
					}
					for _, c := range checks {
						record(c.Run(ctx, target, checkpkg.Exec(run)))
					}
				}
			}
		}

		if !rr.OK {
			rep.OK = false
			// The log is where the reason almost always is, and in CI the
			// container is gone by the time anybody looks.
			a.dumpLog(ctx, r, out)
		}
		rep.Routers = append(rep.Routers, rr)
	}

	passed := 0
	for _, r := range rep.Routers {
		if r.OK {
			passed++
		}
	}
	say("\n%d of %d routers passed\n", passed, len(rep.Routers))

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	}
	if !rep.OK {
		// A plain sentinel: the exit status is the whole interface in CI, and
		// every failure has already been printed one line each.
		return errTestFailed
	}
	return nil
}

// errTestFailed reports assertion failures without printing anything more.
var errTestFailed = errors.New("test failed")

// testReport is the --json document. The schema field is first because
// consumers of a machine-readable output need a way to tell when it changed.
type testReport struct {
	Schema  string         `json:"schema"`
	Owlab   string         `json:"owlab"`
	Project string         `json:"project"`
	OK      bool           `json:"ok"`
	Routers []routerReport `json:"routers"`
}

type routerReport struct {
	ID         string            `json:"id"`
	Distro     string            `json:"distro"`
	Release    string            `json:"release"`
	Arch       string            `json:"arch"`
	Fidelity   string            `json:"fidelity"`
	PkgManager string            `json:"package_manager"`
	LuCI       string            `json:"luci"`
	OK         bool              `json:"ok"`
	Steps      []checkpkg.Result `json:"steps"`
}

// resolveTestConfig decides whether this run is driven by owlab.yaml or by
// --release flags, and produces a config either way.
func (a *app) resolveTestConfig(releases []string, distro, arch, fixtures string, packages []string) error {
	if len(releases) == 0 {
		if a.cfg == nil {
			return fmt.Errorf("%w\n\nEither run `owlab test` where an %s is, or name the releases:\n"+
				"  owlab test --release 25.12.4 --release 24.10.8 --assert 'http 200 /cgi-bin/luci/'",
				a.cfgErr, config.FileName)
		}
		if distro != "" || arch != "" || fixtures != "" || len(packages) > 0 {
			// Silently ignoring them would be worse: the run would pass while
			// testing something other than what was asked for.
			return fmt.Errorf("--distro, --arch, --fixtures and --packages only apply to routers created by --release; this run uses %s", config.FileName)
		}
		return nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	name := filepath.Base(dir)
	if a.cfg != nil {
		// A config is still worth having for its project name and its
		// install/build/post_sync rules, which is what --sync needs.
		dir, name = a.cfg.Dir, a.cfg.Project.Name
	}

	var fixtureList []string
	if fixtures != "" {
		fixtureList = strings.Fields(strings.ReplaceAll(fixtures, ",", " "))
	}
	cfg, err := config.Synthesize(config.SynthOptions{
		Name:     name,
		Dir:      dir,
		Distro:   config.Distro(distro),
		Releases: releases,
		Arch:     arch,
		Packages: packages,
		Fixtures: fixtureList,
	})
	if err != nil {
		return err
	}
	if a.cfg != nil {
		// Keep what the project says about itself; replace only the routers.
		cfg.Project = a.cfg.Project
	}
	a.cfg = cfg
	return nil
}

// testInstall installs the packages under test and insists that it worked.
//
// Unlike `owlab install`, a failure here is fatal. Interactively, "missing on
// 24.10" is information; in CI it is the answer to the question being asked.
// feedSource is a package feed a test adds before installing, so that a package
// can be installed BY NAME out of a signed index rather than from a file.
//
// That difference is the whole point of testing against a feed: installing a
// file proves the package works, and installing it by name proves the channel
// does -- the index parses, the URL does not redirect, and the key on the router
// matches the one that signed it. A file install cannot fail those ways.
type feedSource struct {
	Name string
	URL  string
	Key  string
}

func (a *app) testInstall(ctx context.Context, r *config.Router, run syncpkg.Exec, files, named []string, feedSrc *feedSource) checkpkg.Result {
	start := time.Now()
	feed := feedNames(named, files)
	res := checkpkg.Result{
		Kind:  "install",
		Check: "install " + strings.Join(append(append([]string{}, baseNames(files)...), feed...), " "),
	}
	defer func() { res.Seconds = time.Since(start).Round(time.Millisecond).Seconds() }()

	installArgs := append([]string{}, feed...)

	var pre string
	if feedSrc != nil {
		body, err := os.ReadFile(feedSrc.Key)
		if err != nil {
			res.Detail = err.Error()
			return res
		}
		dest := "/tmp/" + filepath.Base(feedSrc.Key)
		archive, err := tarx.OneFile(dest, body, 0o644)
		if err != nil {
			res.Detail = err.Error()
			return res
		}
		var log strings.Builder
		if err := run(ctx, "tar -C / -xf -", archive, &log); err != nil {
			res.Detail = fmt.Sprintf("pushing the feed key: %v %s", err, strings.TrimSpace(log.String()))
			return res
		}
		pre = pkgmgr.AddFeed(r.PackageManager(), feedSrc.Name, shQuote(feedSrc.URL), dest)
	}

	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			res.Detail = err.Error()
			return res
		}
		dest := "/tmp/" + filepath.Base(f)
		// A tar on stdin rather than `docker cp`: it is the one file transfer
		// both tiers share, since a VM is reached over ssh and dropbear ships
		// no sftp-server for scp to use.
		archive, err := tarx.OneFile(dest, body, 0o644)
		if err != nil {
			res.Detail = err.Error()
			return res
		}
		var log strings.Builder
		if err := run(ctx, "tar -C / -xf -", archive, &log); err != nil {
			res.Detail = fmt.Sprintf("pushing %s: %v %s", dest, err, strings.TrimSpace(log.String()))
			return res
		}
		installArgs = append(installArgs, dest)
	}

	cmd := pre + pkgmgr.Install(r.PackageManager(), installArgs, pkgmgr.Options{
		Update: len(feed) > 0 || feedSrc != nil,
		// A locally built package carries no signature the router's keyring
		// knows, and there is no key it could carry that would.
		Untrusted: len(files) > 0,
		Overwrite: true,
	})
	var log strings.Builder
	if err := run(ctx, cmd, nil, &log); err != nil {
		res.Detail = lastMeaningfulLine(log.String())
		return res
	}
	// LuCI caches its dispatch tree and its module list; a newly installed app
	// is invisible on every page until both are dropped. The same script a sync
	// runs, for the same reason.
	_ = run(ctx, syncpkg.ReloadScript(a.cfg.Project.Theme), nil, io.Discard)
	res.OK = true
	return res
}

// testSync runs the same sync the development loop uses, for projects whose
// sources are the thing under test rather than a built package.
func (a *app) testSync(ctx context.Context, r *config.Router) checkpkg.Result {
	start := time.Now()
	res := checkpkg.Result{Kind: "sync", Check: "sync"}
	results := syncpkg.Run(ctx, syncpkg.Options{Config: a.cfg, Exec: a.execFor}, []*config.Router{r})
	res.Seconds = time.Since(start).Round(time.Millisecond).Seconds()
	for _, s := range results {
		if s.Err != nil {
			res.Detail = s.Err.Error()
			return res
		}
		res.Detail = fmt.Sprintf("%d files, %s", s.Files, humanBytes(s.Bytes))
	}
	res.OK = true
	return res
}

// dumpLog prints a router's log after a failure.
func (a *app) dumpLog(ctx context.Context, r *config.Router, out *os.File) {
	log := a.routerLog(ctx, r, 40)
	if log == "" {
		fmt.Fprintf(out, "\n(no log for %s)\n", r.ID)
		return
	}
	fmt.Fprintf(out, "\n--- %s: last 40 log lines ---\n%s\n--- end of %s log ---\n", r.ID, log, r.ID)
}

// stringList is a repeatable flag that also accepts one value holding several,
// because a workflow file is a much more natural place to write
// `releases: "25.12.4 24.10.8"` than to repeat a flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// values splits every entry on whitespace and commas.
func (s stringList) values() []string {
	var out []string
	for _, v := range s {
		out = append(out, strings.Fields(strings.ReplaceAll(v, ",", " "))...)
	}
	return out
}

// resolveInstalls separates paths on the host from names in a feed, expanding
// globs.
//
// Globs matter because the artifact of an OpenWrt build carries a version in
// its file name that the workflow writing `dist/luci-app-mine-*.apk` does not
// know, and a shell that did not expand it (a quoted YAML scalar, say) would
// otherwise hand the literal string to the package manager.
func resolveInstalls(dir string, in []string) ([]string, error) {
	var files []string
	for _, item := range in {
		if strings.ContainsAny(item, "*?[") {
			pattern := item
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(dir, pattern)
			}
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return nil, fmt.Errorf("--install %s: %w", item, err)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("--install %s matched no files (looked in %s)", item, dir)
			}
			files = append(files, matches...)
			continue
		}
		p := item
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			files = append(files, p)
			continue
		}
		// Something that looks like a path but is not there is a mistake, not
		// a package name: no feed holds a name with a slash or a .apk suffix.
		if strings.ContainsAny(item, "/\\") || strings.HasSuffix(item, ".apk") || strings.HasSuffix(item, ".ipk") {
			return nil, fmt.Errorf("--install %s: no such file", item)
		}
	}
	return files, nil
}

// feedNames is everything from --install that was not a file on the host.
func feedNames(in, files []string) []string {
	isFile := map[string]bool{}
	for _, f := range files {
		isFile[filepath.Base(f)] = true
	}
	var out []string
	for _, item := range in {
		if strings.ContainsAny(item, "*?[/\\") || isFile[filepath.Base(item)] {
			continue
		}
		if strings.HasSuffix(item, ".apk") || strings.HasSuffix(item, ".ipk") {
			continue
		}
		out = append(out, item)
	}
	return out
}

func baseNames(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

// lastMeaningfulLine is the line of a package manager's output most likely to
// say why it refused, which is the last non-empty one.
func lastMeaningfulLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return "the package manager failed without saying why"
}
