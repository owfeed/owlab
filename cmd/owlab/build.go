package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
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
	layout := fs.String("layout", "arch", "output layout: arch (dist/<arch>/) or flat")
	verbose := fs.Bool("v", false, "show the full build log")
	keep := fs.Bool("keep", false, "keep the SDK container's build tree for the next run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *layout != layoutArch && *layout != layoutFlat {
		return fmt.Errorf("--layout is %q; it takes %q or %q", *layout, layoutArch, layoutFlat)
	}

	// Default to whatever the project's first non-VM router targets, so
	// `owlab build` with no flags builds for what is being developed against.
	//
	// A config is not required. A package repository wiring this into CI has a
	// Makefile and a release number, and demanding an owlab.yaml that says
	// nothing the flags do not would be asking for a file to keep in step.
	var ref *config.Router
	title := "OpenWrt"
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if a.cfg != nil {
		dir = a.cfg.Dir
		for i := range a.cfg.Routers {
			if a.cfg.Routers[i].Fidelity != config.VM {
				ref = &a.cfg.Routers[i]
				break
			}
		}
		if ref == nil {
			return fmt.Errorf("no routers in %s to take a target from", config.FileName)
		}
		title = ref.Title()
	}

	rel := *release
	if rel == "" {
		if ref == nil {
			return fmt.Errorf("--release is required when there is no %s to take one from\n\n"+
				"  owlab build --release 25.12.5", config.FileName)
		}
		rel = ref.Release
	}

	var target config.Target
	switch {
	case *arch != "":
		t, err := config.LookupTarget(*arch)
		if err != nil {
			return err
		}
		target = t
	case ref != nil:
		target = ref.Target()
	default:
		// The host's own architecture: the SDK for it is the one that will not
		// run under emulation.
		t, err := config.LookupTarget("auto")
		if err != nil {
			return err
		}
		target = t
	}

	pkgDir, pkgName, pkgArch, err := findPackage(dir)
	if err != nil {
		return err
	}
	// A package that does not declare an architecture is built for the target's.
	if pkgArch == "" {
		pkgArch = target.Arch
	}
	apkDir, ipkDir := archDirs(pkgArch, *layout)

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
		outDir = filepath.Join(dir, outDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// A named volume for the SDK's build tree. staging_dir and build_dir are
	// this ecosystem's node_modules: rebuilding them on every run costs
	// minutes, and on Docker Desktop a bind mount for them would be the slow
	// path as well.
	volume := "owlab-sdk-" + strings.ReplaceAll(target.TagPrefix+"-"+rel, ".", "-")

	fmt.Printf("building %s for %s %s (%s)\n", pkgName, title, rel, target.Arch)
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
		"-e", "APK_DIR=" + apkDir,
		"-e", "IPK_DIR=" + ipkDir,
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

	built, err := builtPackages(outDir)
	if err != nil {
		return err
	}
	if len(built) == 0 {
		return fmt.Errorf("the build reported success but produced no package in %s", outDir)
	}
	fmt.Printf("\nbuilt:\n")
	for _, p := range built {
		rel, err := filepath.Rel(outDir, p)
		if err != nil {
			rel = filepath.Base(p)
		}
		if st, err := os.Stat(p); err == nil {
			fmt.Printf("  %s  (%s)\n", rel, humanBytes(st.Size()))
		}
	}
	fmt.Printf("\nInstall it with:  owlab install <router> /path/to/package\n")
	return nil
}

// builtPackages lists the artifacts under a build's output directory.
//
// It walks rather than globs because the default layout puts each artifact in a
// directory named for its architecture, and it filters by extension because the
// SDK's own leavings are not what was asked for.
func builtPackages(outDir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(outDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".apk", ".ipk":
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
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
#
# The directory is the architecture. An apk's filename does not carry one at
# all, and everything downstream reads the directory rather than the name. The
# two formats spell the architecture-independent case differently -- apk
# "noarch", opkg "all" -- so each goes where it says it belongs; the caller
# worked both names out and passed them in.
find bin -name '%[1]s*.apk' -o -name '%[1]s*.ipk' | while read -r f; do
	case "$f" in
	*.apk) d="$APK_DIR" ;;
	*)     d="$IPK_DIR" ;;
	esac
	mkdir -p "/owlab-out/$d"
	cp "$f" "/owlab-out/$d/"
	echo "owlab: $d/$(basename "$f")"
done
`, pkg, v)
}

// Output layouts. `arch` is the artifact contract every later stage reads;
// `flat` is what owlab wrote before there was a contract, kept for one release
// so a pipeline that globs `dist/*.apk` can be moved deliberately rather than
// discovering the change from a build that suddenly produces nothing.
const (
	layoutArch = "arch"
	layoutFlat = "flat"
)

// archDirs maps a declared package architecture to the directory each format
// goes in.
//
// The two package managers spell the architecture-independent case
// differently: apk requires "noarch" and rejects "all" as uninstallable —
// which is why OpenWrt's own package-pack.mk translates it — while opkg has
// only ever known "all". Each artifact goes in the directory named for what it
// says about itself, because an index built from a tree that disagrees with the
// package inside it is wrong in a way nothing downstream can detect.
func archDirs(pkgArch, layout string) (apk, ipk string) {
	if layout == layoutFlat {
		return ".", "."
	}
	if pkgArch == "all" || pkgArch == "noarch" {
		return "noarch", "all"
	}
	return pkgArch, pkgArch
}

var (
	pkgNameRE = regexp.MustCompile(`(?m)^PKG_NAME\s*:?=\s*(\S+)`)
	pkgArchRE = regexp.MustCompile(`(?m)^(?:PKG_ARCH|LUCI_PKGARCH)\s*:?=\s*(\S+)`)
)

// findPackage locates the directory holding the OpenWrt Makefile, the package
// name it declares, and the architecture it declares.
//
// The name has to come from the Makefile rather than the directory, because
// `make package/<name>/compile` uses PKG_NAME — and for LuCI packages that
// name is often set by luci.mk from the directory anyway, but not always.
//
// The architecture comes from the same place for the same reason: it is what
// the SDK builds the package's own metadata from, so reading it here and
// reading it out of the finished artifact cannot disagree. An empty result
// means the Makefile declared none, and the target's architecture applies.
func findPackage(root string) (dir, name, arch string, err error) {
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
		pkgArch := ""
		if m := pkgArchRE.FindStringSubmatch(text); m != nil {
			pkgArch = m[1]
		}
		if m := pkgNameRE.FindStringSubmatch(text); m != nil {
			return c, m[1], pkgArch, nil
		}
		// luci.mk derives PKG_NAME from the directory when the Makefile does
		// not set it, which is the common case for themes and apps.
		return c, filepath.Base(c), pkgArch, nil
	}
	return "", "", "", fmt.Errorf(
		"no OpenWrt package Makefile found in %s or its subdirectories\n\n"+
			"`owlab build` needs the Makefile that declares the package — the one including\n"+
			"$(TOPDIR)/rules.mk and luci.mk.", root)
}
