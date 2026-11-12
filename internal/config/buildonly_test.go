package config

import (
	"strings"
	"testing"
)

// A package architecture with an SDK and no rootfs — every real board — is a
// valid router for `owlab build` to take its target from. Issue: owlab 0.6.5
// refused to load such a config at all, so even `owlab build --arch` failed.
func TestBuildOnlyArchLoads(t *testing.T) {
	cfg, err := Load(write(t, `
version: 1
routers:
  - id: owrt2410-a53
    release: "24.10.8"
    arch: aarch64_cortex-a53
`))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := cfg.Router("owrt2410-a53")
	tgt := r.Target()
	if tgt.CanRun() {
		t.Error("aarch64_cortex-a53 has no rootfs; CanRun should be false")
	}
	if tgt.Arch != "aarch64_cortex-a53" || tgt.TagPrefix != "aarch64_cortex-a53" {
		t.Errorf("target = %+v, want arch and tag prefix both aarch64_cortex-a53", tgt)
	}
}

func TestBuildOnlyRouterRefusesToRun(t *testing.T) {
	cfg, err := Load(write(t, `
version: 1
routers:
  - id: owrt2410-a53
    release: "24.10.8"
    arch: aarch64_cortex-a53
`))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := cfg.Router("owrt2410-a53")
	err = r.Runnable()
	if err == nil {
		t.Fatal("want Runnable to refuse a build-only arch")
	}
	// The message is the whole feature: it has to name the router, the arch,
	// and what to do instead.
	for _, want := range []string{"owrt2410-a53", "aarch64_cortex-a53", "owlab build", "aarch64_generic"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	if _, err := cfg.Select([]string{"owrt2410-a53"}); err == nil {
		t.Error("naming a build-only router should fail")
	}
	// Nothing runnable at all: `owlab up` must say why rather than do nothing.
	if _, err := cfg.Select(nil); err == nil || !strings.Contains(err.Error(), "can be built") {
		t.Errorf("Select(nil) with only build-only routers = %v, want the build-only error", err)
	}
}

// A project keeping a build target next to its real routers must still `up`.
func TestSelectAllSkipsBuildOnlyRouters(t *testing.T) {
	cfg, err := Load(write(t, `
version: 1
defaults:
  release: "24.10.8"
routers:
  - id: filogic
    arch: aarch64_cortex-a53
  - id: armsr
    arch: aarch64_generic
`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Select(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "armsr" {
		t.Errorf("Select(nil) = %v, want only armsr", routerIDsOf(got))
	}
}

func TestBuildOnlyArchWithFidelityVMIsRejected(t *testing.T) {
	_, err := Load(write(t, `
version: 1
routers:
  - id: a53
    release: "24.10.8"
    arch: aarch64_cortex-a53
    fidelity: vm
`))
	if err == nil || !strings.Contains(err.Error(), "can only be built") {
		t.Fatalf("err = %v, want fidelity vm refused for a build-only arch", err)
	}
}

func TestResolveTarget(t *testing.T) {
	for _, arch := range []string{"aarch64_cortex-a53", "mipsel_24kc", "arm_cortex-a7_neon-vfpv4", "riscv64_riscv64"} {
		tgt, err := ResolveTarget(arch)
		if err != nil {
			t.Errorf("%s: %v", arch, err)
			continue
		}
		if tgt.CanRun() {
			t.Errorf("%s: CanRun = true", arch)
		}
	}
	// A runnable arch resolves to its full entry, not the build-only shape.
	if tgt, err := ResolveTarget("aarch64_generic"); err != nil || !tgt.CanRun() || tgt.TagPrefix != "armsr-armv8" {
		t.Errorf("aarch64_generic = %+v, %v", tgt, err)
	}
	// Typos and non-OpenWrt names are caught here, not as a missing image.
	for _, bad := range []string{"sparc", "aarch64-cortex-a53", "cortex-a53", "aarch64_", "Aarch64_cortex-a53"} {
		if _, err := ResolveTarget(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func routerIDsOf(rs []*Router) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}
