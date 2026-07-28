package engine

import "testing"

// classify is what makes a message name the true reason rather than a guess,
// so each of these rows is a real string one of these engines reports.
func TestClassify(t *testing.T) {
	cases := []struct {
		name      string
		osName    string
		dockerCtx string
		hostOS    string
		wsl       bool
		want      Kind
	}{
		{"orbstack names itself", "OrbStack", "orbstack", "darwin", false, OrbStack},
		{"docker desktop names itself", "Docker Desktop", "desktop-linux", "darwin", false, DockerDesktop},
		// These all report a plain Linux distribution, so only the context
		// name tells them apart.
		{"colima by context", "Ubuntu 24.04.1 LTS", "colima", "darwin", false, Colima},
		{"lima by context", "Ubuntu 24.04.1 LTS", "lima-default", "darwin", false, Lima},
		{"rancher by context", "Alpine Linux v3.20", "rancher-desktop", "darwin", false, RancherDesktop},
		{"podman by os", "podman", "default", "linux", false, Podman},
		{"podman by context", "Fedora Linux 41", "podman-machine", "darwin", false, Podman},
		// A Linux daemon on a Linux host with no product marker is the real
		// thing — including inside WSL, whose kernel is a normal one.
		{"native linux", "Ubuntu 24.04.1 LTS", "default", "linux", false, NativeLinux},
		{"wsl is still native", "Ubuntu 24.04.1 LTS", "default", "linux", true, NativeLinux},
		// A Linux daemon reached from macOS through nothing we recognise.
		{"unknown", "Debian GNU/Linux 12", "default", "darwin", false, Unknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.osName, c.dockerCtx, c.hostOS, c.wsl); got != c.want {
				t.Errorf("classify(%q, %q, %q) = %q, want %q",
					c.osName, c.dockerCtx, c.hostOS, got, c.want)
			}
		})
	}
}

// The uid-mismatch class of bug — dropbear refusing an authorized_keys it does
// not see as root's — only exists where a bind mount keeps the host's owner.
func TestBindMountsAreHostOwned(t *testing.T) {
	hostOwned := []Kind{NativeLinux, Colima, Lima}
	presented := []Kind{DockerDesktop, OrbStack, RancherDesktop, Podman, Unknown}

	for _, k := range hostOwned {
		if !(Info{Kind: k}).BindMountsAreHostOwned() {
			t.Errorf("%s should keep host uid/gid", k)
		}
	}
	for _, k := range presented {
		if (Info{Kind: k}).BindMountsAreHostOwned() {
			t.Errorf("%s should present mounts as the container user", k)
		}
	}
}

func TestDescribeReportsUnreachableDaemon(t *testing.T) {
	got := (Info{Kind: Unknown, Err: errDaemonUnreachable}).Describe()
	if got != "docker daemon unreachable" {
		t.Errorf("Describe = %q", got)
	}
	if got := (Info{Kind: OrbStack, OperatingSystem: "OrbStack"}).Describe(); got != "orbstack (OrbStack)" {
		t.Errorf("Describe = %q", got)
	}
}
