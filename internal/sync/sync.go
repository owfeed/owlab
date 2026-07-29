// Package sync copies a package's source tree into running routers.
//
// This is the inner loop of LuCI development: edit a file, run sync, reload
// the page. It deliberately does NOT build a real .ipk — that is a separate,
// slower verification step. What it does is put the files exactly where
// luci.mk's own install rules would have put them, then drop the caches
// luci.mk's own postinst drops.
package sync

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"owfeed.org/owlab/internal/config"
	"owfeed.org/owlab/internal/tarx"
)

// Result is what one router's sync did.
type Result struct {
	Router string
	Files  int
	Bytes  int64
	Err    error
}

// skipDirs are never synced from the source tree.
//
// .git is obvious. node_modules and the build directories are the ones that
// silently turn a 200 KB sync into a 200 MB one.
var skipDirs = map[string]bool{
	".git":         true,
	".github":      true,
	"node_modules": true,
	".owlab":       true,
	"dist":         true,
	"build":        true,
	".venv":        true,
	"__pycache__":  true,
}

// Exec runs a shell script on a router, optionally feeding it stdin.
//
// The two tiers reach a router by completely different means — `docker exec`
// for a container, ssh for a VM — but everything sync does is "run this
// script, maybe with a tar on stdin". Naming that one operation is what keeps
// the rest of this file from having to know which kind of router it is
// talking to.
type Exec func(ctx context.Context, script string, stdin []byte, out io.Writer) error

// Options control one sync run.
type Options struct {
	// Config is the project being synced.
	Config *config.Config
	// Exec returns the transport for a router.
	Exec func(r *config.Router) (Exec, error)
	// Verbose lists every file.
	Verbose bool
	// SkipBuild suppresses project.build.
	SkipBuild bool
}

// Run syncs the project into each router and reloads LuCI there.
func Run(ctx context.Context, opts Options, routers []*config.Router) []Result {
	fail := func(err error) []Result {
		out := make([]Result, 0, len(routers))
		for _, r := range routers {
			out = append(out, Result{Router: r.ID, Err: err})
		}
		return out
	}

	if cmd := opts.Config.Project.Build; cmd != "" && !opts.SkipBuild {
		if err := runBuild(ctx, opts.Config.Dir, cmd, opts.Verbose); err != nil {
			return fail(err)
		}
	}

	archive, count, size, err := buildArchive(opts.Config, opts.Verbose)
	if err != nil {
		return fail(err)
	}
	if count == 0 {
		return fail(fmt.Errorf(
			"nothing to sync: none of the install: source directories exist in %s", opts.Config.Dir))
	}

	results := make([]Result, 0, len(routers))
	for _, r := range routers {
		res := Result{Router: r.ID, Files: count, Bytes: size}
		run, err := opts.Exec(r)
		if err != nil {
			res.Err = err
			results = append(results, res)
			continue
		}
		if err := extractInto(ctx, run, archive); err != nil {
			res.Err = err
			results = append(results, res)
			continue
		}
		if cmd := opts.Config.Project.PostSync; cmd != "" {
			if err := postSync(ctx, run, cmd, opts.Verbose); err != nil {
				res.Err = err
				results = append(results, res)
				continue
			}
		}
		if err := reload(ctx, run, opts.Config.Project.Theme); err != nil {
			res.Err = err
		}
		results = append(results, res)
	}
	return results
}

// runBuild runs project.build in the project directory.
//
// Via `sh -c` rather than by splitting the string, because the value is a
// command line — it can have arguments, a pipeline, or a redirect, and
// splitting on spaces would break all three. On Windows this needs a POSIX
// shell on PATH; a project that has no build step is unaffected.
func runBuild(ctx context.Context, dir, command string, verbose bool) error {
	if verbose {
		fmt.Fprintf(os.Stderr, "+ %s\n", command)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// The build's own output is the useful part of the error; the exit
		// status alone says nothing a developer can act on.
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			return fmt.Errorf("build command failed: %s: %w", command, err)
		}
		return fmt.Errorf("build command failed: %s\n%s", command, msg)
	}
	if verbose && out.Len() > 0 {
		fmt.Fprint(os.Stderr, out.String())
	}
	return nil
}

// buildArchive turns the project's install mapping into one tar stream rooted
// at /, so a single `tar -x` in the container places everything at once.
func buildArchive(cfg *config.Config, verbose bool) ([]byte, int, int64, error) {
	w := tarx.New()
	count := 0
	var total int64

	for _, pair := range cfg.InstallPairs() {
		src := filepath.Join(cfg.Dir, filepath.FromSlash(pair[0]))
		dst := pair[1]

		st, err := os.Stat(src)
		if err != nil {
			// A LuCI package has no reason to contain every one of the four
			// default directories, so a missing one is normal.
			continue
		}
		if !st.IsDir() {
			continue
		}

		err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			if d.IsDir() {
				if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			target := path.Join(dst, filepath.ToSlash(rel))
			if isConfigFile(target) {
				// Config files are the router's state, not the package's.
				// A real install ships them as conffiles and leaves an
				// existing one alone; overwriting them on every sync would
				// throw away whatever the developer just configured.
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return err
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			w.AddTime(target, body, info.Mode(), info.ModTime())
			count++
			total += int64(len(body))
			if verbose {
				fmt.Fprintf(os.Stderr, "  %s\n", target)
			}
			return nil
		})
		if err != nil {
			return nil, 0, 0, err
		}
	}

	archive, err := w.Bytes()
	if err != nil {
		return nil, 0, 0, err
	}
	return archive, count, total, nil
}

// isConfigFile reports whether a destination path is router state rather than
// package content.
func isConfigFile(target string) bool {
	return strings.HasPrefix(target, "/etc/config/") ||
		strings.HasPrefix(target, "/etc/uci-defaults/")
}

// extractInto streams the archive into a router and unpacks it at /.
//
// A tar on stdin rather than a file copy: one round trip for the whole tree,
// and it is the one operation both transports have. busybox tar reads the
// stream happily; the GNU format header is what keeps long paths intact.
func extractInto(ctx context.Context, run Exec, archive []byte) error {
	var out bytes.Buffer
	if err := run(ctx, "tar -C / -xf -", archive, &out); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// postSync runs the project's post_sync command on the router.
//
// Before reload(), so that anything it registers is present when the caches
// are dropped rather than one sync later.
func postSync(ctx context.Context, run Exec, command string, verbose bool) error {
	if verbose {
		fmt.Fprintf(os.Stderr, "+ (router) %s\n", command)
	}
	var out bytes.Buffer
	if err := run(ctx, command, nil, &out); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("post_sync failed: %s", msg)
	}
	if verbose && out.Len() > 0 {
		fmt.Fprint(os.Stderr, out.String())
	}
	return nil
}

// ReloadScript drops the caches that make LuCI keep serving the old tree.
//
// This is exactly luci.mk's own postinst. Without it, a new page does not
// appear in the menu and an edited template keeps rendering its old text —
// which reads like the sync silently failed.
//
// Exported because `owlab install` needs the same thing for the same reason: a
// newly installed app is invisible until both caches are dropped, whether the
// files arrived through a package manager or through a sync.
func ReloadScript(theme string) string {
	script := "rm -f /tmp/luci-indexcache*; rm -rf /tmp/luci-modulecache"
	if theme != "" {
		// Re-assert the theme: installing or syncing can register a theme
		// without selecting it, and a half-synced theme is the one case where
		// LuCI crashes rather than falling back.
		script += fmt.Sprintf(
			"; [ -d /www/luci-static/%s ] && { uci -q set luci.main.mediaurlbase=/luci-static/%s; uci -q commit luci; }",
			theme, theme)
	}
	return script + "; /etc/init.d/rpcd reload >/dev/null 2>&1 || true"
}

func reload(ctx context.Context, run Exec, theme string) error {
	var out bytes.Buffer
	if err := run(ctx, ReloadScript(theme), nil, &out); err != nil {
		return fmt.Errorf("reload: %s", strings.TrimSpace(out.String()))
	}
	return nil
}

// Watch re-syncs whenever a source file changes.
//
// Polling rather than filesystem events, deliberately: inotify does not
// propagate from a Windows drive into WSL, and Docker Desktop's shared
// filesystems have their own gaps. A poll is slower to notice but it notices
// everywhere, which for a tool whose whole point is running on any host is
// the right trade.
func Watch(ctx context.Context, opts Options, routers []*config.Router, interval time.Duration, onSync func([]Result)) error {
	last, err := snapshot(opts.Config)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "watching %s (poll every %s) — ctrl-c to stop\n", opts.Config.Dir, interval)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
		now, err := snapshot(opts.Config)
		if err != nil {
			continue
		}
		if now == last {
			continue
		}
		onSync(Run(ctx, opts, routers))

		// Re-snapshot AFTER syncing, not before. project.build writes into
		// the tree — a theme's cascade.css is generated from styles/ — so the
		// state that triggered this run is not the state the run leaves
		// behind. Reusing the pre-sync snapshot would see the build's own
		// output as a fresh change and rebuild forever.
		if after, err := snapshot(opts.Config); err == nil {
			last = after
		} else {
			last = now
		}
	}
}

// snapshot is a cheap fingerprint of the source tree: every file's path, size
// and mtime. Comparing strings avoids hashing contents, which on a theme with
// a few hundred assets would cost more than the sync itself.
//
// The roots are the install: directories, plus the whole project when there
// is a build step. That second part is not optional: the files a build reads
// (styles/, po/, src/) are by definition NOT in the install mapping, so
// watching only the mapping would ignore every edit that actually matters to
// a project that builds its assets.
func snapshot(cfg *config.Config) (string, error) {
	var roots []string
	if cfg.Project.Build != "" {
		roots = []string{cfg.Dir}
	} else {
		for _, pair := range cfg.InstallPairs() {
			roots = append(roots, filepath.Join(cfg.Dir, filepath.FromSlash(pair[0])))
		}
	}

	var parts []string
	for _, src := range roots {
		if st, err := os.Stat(src); err != nil || !st.IsDir() {
			continue
		}
		err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			parts = append(parts, fmt.Sprintf("%s:%d:%d", p, info.Size(), info.ModTime().UnixNano()))
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, "\n"), nil
}
