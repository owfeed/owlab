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
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *list {
		return a.printMatrix()
	}
	if *router == "" {
		return fmt.Errorf("--router is required (or --list to see them)\n\nhave: %s",
			strings.Join(a.cfg.RouterIDs(), ", "))
	}
	r, ok := a.cfg.Router(*router)
	if !ok {
		return fmt.Errorf("no router %q (have: %s)", *router, strings.Join(a.cfg.RouterIDs(), ", "))
	}

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

func (a *app) printMatrix() error {
	var entries []matrixEntry
	for i := range a.cfg.Routers {
		r := &a.cfg.Routers[i]
		if r.Fidelity == config.VM {
			continue
		}
		entries = append(entries, matrixEntry{
			ID:       r.ID,
			Distro:   string(r.Distro),
			Release:  r.Release,
			Arch:     r.Arch,
			Tag:      imageTag(r),
			Platform: r.Platform(),
			Runner:   runnerFor(r),
		})
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(map[string]any{"include": entries})
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
