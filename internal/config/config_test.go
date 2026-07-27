package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsMergeAndPackageArithmetic(t *testing.T) {
	p := write(t, `
version: 1
defaults:
  release: "25.12.4"
  packages: [luci, luci-app-firewall, luci-app-opkg]
routers:
  - id: a
  - id: b
    packages: ["+luci-app-sqm", "-luci-app-opkg"]
  - id: c
    packages: [luci-light]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}

	// Inherits the defaults untouched.
	a, _ := cfg.Router("a")
	if got := strings.Join(a.Packages, " "); got != "luci luci-app-firewall luci-app-opkg" {
		t.Errorf("a: got %q", got)
	}

	// + and - are applied on top of the defaults.
	b, _ := cfg.Router("b")
	if got := strings.Join(b.Packages, " "); got != "luci luci-app-firewall luci-app-sqm" {
		t.Errorf("b: got %q", got)
	}

	// A list with no prefixes replaces the defaults outright, which is what
	// someone writing an explicit list means.
	c, _ := cfg.Router("c")
	if got := strings.Join(c.Packages, " "); got != "luci-light" {
		t.Errorf("c: got %q", got)
	}

	// Defaults must not be mutated by any of the above.
	if got := strings.Join(a.Packages, " "); got != "luci luci-app-firewall luci-app-opkg" {
		t.Errorf("a was mutated by b/c merging: %q", got)
	}
}

func TestPortsAutoAssignedAndUnique(t *testing.T) {
	p := write(t, `
version: 1
defaults: { release: "25.12.4" }
routers:
  - id: a
  - id: b
  - id: c
    ports: { http: 9999, ssh: 9998 }
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Router("a")
	b, _ := cfg.Router("b")
	c, _ := cfg.Router("c")
	if a.Ports.HTTP == b.Ports.HTTP || a.Ports.SSH == b.Ports.SSH {
		t.Errorf("auto-assigned ports collided: %+v %+v", a.Ports, b.Ports)
	}
	if c.Ports.HTTP != 9999 || c.Ports.SSH != 9998 {
		t.Errorf("explicit ports not honoured: %+v", c.Ports)
	}
}

func TestPortCollisionIsRejected(t *testing.T) {
	p := write(t, `
version: 1
defaults: { release: "25.12.4" }
routers:
  - id: a
    ports: { http: 8080, ssh: 2222 }
  - id: b
    ports: { http: 8080, ssh: 2223 }
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected a port collision to be rejected")
	}
	if !strings.Contains(err.Error(), "8080") {
		t.Errorf("error should name the port: %v", err)
	}
}

func TestPackageManagerByRelease(t *testing.T) {
	cases := map[string]PackageManager{
		"25.12.4":  APK,
		"25.12.0":  APK,
		"26.03.1":  APK,
		"snapshot": APK,
		"master":   APK,
		"24.10.8":  OPKG,
		"23.05.6":  OPKG,
		"21.02.7":  OPKG,
	}
	for release, want := range cases {
		if got := PackageManagerFor(release); got != want {
			t.Errorf("%s: got %s, want %s", release, got, want)
		}
	}
}

func TestPlatformIsExplicitPerTarget(t *testing.T) {
	p := write(t, `
version: 1
defaults: { release: "25.12.4" }
routers:
  - id: arm
    arch: aarch64_generic
  - id: x86
    arch: x86_64
  - id: imm
    arch: aarch64_generic
    distro: immortalwrt
    release: "25.12.1"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}

	// Only x86_64 normalises to a standard OCI platform; everything else
	// must repeat OpenWrt's own non-standard architecture string or the pull
	// will not resolve.
	arm, _ := cfg.Router("arm")
	if arm.Platform() != "linux/aarch64_generic" {
		t.Errorf("arm platform: %q", arm.Platform())
	}
	x86, _ := cfg.Router("x86")
	if x86.Platform() != "linux/amd64" {
		t.Errorf("x86 platform: %q", x86.Platform())
	}

	// ImmortalWrt is built from a tarball, where we own the metadata and
	// therefore state the honest platform.
	imm, _ := cfg.Router("imm")
	if !imm.FromTarball() {
		t.Error("immortalwrt should be built from a tarball, not an upstream image")
	}
	if imm.Platform() != "linux/arm64" {
		t.Errorf("immortalwrt platform: %q", imm.Platform())
	}
	if !strings.Contains(imm.RootfsTarballURL(), "downloads.immortalwrt.org") ||
		!strings.Contains(imm.RootfsTarballURL(), "immortalwrt-25.12.1-armsr-armv8-rootfs.tar.gz") {
		t.Errorf("immortalwrt tarball url: %q", imm.RootfsTarballURL())
	}
}

func TestFeedPinsToExactPointRelease(t *testing.T) {
	p := write(t, `
version: 1
routers:
  - id: a
    release: "25.12.1"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Router("a")
	// Mixing a 25.12.1 rootfs with a 25.12.5 feed makes every install fail
	// with "breaks: world[...]", so the feed base must name the exact point
	// release and nothing looser.
	if got := a.FeedBase(); got != "https://downloads.openwrt.org/releases/25.12.1" {
		t.Errorf("feed base: %q", got)
	}
	if got := a.BaseImage(); got != "openwrt/rootfs:aarch64_generic-25.12.1" && got != "openwrt/rootfs:x86_64-25.12.1" {
		t.Errorf("base image should pin the point release, got %q", got)
	}
}

func TestUnknownFieldIsAnError(t *testing.T) {
	// A typo in a key must not be silently ignored — that is how a developer
	// ends up debugging a router that quietly used the defaults.
	p := write(t, `
version: 1
routers:
  - id: a
    release: "25.12.4"
    packagess: [luci]
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected an unknown field to be rejected")
	}
}

func TestMissingReleaseIsAnError(t *testing.T) {
	p := write(t, `
version: 1
routers:
  - id: a
`)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "release") {
		t.Fatalf("expected a missing release to be reported, got %v", err)
	}
}

func TestFindWalksUp(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, FileName)
	if err := os.WriteFile(want, []byte("version: 1\nrouters: [{id: a, release: \"25.12.4\"}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Find(deep)
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir may hand back a symlinked path on macOS; compare the bases
	// and the fact that it resolved upward.
	if filepath.Base(got) != FileName {
		t.Errorf("found %q", got)
	}
	if _, err := Load(got); err != nil {
		t.Errorf("found file does not load: %v", err)
	}
}
