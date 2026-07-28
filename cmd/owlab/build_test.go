package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMakefile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindPackageReadsPkgName(t *testing.T) {
	root := t.TempDir()
	writeMakefile(t, root, `
include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-example
PKG_VERSION:=1.0

include $(TOPDIR)/feeds/luci/luci.mk
`)
	dir, name, _, err := findPackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if name != "luci-app-example" {
		t.Errorf("name: %q", name)
	}
	if dir != root {
		t.Errorf("dir: %q", dir)
	}
}

func TestFindPackageInSubdirectory(t *testing.T) {
	// The common LuCI layout: repository root holds docs and CI, the package
	// itself is one level down.
	root := t.TempDir()
	sub := filepath.Join(root, "luci-theme-example")
	writeMakefile(t, sub, `
include $(TOPDIR)/rules.mk
LUCI_TITLE:=Example theme
include ../../luci.mk
`)
	dir, name, _, err := findPackage(root)
	if err != nil {
		t.Fatal(err)
	}
	// luci.mk derives PKG_NAME from the directory when the Makefile does not
	// set it, which is the usual case for themes.
	if name != "luci-theme-example" {
		t.Errorf("name: %q", name)
	}
	if dir != sub {
		t.Errorf("dir: %q", dir)
	}
}

func TestFindPackageIgnoresUnrelatedMakefile(t *testing.T) {
	// A plain Makefile at the root — a docs or release helper — must not be
	// mistaken for the package, or the build would target nothing.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"),
		[]byte("all:\n\techo hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "luci-app-real")
	writeMakefile(t, sub, "include $(TOPDIR)/rules.mk\nPKG_NAME:=luci-app-real\ninclude ../../luci.mk\n")

	dir, name, _, err := findPackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if name != "luci-app-real" || dir != sub {
		t.Errorf("picked %q in %q", name, dir)
	}
}

func TestFindPackageReportsWhenThereIsNone(t *testing.T) {
	_, _, _, err := findPackage(t.TempDir())
	if err == nil {
		t.Fatal("expected an error when no package Makefile exists")
	}
}

func TestFindPackageReadsArchitecture(t *testing.T) {
	// Themes and apps declare it through luci.mk's variable, compiled packages
	// through the buildroot one, and most declare nothing at all — in which case
	// the target's architecture applies and an empty result says so.
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"luci", "include $(TOPDIR)/rules.mk\nPKG_NAME:=x\nLUCI_PKGARCH:=all\ninclude ../../luci.mk\n", "all"},
		{"buildroot", "include $(TOPDIR)/rules.mk\nPKG_NAME:=x\nPKG_ARCH:=all\ninclude ../../package.mk\n", "all"},
		{"undeclared", "include $(TOPDIR)/rules.mk\nPKG_NAME:=x\ninclude ../../package.mk\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeMakefile(t, root, tc.body)
			_, _, arch, err := findPackage(root)
			if err != nil {
				t.Fatal(err)
			}
			if arch != tc.want {
				t.Errorf("arch: %q, want %q", arch, tc.want)
			}
		})
	}
}

func TestArchDirs(t *testing.T) {
	// The architecture-independent case is the one that matters: apk rejects
	// "all" as uninstallable and opkg has never heard of "noarch", so one build
	// produces two artifacts that belong in differently named directories.
	for _, tc := range []struct {
		arch, layout   string
		wantAPK, wantI string
	}{
		{"all", layoutArch, "noarch", "all"},
		{"noarch", layoutArch, "noarch", "all"},
		{"x86_64", layoutArch, "x86_64", "x86_64"},
		{"aarch64_cortex-a53", layoutArch, "aarch64_cortex-a53", "aarch64_cortex-a53"},
		{"all", layoutFlat, ".", "."},
		{"x86_64", layoutFlat, ".", "."},
	} {
		apk, ipk := archDirs(tc.arch, tc.layout)
		if apk != tc.wantAPK || ipk != tc.wantI {
			t.Errorf("archDirs(%q, %q) = %q, %q; want %q, %q",
				tc.arch, tc.layout, apk, ipk, tc.wantAPK, tc.wantI)
		}
	}
}

func TestBuiltPackagesWalksArchitectureDirectories(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "noarch", "luci-theme-example-1.0.apk"))
	writeFile(t, filepath.Join(out, "all", "luci-theme-example_1.0_all.ipk"))
	// The SDK leaves things behind that were not asked for.
	writeFile(t, filepath.Join(out, "noarch", "packages.adb"))

	built, err := builtPackages(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(built) != 2 {
		t.Fatalf("found %d packages, want 2: %v", len(built), built)
	}
	for _, p := range built {
		if ext := filepath.Ext(p); ext != ".apk" && ext != ".ipk" {
			t.Errorf("picked up %q", p)
		}
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
