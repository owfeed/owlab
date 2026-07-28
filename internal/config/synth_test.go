package config

import (
	"slices"
	"strings"
	"testing"
)

func TestSynthesize(t *testing.T) {
	cfg, err := Synthesize(SynthOptions{
		Name:     "luci-app-mine",
		Dir:      "/tmp/x",
		Releases: []string{"25.12.4", "24.10.8"},
		Arch:     "x86_64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routers) != 2 {
		t.Fatalf("got %d routers", len(cfg.Routers))
	}

	// The id becomes a container name and appears in every CI log line, so it
	// has to say which release failed.
	if cfg.Routers[0].ID != "openwrt-25.12.4" {
		t.Errorf("id = %q", cfg.Routers[0].ID)
	}
	// Derived exactly as the file path derives it: 25.12 is apk, 24.10 is opkg.
	if got := cfg.Routers[0].PackageManager(); got != APK {
		t.Errorf("25.12.4 package manager = %q", got)
	}
	if got := cfg.Routers[1].PackageManager(); got != OPKG {
		t.Errorf("24.10.8 package manager = %q", got)
	}
	// Ports must not collide, or the second router never starts.
	if cfg.Routers[0].Ports.HTTP == cfg.Routers[1].Ports.HTTP {
		t.Error("both routers were given the same http port")
	}
	if len(cfg.Routers[0].Fixtures) == 0 {
		t.Error("synthesized routers should get the default fixtures")
	}
}

// `--packages +luci-app-sqm` has to mean the same thing on the command line as
// in the file: added to what a router already gets, not a package literally
// named "+luci-app-sqm".
func TestSynthesizePackagePrefixes(t *testing.T) {
	cfg, err := Synthesize(SynthOptions{
		Releases: []string{"25.12.4"},
		Packages: []string{"+luci-app-sqm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pkgs := cfg.Routers[0].Packages
	if !slices.Contains(pkgs, "luci-app-sqm") {
		t.Errorf("packages = %v, want the stock set plus luci-app-sqm", pkgs)
	}
	if len(pkgs) <= 1 {
		t.Errorf("packages = %v, want the stock router set kept", pkgs)
	}
	for _, p := range pkgs {
		if strings.HasPrefix(p, "+") {
			t.Errorf("the + prefix survived into a package name: %q", p)
		}
	}
}

func TestSynthesizeReplacesPackagesWithoutPrefixes(t *testing.T) {
	cfg, err := Synthesize(SynthOptions{
		Releases: []string{"25.12.4"},
		Packages: []string{"luci"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Routers[0].Packages; len(got) != 1 || got[0] != "luci" {
		t.Errorf("packages = %v, want exactly [luci]", got)
	}
}

func TestSynthesizeValidates(t *testing.T) {
	if _, err := Synthesize(SynthOptions{Releases: nil}); err == nil {
		t.Error("want an error with no releases")
	}
	if _, err := Synthesize(SynthOptions{Releases: []string{"25.12.4"}, Distro: "kwrt"}); err == nil {
		t.Error("want an error for an unknown distro")
	}
	if _, err := Synthesize(SynthOptions{Releases: []string{"25.12.4"}, Arch: "sparc"}); err == nil {
		t.Error("want an error for an unknown arch")
	}
	if _, err := Synthesize(SynthOptions{Releases: []string{"25.12.4"}, Fixtures: []string{"nonsense"}}); err == nil {
		t.Error("want an error for an unknown fixture")
	}
	// The same release twice would be two containers with the same name.
	if _, err := Synthesize(SynthOptions{Releases: []string{"25.12.4", "25.12.4"}}); err == nil {
		t.Error("want an error for a duplicated release")
	}
}

func TestSynthesizeImmortalWrt(t *testing.T) {
	cfg, err := Synthesize(SynthOptions{Releases: []string{"25.12.1"}, Distro: ImmortalWrt})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routers[0].ID != "immortalwrt-25.12.1" {
		t.Errorf("id = %q", cfg.Routers[0].ID)
	}
	if !strings.Contains(cfg.Routers[0].RootfsTarballURL(), "immortalwrt.org") {
		t.Errorf("tarball url = %q", cfg.Routers[0].RootfsTarballURL())
	}
}
