// Package engine identifies which container engine is in front of us and what
// it can and cannot do.
//
// The engines differ in ways that change owlab's behaviour and that no amount
// of reading the host OS will tell you: whether a bind-mounted file keeps the
// host's ownership inside the container, whether outbound UDP leaves at all,
// which resolver address the container is handed. Naming the engine is what
// lets a message say the true reason rather than a guess.
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
	info := Info{Kind: Unknown, HostOS: runtime.GOOS, WSL: InWSL()}

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

// InWSL reports whether owlab is running inside WSL.
//
// Exported and independent of Detect, because it is a property of the host and
// not of the container engine: a project whose routers are all fidelity vm
// never reaches a daemon, and the WSL caveats — inotify does not propagate from
// a Windows drive, IO across /mnt is slow — apply to it just the same.
func InWSL() bool {
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
