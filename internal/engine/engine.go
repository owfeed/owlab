// Package engine identifies which container engine is in front of us and what
// it can and cannot do.
//
// This exists because the honest answer to "does WiFi simulation work here?"
// is different on every macOS setup, and guessing wrong is worse than saying
// no. Docker Desktop's LinuxKit kernel is built with `# CONFIG_CFG80211 is not
// set`, so mac80211_hwsim — and virt_wifi, and vwifi, and wmediumd, all of
// which sit on top of cfg80211 — cannot load there at all. OrbStack's kernel
// has no wireless stack either. Colima and Lima run a stock Ubuntu kernel the
// user has root on, so there the same technique that works on Linux works
// unchanged.
package engine

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Kind is a container engine owlab recognises.
type Kind string

const (
	DockerDesktop  Kind = "docker-desktop"
	OrbStack       Kind = "orbstack"
	Colima         Kind = "colima"
	Lima           Kind = "lima"
	RancherDesktop Kind = "rancher-desktop"
	Podman         Kind = "podman"
	NativeLinux    Kind = "native-linux"
	Unknown        Kind = "unknown"
)

// Info is what we could learn about the local engine.
type Info struct {
	Kind Kind
	// Context is the docker context name, e.g. "orbstack" or "colima".
	Context string
	// OperatingSystem is docker info's own description of the daemon host.
	OperatingSystem string
	// ServerVersion is the daemon version.
	ServerVersion string
	// HostOS is the OS owlab itself is running on.
	HostOS string
	// WSL reports whether owlab is running inside WSL.
	WSL bool
	// Err is set when the daemon could not be reached at all.
	Err error
}

// Detect asks the daemon what it is.
func Detect(ctx context.Context) Info {
	info := Info{Kind: Unknown, HostOS: runtime.GOOS, WSL: inWSL()}

	info.Context = strings.TrimSpace(output(ctx, "docker", "context", "show"))

	out := output(ctx, "docker", "info", "--format",
		"{{.OperatingSystem}}\n{{.ServerVersion}}")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 0 {
		info.OperatingSystem = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		info.ServerVersion = strings.TrimSpace(lines[1])
	}
	if info.OperatingSystem == "" {
		info.Err = errDaemonUnreachable
		return info
	}

	info.Kind = classify(info.OperatingSystem, info.Context, info.HostOS, info.WSL)
	return info
}

func classify(osName, dockerCtx, hostOS string, wsl bool) Kind {
	lo := strings.ToLower(osName)
	lc := strings.ToLower(dockerCtx)

	// The daemon's own description is the strongest signal when it names a
	// product; the context name disambiguates the several engines that all
	// report a plain Linux distribution.
	switch {
	case strings.Contains(lo, "orbstack"):
		return OrbStack
	case strings.Contains(lo, "docker desktop"):
		return DockerDesktop
	case strings.Contains(lo, "podman") || strings.Contains(lc, "podman"):
		return Podman
	case strings.Contains(lc, "colima"):
		return Colima
	case strings.Contains(lc, "rancher"):
		return RancherDesktop
	case strings.Contains(lc, "lima"):
		return Lima
	case hostOS == "linux":
		// A Linux daemon on a Linux host with no product marker is the real
		// thing — including inside WSL, where the kernel is a normal Linux
		// kernel the user can load modules into.
		return NativeLinux
	}
	return Unknown
}

// HostKernelWiFi reports whether this engine can give a container real
// mac80211_hwsim radios, and why not when it cannot.
//
// The rule is about whose kernel the containers run under and whether the
// user can load modules into it — not about the host OS.
func (i Info) HostKernelWiFi() (ok bool, reason string) {
	switch i.Kind {
	case NativeLinux:
		return true, ""
	case Colima, Lima:
		// Stock Ubuntu cloud-image kernel; cfg80211, mac80211 and
		// mac80211_hwsim ship in linux-modules-extra-$(uname -r), which is
		// not installed by default but can be.
		return true, ""
	case DockerDesktop:
		if i.HostOS == "windows" || i.WSL {
			// The WSL2 backend runs containers under the WSL kernel, which is
			// a real Linux kernel — the same path as native Linux.
			return true, ""
		}
		return false, "Docker Desktop's LinuxKit kernel is built with CONFIG_CFG80211 unset, " +
			"so mac80211_hwsim cannot load (and neither can virt_wifi, vwifi or wmediumd, " +
			"which all sit on top of cfg80211)"
	case OrbStack:
		return false, "OrbStack's kernel ships no wireless stack and does not support custom modules"
	case RancherDesktop:
		return false, "Rancher Desktop's alpine-lima kernel is fixed and has no supported way to add modules"
	case Podman:
		return false, "the Podman machine image is immutable; adding kernel modules needs rpm-ostree and a reboot"
	}
	return false, "unrecognised container engine"
}

// BindMountsAreHostOwned reports whether a bind-mounted file keeps the host's
// uid/gid inside the container.
//
// This is true only on native Linux. Docker Desktop on Mac and Windows
// presents mounted files as owned by the container user, so the uid-mismatch
// class of bug simply does not occur there. It matters for anything we mount
// that a daemon inspects the ownership of — dropbear's authorized_keys being
// the one that bites, since it reports only "Permission denied (publickey)".
func (i Info) BindMountsAreHostOwned() bool {
	return i.Kind == NativeLinux || i.Kind == Colima || i.Kind == Lima
}

// Describe is a one-line human description for status and doctor output.
func (i Info) Describe() string {
	if i.Err != nil {
		return "docker daemon unreachable"
	}
	s := string(i.Kind)
	if i.OperatingSystem != "" {
		s += " (" + i.OperatingSystem + ")"
	}
	return s
}

func inWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(b)), "microsoft")
}

func output(ctx context.Context, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

type daemonErr struct{}

func (daemonErr) Error() string {
	return "cannot reach the docker daemon — is it running? (`docker info` failed)"
}

var errDaemonUnreachable = daemonErr{}
