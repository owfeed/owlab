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
	dir, name, err := findPackage(root)
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
	dir, name, err := findPackage(root)
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

	dir, name, err := findPackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if name != "luci-app-real" || dir != sub {
		t.Errorf("picked %q in %q", name, dir)
	}
}

func TestFindPackageReportsWhenThereIsNone(t *testing.T) {
	_, _, err := findPackage(t.TempDir())
	if err == nil {
		t.Fatal("expected an error when no package Makefile exists")
	}
}
