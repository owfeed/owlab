package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/VizzleTF/owlab/internal/config"
)

// build compiles the project into a real .apk or .ipk using OpenWrt's SDK.
//
// This is the verification tier, not the development loop. `owlab sync`
// copies sources in and is what you use while working; this produces the
// artifact a user would actually install, and the two can differ. The
// difference that bites is minification: luci.mk sets LUCI_MINIFY_JS and
// LUCI_MINIFY_CSS by default, so a real build runs the sources through jsmin
// and csstidy while a sync does not. Code that works unminified and breaks
// minified is invisible until someone builds a package.
func (a *app) build(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	release := fs.String("release", "", "OpenWrt release to build against (default: the first router's)")
	arch := fs.String("arch", "", "target architecture (default: the first router's)")
	out := fs.String("out", "dist", "directory to write the built packages into")
	verbose := fs.Bool("v", false, "show the full build log")
	keep := fs.Bool("keep", false, "keep the SDK container's build tree for the next run")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Default to whatever the project's first non-VM router targets, so
	// `owlab build` with no flags builds for what is being developed against.
	var ref *config.Router
	for i := range a.cfg.Routers {
		if a.cfg.Routers[i].Fidelity != config.VM {
			ref = &a.cfg.Routers[i]
			break
		}
	}
	if ref == nil {
		return fmt.Errorf("no routers in %s to take a target from", config.FileName)
	}
	rel := *release
	if rel == "" {
		rel = ref.Release
	}
	target := ref.Target()
	if *arch != "" {
		t, err := config.LookupTarget(*arch)
		if err != nil {
			return err
		}
		target = t
	}

	pkgDir, pkgName, err := findPackage(a.cfg.Dir)
	if err != nil {
		return err
	}

	sdk := fmt.Sprintf("openwrt/sdk:%s-%s", target.TagPrefix, rel)

	// Every openwrt/sdk tag is linux/amd64, including the ones targeting
	// aarch64 — the SDK is a cross-compiler, and upstream publishes only
	// Linux-x86_64 SDK tarballs. On an arm64 host that means emulation, and
	// an OpenWrt package build is long enough that the difference is worth
	// saying out loud rather than leaving someone to wonder.
	if runtime.GOARCH != "amd64" {
		fmt.Fprintf(os.Stderr,
			"owlab: the OpenWrt SDK is published for linux/amd64 only, so this build runs\n"+
				"owlab: under emulation on %s and will be slow. This is a verification step,\n"+
				"owlab: not the development loop — `owlab sync` is what you want while working.\n\n",
			runtime.GOARCH)
	}

	outDir := *out
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(a.cfg.Dir, outDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// A named volume for the SDK's build tree. staging_dir and build_dir are
	// this ecosystem's node_modules: rebuilding them on every run costs
	// minutes, and on Docker Desktop a bind mount for them would be the slow
	// path as well.
	volume := "owlab-sdk-" + strings.ReplaceAll(target.TagPrefix+"-"+rel, ".", "-")

	fmt.Printf("building %s for %s %s (%s)\n", pkgName, ref.Title(), rel, target.Arch)
	fmt.Printf("  sdk    %s\n", sdk)
	fmt.Printf("  source %s\n", pkgDir)
	fmt.Printf("  out    %s\n\n", outDir)

	script := sdkScript(pkgName, *verbose)
	runArgs := []string{
		"run", "--rm",
		// Explicit, because every openwrt/sdk tag is amd64 and on an arm64
		// host the pull would otherwise fail to resolve.
		"--platform", "linux/amd64",
		"-v", pkgDir + ":/owlab-feed/" + pkgName + ":ro",
		"-v", outDir + ":/owlab-out",
		"-e", "PKG=" + pkgName,
	}
	if *keep {
		// A named volume for the SDK's build tree, so a second build reuses
		// it. Off by default: a stale build_dir is a confusing failure mode,
		// and the first thing to try when a build misbehaves is a clean one.
		runArgs = append(runArgs, "-v", volume+":/builder/build_dir")
	}
	runArgs = append(runArgs, sdk, "/bin/bash", "-c", script)

	if err := a.docker.Run(ctx, runArgs...); err != nil {
		return fmt.Errorf("SDK build failed: %w", err)
	}

	built, _ := filepath.Glob(filepath.Join(outDir, "*"))
	if len(built) == 0 {
		return fmt.Errorf("the build reported success but produced no package in %s", outDir)
	}
	fmt.Printf("\nbuilt:\n")
	for _, p := range built {
		if st, err := os.Stat(p); err == nil {
			fmt.Printf("  %s  (%s)\n", filepath.Base(p), humanBytes(st.Size()))
		}
	}
	fmt.Printf("\nInstall it with:  owlab install <router> /path/to/package\n")
	return nil
}

// sdkScript is what runs inside the SDK container.
func sdkScript(pkg string, verbose bool) string {
	v := ""
	if verbose {
		v = " V=s"
	}
	// src-link goes ABOVE the other feeds, because feeds are searched in
	// order and a package with the same name in the official feed would
	// otherwise win — which is exactly the case when someone is developing a
	// newer version of something already packaged.
	return fmt.Sprintf(`set -eu
cd /builder

# Release SDKs ship ready to use; snapshot ones ship a setup.sh that fetches
# the real thing.
[ -d ./scripts ] || ./setup.sh

printf 'src-link owlab /owlab-feed\n' > feeds.conf
cat feeds.conf.default >> feeds.conf

./scripts/feeds update -a >/dev/null
./scripts/feeds install -a -p owlab >/dev/null
./scripts/feeds install %[1]s >/dev/null 2>&1 || true

make defconfig >/dev/null
make package/%[1]s/clean >/dev/null 2>&1 || true
make package/%[1]s/compile%[2]s

# Both eras, and only the package we asked for: the SDK also rebuilds
# dependencies and we do not want to hand those back as if they were ours.
find bin -name '%[1]s*.apk' -o -name '%[1]s*.ipk' | while read -r f; do
	cp "$f" /owlab-out/
	echo "owlab: $(basename "$f")"
done
`, pkg, v)
}

var pkgNameRE = regexp.MustCompile(`(?m)^PKG_NAME\s*:?=\s*(\S+)`)

// findPackage locates the directory holding the OpenWrt Makefile and the
// package name it declares.
//
// The name has to come from the Makefile rather than the directory, because
// `make package/<name>/compile` uses PKG_NAME — and for LuCI packages that
// name is often set by luci.mk from the directory anyway, but not always.
func findPackage(root string) (dir, name string, err error) {
	candidates := []string{root}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			candidates = append(candidates, filepath.Join(root, e.Name()))
		}
	}

	for _, c := range candidates {
		mk := filepath.Join(c, "Makefile")
		body, err := os.ReadFile(mk)
		if err != nil {
			continue
		}
		text := string(body)
		// An OpenWrt package Makefile always includes one of these.
		if !strings.Contains(text, "include $(TOPDIR)/rules.mk") &&
			!strings.Contains(text, "luci.mk") &&
			!strings.Contains(text, "package.mk") {
			continue
		}
		if m := pkgNameRE.FindStringSubmatch(text); m != nil {
			return c, m[1], nil
		}
		// luci.mk derives PKG_NAME from the directory when the Makefile does
		// not set it, which is the common case for themes and apps.
		return c, filepath.Base(c), nil
	}
	return "", "", fmt.Errorf(
		"no OpenWrt package Makefile found in %s or its subdirectories\n\n"+
			"`owlab build` needs the Makefile that declares the package — the one including\n"+
			"$(TOPDIR)/rules.mk and luci.mk.", root)
}
