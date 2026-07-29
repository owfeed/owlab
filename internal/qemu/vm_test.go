package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"owfeed.org/owlab/internal/config"
)

func vmFor(t *testing.T) *VM {
	t.Helper()
	dir := t.TempDir()
	body := `version: 1
project:
  name: demo
routers:
  - id: box
    release: "25.12.4"
    arch: x86_64
    fidelity: vm
`
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, &cfg.Routers[0])
}

// New touches nothing on disk: every command builds a handle, and most of them
// only ask it questions.
func TestNewCreatesNothing(t *testing.T) {
	v := vmFor(t)
	if _, err := os.Stat(v.Dir); !os.IsNotExist(err) {
		t.Errorf("New created %s", v.Dir)
	}
}

// The state machine `owlab status` prints. Provisioning is a property of the
// DISK, not of the run — which is what makes a VM survive `owlab down` with
// its packages intact — so "stopped" and "created" are different answers.
func TestStateProgression(t *testing.T) {
	v := vmFor(t)

	if got := v.State(); got != "-" {
		t.Errorf("a router that has never been started should be %q, got %q", "-", got)
	}
	if v.Provisioned() {
		t.Error("nothing has been provisioned yet")
	}

	if err := os.MkdirAll(v.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v.diskPath(), []byte("qcow2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := v.State(); got != "created" {
		t.Errorf("a router with a disk and no provisioning should be %q, got %q", "created", got)
	}

	if err := v.saveState(state{Provisioned: true}); err != nil {
		t.Fatal(err)
	}
	if !v.Provisioned() {
		t.Error("the marker was written but not read back")
	}
	// Stopped, not created: the disk carries everything installed on it, and
	// the next `owlab up` is a plain boot rather than another provisioning run.
	if got := v.State(); got != "stopped" {
		t.Errorf("a provisioned router that is not running should be %q, got %q", "stopped", got)
	}
}

// A pidfile outlives the process that wrote it — QEMU does not remove it on
// exit, and a rebooted machine may well have handed the number to something
// else. Treating a stale one as live would make `up` a no-op forever.
func TestStalePidfileIsNotRunning(t *testing.T) {
	v := vmFor(t)
	if err := os.MkdirAll(v.Dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{"", "not-a-number", "0", "-1", "4294967290"} {
		if err := os.WriteFile(v.pidPath(), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if v.Running() {
			t.Errorf("pidfile %q was treated as a live process", body)
		}
	}
}

// The disks are the router; removeDisks is the only factory reset this tier
// has, and it must not fail on a router that was never started.
func TestRemoveDisksIsIdempotent(t *testing.T) {
	v := vmFor(t)
	if err := v.removeDisks(); err != nil {
		t.Fatalf("removing the disks of a router that has none: %v", err)
	}

	if err := os.MkdirAll(v.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{v.diskPath(), v.overlayPath(), v.statePath(), v.ConsolePath()} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.removeDisks(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{v.diskPath(), v.overlayPath(), v.statePath(), v.ConsolePath()} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived a rebuild", filepath.Base(p))
		}
	}
}

// Both ports go to the LAN side. OpenWrt's board.d makes the first ethernet
// port LAN and the second WAN; LAN holds the static 192.168.1.1 where uhttpd
// and dropbear listen, while WAN is where the firewall drops everything
// inbound. Forwarding to the WAN side would produce a router that is up and
// reachable by nothing.
func TestQEMUArgsForwardToTheLANSide(t *testing.T) {
	v := vmFor(t)
	tgt := v.Router.Target()
	args := v.qemuArgs(tgt, Accel{Name: "tcg"}, "")

	var lan string
	for i, a := range args {
		if a == "-netdev" && i+1 < len(args) && strings.HasPrefix(args[i+1], "user,id=lan") {
			lan = args[i+1]
		}
	}
	if lan == "" {
		t.Fatalf("no LAN netdev in the command line: %v", args)
	}
	for _, want := range []string{
		"hostfwd=tcp::8080-" + LANIP + ":80",
		"hostfwd=tcp::2222-" + LANIP + ":22",
		// SLIRP only delivers to an address it believes is on its network, and
		// the guest's is fixed by config_generate before owlab can say
		// otherwise.
		"net=192.168.1.0/24",
	} {
		if !strings.Contains(lan, want) {
			t.Errorf("LAN netdev missing %q:\n%s", want, lan)
		}
	}
}

// OpenWrt's boot waits on entropy — urngd, dropbear host keys, the jffs2
// overlay — and a headless VM has almost nothing to fill the kernel pool from.
func TestQEMUArgsAlwaysGiveTheGuestEntropy(t *testing.T) {
	v := vmFor(t)
	joined := strings.Join(v.qemuArgs(v.Router.Target(), Accel{Name: "tcg"}, ""), " ")
	for _, want := range []string{"rng-random", "virtio-rng-pci"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %s in the command line:\n%s", want, joined)
		}
	}
}
