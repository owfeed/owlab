// Package qemu runs the VM tier: a real OpenWrt kernel, booted natively on
// the developer's machine.
//
// Natively is the whole point. The obvious alternative — QEMU inside a
// container — needs /dev/kvm passed into that container, and Docker Desktop
// grants it on neither macOS nor Windows, which are exactly the hosts that
// need this tier most. Spawning QEMU as a host process instead turns the
// problem inside out: on Apple Silicon the guest is aarch64, the accelerator
// is hvf, and a router boots in under ten seconds with no nested
// virtualisation, no minimum macOS version and no special hardware.
package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"owfeed.org/owlab/internal/config"
)

// Accel is the chosen accelerator and why.
type Accel struct {
	// Name is what goes after -accel.
	Name string
	// Native is false when every guest instruction is being translated.
	Native bool
	// Reason explains a non-native choice, for a warning the developer sees
	// once rather than a mystery they measure later.
	Reason string
}

// Available lists the accelerators a qemu binary was built with.
//
// Probed, never assumed. Homebrew's qemu-system-x86_64 on Apple Silicon
// reports tcg only — it is built without hvf, because hvf cannot run an x86
// guest on an ARM CPU — while qemu-system-aarch64 from the same bottle has
// hvf. Guessing from the OS would get that backwards.
func Available(qemuBin string) map[string]bool {
	out, err := exec.Command(qemuBin, "-accel", "help").CombinedOutput()
	if err != nil {
		return nil
	}
	got := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.TrimSpace(line)
		if f == "" || strings.HasSuffix(f, ":") {
			continue
		}
		got[f] = true
	}
	return got
}

// ChooseAccel picks the fastest accelerator that can run this target here.
//
// The first question is not which accelerator the host has but whether the
// guest CPU is the host CPU. No accelerator virtualises a foreign
// architecture: asking for an x86 router on an ARM laptop means translation
// whatever else is installed, and measured against a native boot that is
// roughly an order of magnitude slower. Saying so is the entire value of this
// function — a silent fallback to TCG looks like "QEMU is slow" rather than
// "you asked for the wrong arch".
func ChooseAccel(qemuBin string, t config.Target) Accel {
	// An escape hatch, and the one case where owlab does not get to decide.
	//
	// Hardware acceleration is where a VM meets the host's own firmware,
	// microcode and hypervisor, and those fail in ways no amount of probing
	// predicts — WHPX in particular is reported to hang some machines on an
	// SMP boot while working on the next one over. `OWLAB_ACCEL=tcg` turns a
	// router that will not boot into a slow router that does, without waiting
	// for owlab to learn about that machine.
	if name := os.Getenv("OWLAB_ACCEL"); name != "" {
		if name == "tcg" {
			return Accel{Name: "tcg", Reason: "OWLAB_ACCEL=tcg asked for translation"}
		}
		return Accel{Name: name, Native: true}
	}
	if t.GOARCH != runtime.GOARCH {
		return Accel{
			Name:   "tcg",
			Native: false,
			Reason: fmt.Sprintf("guest is %s and this host is %s, so every instruction is translated", t.Arch, runtime.GOARCH),
		}
	}

	have := Available(qemuBin)

	switch runtime.GOOS {
	case "darwin":
		if have["hvf"] {
			return Accel{Name: "hvf", Native: true}
		}
		return Accel{Name: "tcg", Reason: "this qemu build has no hvf"}
	case "linux":
		if !have["kvm"] {
			return Accel{Name: "tcg", Reason: "this qemu build has no kvm"}
		}
		// Built with kvm is not the same as allowed to use it: /dev/kvm is
		// root:kvm and a user who is not in that group gets a permission
		// error from QEMU well after the point where a clear message helps.
		f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if err != nil {
			return Accel{Name: "tcg", Reason: "/dev/kvm is not usable (" + err.Error() + ") — adding yourself to the kvm group usually fixes it"}
		}
		f.Close()
		return Accel{Name: "kvm", Native: true}
	case "windows":
		if have["whpx"] {
			return Accel{Name: "whpx", Native: true}
		}
		// Naming the command matters more here than anywhere else: the
		// feature is off by default, it is not called anything a developer
		// would search for, and the Windows Features dialog lists it under a
		// third name again.
		return Accel{Name: "tcg", Reason: "this qemu build has no whpx. If QEMU has it and Windows does not, " +
			"enable the Windows Hypervisor Platform — in an elevated PowerShell:\n" +
			"      dism.exe /Online /Enable-Feature /All /FeatureName:HypervisorPlatform\n" +
			"    then reboot"}
	}
	return Accel{Name: "tcg", Reason: "no accelerator is known for " + runtime.GOOS}
}

// CPUModel is the -cpu value to pair with an accelerator.
//
// "host" is only meaningful when the guest CPU is the host CPU; under
// translation it asks QEMU to emulate whatever this machine happens to be,
// which is both slower and less reproducible than naming a model.
func CPUModel(t config.Target, a Accel) string {
	if a.Native {
		return "host"
	}
	return t.QEMUCPUEmulated
}

// installDirs are the places a QEMU install puts its binaries when nothing
// added them to PATH.
//
// This list exists because of Windows, where it is the normal case rather than
// the exception: the official installer and `winget` both drop QEMU in
// Program Files and neither touches PATH, so a developer who has just
// installed QEMU exactly as instructed is told it is not there. Being told to
// install something you already installed is the kind of message that ends an
// evaluation, and looking in the four places it can be is cheaper than
// explaining PATH.
//
// The unix entries are for the installs that a login shell would find but a
// GUI-launched process might not, and cost one stat each.
func installDirs() []string {
	switch runtime.GOOS {
	case "windows":
		var dirs []string
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
			if base := os.Getenv(env); base != "" {
				dirs = append(dirs, filepath.Join(base, "qemu"))
			}
		}
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			dirs = append(dirs, filepath.Join(base, "Programs", "qemu"))
		}
		if home, err := os.UserHomeDir(); err == nil {
			// scoop keeps the real binaries under the app directory; its shim
			// directory is on PATH and would have been found already.
			dirs = append(dirs, filepath.Join(home, "scoop", "apps", "qemu", "current"))
		}
		return append(dirs,
			`C:\ProgramData\chocolatey\lib\qemu\tools\qemu`,
			`C:\qemu`,
		)
	case "darwin":
		return []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}
	default:
		return []string{"/usr/bin", "/usr/local/bin"}
	}
}

// lookTool finds one of QEMU's binaries: on PATH first, then where the
// installers put it.
func lookTool(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, dir := range installDirs() {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found", name)
}

// FindQEMU locates the system emulator for a target.
func FindQEMU(t config.Target) (string, error) {
	if !t.SupportsVM() {
		return "", fmt.Errorf("no QEMU system emulator is defined for arch %s", t.Arch)
	}
	if env := os.Getenv("OWLAB_QEMU"); env != "" {
		return env, nil
	}
	path, err := lookTool(t.QEMUSystem)
	if err != nil {
		return "", fmt.Errorf("%s is not on PATH, and not in any of the places an installer puts it (%s).\n\nInstall QEMU:\n%s\n\nAlready installed somewhere else? Point owlab at it with OWLAB_QEMU=%s",
			t.QEMUSystem, strings.Join(installDirs(), ", "), installHint(), exampleQEMUPath(t))
	}
	return path, nil
}

// FindQEMUImg locates qemu-img, preferring the one that ships beside the
// system emulator already in use.
//
// Beside it rather than on PATH, because the two have to come from the same
// install: a qemu-img from a different QEMU can write a qcow2 the emulator
// then refuses. It is also the only way this works at all on Windows, where
// neither binary is on PATH to begin with.
func FindQEMUImg(qemuBin string) (string, error) {
	if env := os.Getenv("OWLAB_QEMU_IMG"); env != "" {
		return env, nil
	}
	name := "qemu-img"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if qemuBin != "" {
		if abs, err := filepath.Abs(qemuBin); err == nil {
			p := filepath.Join(filepath.Dir(abs), name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	p, err := lookTool("qemu-img")
	if err != nil {
		return "", fmt.Errorf("qemu-img is not beside %s and not on PATH. It ships with every QEMU install; point owlab at it with OWLAB_QEMU_IMG if this one is somewhere unusual", qemuBin)
	}
	return p, nil
}

func exampleQEMUPath(t config.Target) string {
	if runtime.GOOS == "windows" {
		return `C:\Program Files\qemu\` + t.QEMUSystem + ".exe"
	}
	return "/path/to/" + t.QEMUSystem
}

// installHint is the command to run on THIS machine, chosen by which package
// manager is actually installed rather than by which one the OS usually has.
func installHint() string {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("port"); err == nil {
			if _, err := exec.LookPath("brew"); err != nil {
				return "  sudo port install qemu"
			}
		}
		return "  brew install qemu"
	case "windows":
		for _, m := range []struct{ bin, cmd string }{
			{"winget", "  winget install SoftwareFreedomConservancy.QEMU"},
			{"scoop", "  scoop install qemu"},
			{"choco", "  choco install qemu"},
		} {
			if _, err := exec.LookPath(m.bin); err == nil {
				return m.cmd + "\n\nThe installer does not put QEMU on PATH, and does not need to: owlab looks in Program Files itself."
			}
		}
		return "  Download the installer from https://qemu.weilnetz.de/w64/"
	default:
		for _, m := range []struct{ bin, cmd string }{
			{"apt-get", "  sudo apt install qemu-system-x86 qemu-system-arm"},
			{"dnf", "  sudo dnf install qemu-system-x86 qemu-system-aarch64"},
			{"pacman", "  sudo pacman -S qemu-system-x86 qemu-system-aarch64"},
			{"zypper", "  sudo zypper install qemu-x86 qemu-arm"},
			{"apk", "  sudo apk add qemu-system-x86_64 qemu-system-aarch64"},
		} {
			if _, err := exec.LookPath(m.bin); err == nil {
				return m.cmd
			}
		}
		return "  install the qemu-system packages for your distribution"
	}
}

// FindFirmware locates the UEFI firmware a target needs, or returns "" when
// it needs none.
//
// Searched rather than hardcoded because every distribution ships edk2 under a
// different name in a different place, and the file is far too large to vendor
// into a CLI binary: the aarch64 build alone is 64 MB of padded flash image.
func FindFirmware(qemuBin string, t config.Target) (string, error) {
	if t.Firmware == "" {
		return "", nil
	}
	if env := os.Getenv("OWLAB_FIRMWARE"); env != "" {
		if _, err := os.Stat(env); err != nil {
			return "", fmt.Errorf("OWLAB_FIRMWARE=%s: %w", env, err)
		}
		return env, nil
	}

	var dirs []string
	// QEMU keeps its firmware beside itself, so the binary that was found on
	// PATH is the most reliable starting point — it is right for Homebrew,
	// for a manual build, and for a distribution package alike.
	//
	// Two layouts, because Windows uses the other one: a unix install puts the
	// firmware in ../share/qemu relative to the binary, while the Windows
	// build keeps edk2-*.fd in the same directory as qemu-system-*.exe.
	if abs, err := filepath.Abs(qemuBin); err == nil {
		dirs = append(dirs,
			filepath.Join(filepath.Dir(abs), "..", "share", "qemu"),
			filepath.Dir(abs),
		)
	}
	dirs = append(dirs,
		"/opt/homebrew/share/qemu",
		"/usr/local/share/qemu",
		"/usr/share/qemu",
		"/usr/share/edk2/aarch64",
		"/usr/share/edk2/arm",
		"/usr/share/AAVMF",
		"/usr/share/OVMF",
	)

	// Distribution aliases for the same firmware. Debian and Fedora both
	// rename it, and neither name matches the other.
	names := []string{t.Firmware}
	switch t.Firmware {
	case "edk2-aarch64-code.fd":
		names = append(names, "AAVMF_CODE.fd", "QEMU_EFI.fd", "QEMU_EFI-silent.fd")
	case "edk2-arm-code.fd":
		names = append(names, "AAVMF32_CODE.fd", "QEMU_EFI.fd")
	}

	for _, d := range dirs {
		for _, n := range names {
			p := filepath.Join(d, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf(
		"UEFI firmware for %s not found (looked for %s in %s).\n\n"+
			"This target publishes EFI-only images, so a firmware file is required.\n"+
			"Install one, or point owlab at it with OWLAB_FIRMWARE=/path/to/code.fd",
		t.Arch, strings.Join(names, ", "), strings.Join(dirs, ", "))
}

// Doctor reports what this host can do for the VM tier, for `owlab doctor`.
type Doctor struct {
	Arch     string
	Binary   string
	Version  string
	Accel    Accel
	Firmware string
	Err      error
}

// Inspect probes the host for one target without starting anything.
func Inspect(ctx context.Context, t config.Target) Doctor {
	d := Doctor{Arch: t.Arch}
	bin, err := FindQEMU(t)
	if err != nil {
		d.Err = err
		return d
	}
	d.Binary = bin
	if out, err := exec.CommandContext(ctx, bin, "--version").Output(); err == nil {
		if line, _, ok := strings.Cut(string(out), "\n"); ok {
			d.Version = strings.TrimSpace(line)
		}
	}
	d.Accel = ChooseAccel(bin, t)
	fw, err := FindFirmware(bin, t)
	if err != nil {
		d.Err = err
		return d
	}
	d.Firmware = fw
	return d
}
