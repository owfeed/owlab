package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/VizzleTF/owlab/internal/compose"
	"github.com/VizzleTF/owlab/internal/config"
	"github.com/VizzleTF/owlab/internal/upstream"
)

// buildContext prepares a docker build context for one router and prints the
// arguments needed to build it.
//
// This exists for CI. Everyday use goes through `owlab up`, which drives
// compose; publishing needs the same context and the same build args, but fed
// to `docker buildx build` directly so the workflow controls tagging,
// caching, provenance and the registry push.
//
// Keeping it as a command rather than duplicating the logic in YAML is what
// stops the published images from drifting away from the ones developers get
// locally: both go through exactly this context, with exactly these args.
func (a *app) buildContext(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	list := fs.Bool("list", false, "print the routers as a JSON matrix for CI, and exit")
	out := fs.String("out", ".owlab/publish", "directory to write the context into")
	router := fs.String("router", "", "which router to prepare (required unless --list)")
	release := fs.String("release", "", "build this release instead of the one pinned in the config")
	keep := fs.Int("keep", 1, "with --list, how many point releases per branch to emit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *list {
		return a.printMatrix(ctx, *keep)
	}
	if *router == "" {
		return fmt.Errorf("--router is required (or --list to see them)\n\nhave: %s",
			strings.Join(a.cfg.RouterIDs(), ", "))
	}
	r, ok := a.cfg.Router(*router)
	if !ok {
		return fmt.Errorf("no router %q (have: %s)", *router, strings.Join(a.cfg.RouterIDs(), ", "))
	}
	// The matrix may name a release the config does not pin — that is the
	// whole point of resolving it against the download server — so the release
	// travels with the matrix entry and is applied here.
	if *release != "" {
		r.Release = *release
	}

	resolveBaseImages(ctx, []*config.Router{r})

	// Prepare writes the shared context, the per-router extra packages and
	// the compliance bundle.
	proj, err := compose.Prepare(a.cfg, a.eng)
	if err != nil {
		return err
	}

	dir := *out
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(a.cfg.Dir, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	buildArgs := compose.BuildArgsFor(a.cfg, r)
	var lines []string
	for _, k := range sortedKeys(buildArgs) {
		lines = append(lines, fmt.Sprintf("--build-arg\n%s=%s", k, buildArgs[k]))
	}
	// One argument per line, so the workflow can feed it straight to xargs
	// without worrying about quoting values that contain spaces (PACKAGES
	// always does).
	argsFile := filepath.Join(dir, "buildargs")
	if err := os.WriteFile(argsFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	platformFile := filepath.Join(dir, "platform")
	if err := os.WriteFile(platformFile, []byte(r.Platform()+"\n"), 0o644); err != nil {
		return err
	}

	fmt.Printf("context=%s\n", proj.ContextDir)
	fmt.Printf("buildargs=%s\n", argsFile)
	fmt.Printf("platform=%s\n", r.Platform())
	fmt.Printf("tag=%s\n", imageTag(r))
	return nil
}

// matrixEntry is one row of the CI build matrix.
type matrixEntry struct {
	ID       string `json:"id"`
	Distro   string `json:"distro"`
	Release  string `json:"release"`
	Arch     string `json:"arch"`
	Tag      string `json:"tag"`
	Platform string `json:"platform"`
	// Runner is the GitHub runner label that builds this natively. Building
	// an arm64 rootfs on an amd64 runner works only under emulation, and
	// every package install in the image would pay for it.
	Runner string `json:"runner"`
}

// printMatrix emits one row per image to build.
//
// With keep=1 that is one row per router, at the release the config pins. With
// more, each router's pin is replaced by the newest N point releases of its
// own branch, resolved against the download server — so the published set
// tracks upstream without anyone editing a version number, and the previous
// release stays available for whoever has not moved yet.
func (a *app) printMatrix(ctx context.Context, keep int) error {
	var entries []matrixEntry
	for i := range a.cfg.Routers {
		r := &a.cfg.Routers[i]
		if r.Fidelity == config.VM {
			continue
		}
		for _, rel := range a.releasesFor(ctx, r, keep) {
			build := *r
			build.Release = rel
			entries = append(entries, matrixEntry{
				ID:       build.ID,
				Distro:   string(build.Distro),
				Release:  rel,
				Arch:     build.Arch,
				Tag:      imageTag(&build),
				Platform: build.Platform(),
				Runner:   runnerFor(&build),
			})
		}
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(map[string]any{"include": entries})
}

// releasesFor is the list of point releases to build for one router.
//
// The pinned release is always included, even when it has fallen off the end
// of the list: someone may be pinning it in their own owlab.yaml, and the
// weekly rebuild is what keeps that image current with its feed.
//
// Falls back to the pin alone whenever the server cannot be read. A publish
// job that fails because a listing timed out would be a worse outcome than one
// that publishes what the config already says.
func (a *app) releasesFor(ctx context.Context, r *config.Router, keep int) []string {
	if keep <= 1 {
		return []string{r.Release}
	}
	all, err := upstream.Releases(ctx, r.Distro)
	if err != nil {
		fmt.Fprintf(os.Stderr, "owlab: %s: cannot list releases (%v); building only the pinned %s\n",
			r.Distro, err, r.Release)
		return []string{r.Release}
	}

	out := []string{}
	seen := map[string]bool{}
	for _, rel := range upstream.Newest(all, upstream.Branch(r.Release), keep) {
		// A release is in the listing as soon as it is tagged; a given
		// target's images land when that target's build finishes. Taking the
		// listing at its word produces a job that 404s twenty minutes in.
		if !upstream.Published(ctx, r, rel) {
			fmt.Fprintf(os.Stderr, "owlab: %s %s has no artifacts for %s yet, skipping\n",
				r.Distro, rel, r.Arch)
			continue
		}
		out = append(out, rel)
		seen[rel] = true
	}
	if !seen[r.Release] {
		out = append(out, r.Release)
	}
	return out
}

// imageTag is the published tag for a router: distro, exact release, and
// OpenWrt architecture.
//
// All three belong in the tag. An OCI image index has no field for "distro
// version", and most OpenWrt architecture names have no valid GOARCH mapping
// — so a manifest list cannot express this matrix, and upstream does not try
// either. One tag is one target.
func imageTag(r *config.Router) string {
	return fmt.Sprintf("%s-%s-%s", r.Distro, r.Release, r.Arch)
}

func runnerFor(r *config.Router) string {
	if r.Platform() == "linux/arm64" || strings.Contains(r.Arch, "aarch64") {
		return "ubuntu-24.04-arm"
	}
	return "ubuntu-24.04"
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
