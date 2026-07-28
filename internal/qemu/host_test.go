package qemu

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/VizzleTF/owlab/internal/config"
)

// foreignTarget is a VM-capable target whose CPU is not this host's, whatever
// this host is. Used for the rule that matters most here: no accelerator
// virtualises a foreign architecture.
func foreignTarget(t *testing.T) config.Target {
	t.Helper()
	for _, arch := range config.VMArches() {
		tgt, err := config.LookupTarget(arch)
		if err != nil {
			t.Fatal(err)
		}
		if tgt.GOARCH != runtime.GOARCH {
			return tgt
		}
	}
	t.Skip("every VM-capable target matches this host's arch")
	return config.Target{}
}

// The first question is not which accelerator the host has but whether the
// guest CPU is the host CPU. Getting this wrong makes an x86 router on an ARM
// laptop look like "QEMU is slow" rather than "you asked for the wrong arch",
// and the answer must not depend on what is installed.
func TestForeignGuestArchIsAlwaysTranslated(t *testing.T) {
	tgt := foreignTarget(t)

	// A binary that does not exist: if the arch check did not come first, this
	// would have to probe it.
	a := ChooseAccel("/nonexistent/qemu-system-nothing", tgt)
	if a.Native {
		t.Errorf("claimed native acceleration for a %s guest on a %s host", tgt.GOARCH, runtime.GOARCH)
	}
	if a.Name != "tcg" {
		t.Errorf("Name = %q, want tcg", a.Name)
	}
	if !strings.Contains(a.Reason, tgt.Arch) || !strings.Contains(a.Reason, runtime.GOARCH) {
		t.Errorf("the reason should name both architectures, got %q", a.Reason)
	}
}

// Probed, never assumed. Homebrew's qemu-system-x86_64 on Apple Silicon reports
// tcg only, so a build that answers nothing must never be treated as native.
func TestUnprobableBinaryIsNeverNative(t *testing.T) {
	host, err := config.LookupTarget(config.HostArch())
	if err != nil {
		t.Fatal(err)
	}
	a := ChooseAccel("/nonexistent/qemu-system-nothing", host)
	if a.Native {
		t.Error("claimed native acceleration from a binary that could not be probed")
	}
	if a.Reason == "" {
		t.Error("a non-native choice must say why; a silent fallback is the bug this exists to prevent")
	}
}

// "host" is only meaningful when the guest CPU is the host CPU. Under
// translation it asks QEMU to emulate whatever this machine happens to be,
// which is slower and less reproducible than naming a model.
func TestCPUModel(t *testing.T) {
	tgt, err := config.LookupTarget("x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if got := CPUModel(tgt, Accel{Name: "hvf", Native: true}); got != "host" {
		t.Errorf("native CPUModel = %q, want host", got)
	}
	if got := CPUModel(tgt, Accel{Name: "tcg"}); got != tgt.QEMUCPUEmulated {
		t.Errorf("emulated CPUModel = %q, want %q", got, tgt.QEMUCPUEmulated)
	}
}

func TestFindQEMURejectsTargetsWithNoEmulator(t *testing.T) {
	// mvebu publishes a rootfs but no bootable combined image, so the VM tier
	// does not support it and there is no binary to look for.
	tgt, err := config.LookupTarget("arm_cortex-a9_vfpv3-d16")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FindQEMU(tgt); err == nil {
		t.Fatal("expected an error for a target the VM tier does not support")
	}
}

func TestFindQEMUHonoursTheOverride(t *testing.T) {
	tgt, err := config.LookupTarget("x86_64")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OWLAB_QEMU", "/opt/custom/qemu-system-x86_64")
	got, err := FindQEMU(tgt)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/opt/custom/qemu-system-x86_64" {
		t.Errorf("FindQEMU = %q, want the override", got)
	}
}

// x86 still publishes a legacy-boot combined image, which SeaBIOS handles with
// nothing to find.
func TestFirmwareIsNotNeededForX86(t *testing.T) {
	t.Setenv("OWLAB_FIRMWARE", "")
	tgt, err := config.LookupTarget("x86_64")
	if err != nil {
		t.Fatal(err)
	}
	fw, err := FindFirmware("/usr/bin/qemu-system-x86_64", tgt)
	if err != nil {
		t.Fatal(err)
	}
	if fw != "" {
		t.Errorf("x86 needs no firmware file, got %q", fw)
	}
}

// QEMU keeps its firmware beside itself, so the binary found on PATH is the
// most reliable place to start: right for Homebrew, a manual build and a
// distribution package alike.
func TestFirmwareIsFoundBesideTheBinary(t *testing.T) {
	t.Setenv("OWLAB_FIRMWARE", "")
	tgt, err := config.LookupTarget("aarch64_generic")
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	bin := filepath.Join(root, "bin", "qemu-system-aarch64")
	share := filepath.Join(root, "share", "qemu")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(share, 0o755); err != nil {
		t.Fatal(err)
	}
	// A distribution alias, not the canonical name: Debian and Fedora each
	// rename this file and neither name matches the other.
	want := filepath.Join(share, "AAVMF_CODE.fd")
	if err := os.WriteFile(want, []byte("firmware"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindFirmware(bin, tgt)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("FindFirmware = %q, want %q", got, want)
	}
}

// An override that points at nothing has to say so rather than silently fall
// back to a search, or the developer never learns their path was wrong.
func TestFirmwareOverrideMustExist(t *testing.T) {
	tgt, err := config.LookupTarget("aarch64_generic")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OWLAB_FIRMWARE", filepath.Join(t.TempDir(), "not-there.fd"))
	if _, err := FindFirmware("/usr/bin/qemu-system-aarch64", tgt); err == nil {
		t.Fatal("expected an error for an OWLAB_FIRMWARE that does not exist")
	}
}

func TestCacheDirHonoursTheOverride(t *testing.T) {
	t.Setenv("OWLAB_CACHE", "/tmp/owlab-cache-test")
	if got := CacheDir(); got != "/tmp/owlab-cache-test" {
		t.Errorf("CacheDir = %q, want the override", got)
	}
}
