package qemu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"owfeed.org/owlab/internal/config"
	"owfeed.org/owlab/internal/sshx"
)

// LANIP is the address the router serves LuCI and ssh on.
//
// It is not configurable, and it is not arbitrary: it is what
// /bin/config_generate assigns to the first ethernet port on every OpenWrt
// target, so it is the address the router has before owlab has said anything
// to it. The host reaches it through the port forwards below, never directly.
const LANIP = "192.168.1.1"

// VM is one fidelity-vm router and the files that make up its state.
type VM struct {
	Router *config.Router
	Config *config.Config

	// Dir holds this router's disks, console log and pidfile. It is the
	// router: deleting it is a factory reset, keeping it is why a VM survives
	// `owlab down` with its installed packages intact.
	Dir string
}

// New builds the handle for a router's VM. It touches nothing on disk.
func New(cfg *config.Config, r *config.Router) *VM {
	return &VM{
		Router: r,
		Config: cfg,
		Dir:    filepath.Join(cfg.Dir, ".owlab", "vm", r.ID),
	}
}

func (v *VM) diskPath() string    { return filepath.Join(v.Dir, "disk.qcow2") }
func (v *VM) overlayPath() string { return filepath.Join(v.Dir, "overlay.qcow2") }
func (v *VM) pidPath() string     { return filepath.Join(v.Dir, "qemu.pid") }
func (v *VM) statePath() string   { return filepath.Join(v.Dir, "state.json") }

// ConsolePath is the router's serial console, captured to a file.
//
// A file rather than an interactive terminal: the VM runs detached, and what a
// developer wants from it afterwards is `owlab logs` — the boot messages,
// procd's service output and any kernel complaint, in order. Interacting with
// the router happens over ssh, where a shell has a real terminal.
func (v *VM) ConsolePath() string { return filepath.Join(v.Dir, "console.log") }

// SSH is the transport for commands on this router.
func (v *VM) SSH() sshx.Target { return sshx.Local(v.Router.Ports.SSH) }

// state is what owlab remembers about a VM between invocations.
type state struct {
	Provisioned bool   `json:"provisioned"`
	Release     string `json:"release"`
	Distro      string `json:"distro"`
	Arch        string `json:"arch"`
	Image       string `json:"image"`
	Extroot     bool   `json:"extroot"`
}

func (v *VM) loadState() state {
	var s state
	body, err := os.ReadFile(v.statePath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(body, &s)
	return s
}

func (v *VM) saveState(s state) error {
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(v.statePath(), append(body, '\n'), 0o644)
}

// Provisioned reports whether this VM has already been set up.
func (v *VM) Provisioned() bool { return v.loadState().Provisioned }

// PID is the running QEMU process, or 0.
func (v *VM) PID() int {
	body, err := os.ReadFile(v.pidPath())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil || pid <= 0 {
		return 0
	}
	// A pidfile outlives the process that wrote it — QEMU does not remove it
	// on exit, and a machine that was rebooted may well have handed the
	// number to something else.
	if !processAlive(pid) {
		return 0
	}
	return pid
}

// Running reports whether this VM's QEMU process is alive.
func (v *VM) Running() bool { return v.PID() != 0 }

// State is a one-word status for `owlab status`.
func (v *VM) State() string {
	switch {
	case v.Running():
		return "running"
	case v.loadState().Provisioned:
		return "stopped"
	case fileExists(v.diskPath()):
		return "created"
	default:
		return "-"
	}
}

// StartOptions control one boot.
type StartOptions struct {
	// Rebuild throws the disks away first, which is the only factory reset
	// this tier has.
	Rebuild bool
	// Progress receives human-readable notes; nil to stay quiet.
	Progress io.Writer
	// Verbose echoes the QEMU command line.
	Verbose bool
}

// Start boots the VM, creating its disks on first use.
func (v *VM) Start(ctx context.Context, opts StartOptions) error {
	if v.Running() {
		return nil
	}
	t := v.Router.Target()

	qemuBin, err := FindQEMU(t)
	if err != nil {
		return err
	}
	firmware, err := FindFirmware(qemuBin, t)
	if err != nil {
		return err
	}
	accel := ChooseAccel(qemuBin, t)
	if !accel.Native && opts.Progress != nil {
		fmt.Fprintf(opts.Progress,
			"  ! %s runs without hardware acceleration: %s.\n"+
				"  !   Expect a boot measured in minutes rather than seconds.\n",
			v.Router.ID, accel.Reason)
	}

	if opts.Rebuild {
		if err := v.removeDisks(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(v.Dir, 0o755); err != nil {
		return err
	}

	base, err := EnsureImage(ctx, v.Router, opts.Progress)
	if err != nil {
		return err
	}
	if err := v.ensureDisks(ctx, base); err != nil {
		return err
	}

	args := v.qemuArgs(t, accel, firmware)
	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "+ %s %s\n", qemuBin, strings.Join(args, " "))
	}

	// -daemonize makes QEMU fork and write the pidfile itself, which is
	// exactly the detach owlab wants and one fewer thing to get right per
	// platform. It does not exist on Windows, where the process is started
	// detached and the pidfile written here instead.
	if runtime.GOOS != "windows" {
		cmd := exec.CommandContext(ctx, qemuBin, append(args, "-daemonize")...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("qemu failed to start: %s", msg)
		}
		return nil
	}

	cmd := exec.Command(qemuBin, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("qemu failed to start: %w", err)
	}
	if err := os.WriteFile(v.pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		return err
	}
	// Released rather than waited on: the VM outlives this command.
	return cmd.Process.Release()
}

// ensureDisks creates the qcow2 overlay on the cached image, and the extroot
// disk beside it.
func (v *VM) ensureDisks(ctx context.Context, base string) error {
	if !fileExists(v.diskPath()) {
		// A qcow2 with the downloaded image as its backing file. The base is
		// never written to, so one download serves every router and every
		// project on the machine, and this file holds only the difference.
		abs, err := filepath.Abs(base)
		if err != nil {
			return err
		}
		if err := qemuImg(ctx, "create", "-q", "-f", "qcow2",
			"-F", "raw", "-b", abs, v.diskPath()); err != nil {
			return err
		}
	}
	if v.Router.VM.Disk != "" && !fileExists(v.overlayPath()) {
		if err := qemuImg(ctx, "create", "-q", "-f", "qcow2", v.overlayPath(), v.Router.VM.Disk); err != nil {
			return err
		}
	}
	return nil
}

// qemuArgs is the whole machine definition.
func (v *VM) qemuArgs(t config.Target, accel Accel, firmware string) []string {
	r := v.Router
	args := []string{
		"-name", "owlab-" + r.ID,
		"-machine", t.QEMUMachine,
		"-accel", accel.Name,
		"-cpu", CPUModel(t, accel),
		"-smp", strconv.Itoa(r.VM.CPUs),
		"-m", r.VM.Memory,
		// No display at all: this is a headless router, and a QEMU window
		// popping up on every `owlab up` would be noise on three platforms.
		"-display", "none",
		"-serial", "file:" + v.ConsolePath(),
		"-pidfile", v.pidPath(),
		// OpenWrt's boot waits on entropy — urngd, dropbear host keys, the
		// jffs2 overlay. Without a virtio-rng the guest is stuck on a kernel
		// pool that a headless VM has almost nothing to fill from.
		"-object", "rng-random,filename=/dev/urandom,id=rng0",
		"-device", "virtio-rng-pci,rng=rng0",
	}
	if firmware != "" {
		args = append(args, "-bios", firmware)
	}
	args = append(args, "-drive", "if=virtio,format=qcow2,file="+v.diskPath())
	if v.Router.VM.Disk != "" {
		args = append(args, "-drive", "if=virtio,format=qcow2,file="+v.overlayPath())
	}

	// Two NICs, and the order is the whole design.
	//
	// OpenWrt's board.d makes the first ethernet port LAN and the second WAN.
	// LAN gets the static 192.168.1.1 and is where uhttpd and dropbear accept
	// connections; WAN gets DHCP and is where the firewall drops everything
	// inbound. Forwarding the host's ports to the WAN side would produce a
	// router that is up, reachable by nothing, and blamed on QEMU.
	//
	// The LAN network is given the guest's own subnet so that SLIRP will
	// deliver to 192.168.1.1: a hostfwd can only reach an address SLIRP
	// believes is on its network, and the guest's address is fixed by
	// config_generate before owlab gets a chance to say otherwise.
	lan := fmt.Sprintf("user,id=lan,net=192.168.1.0/24,host=192.168.1.254,hostfwd=tcp::%d-%s:80,hostfwd=tcp::%d-%s:22",
		r.Ports.HTTP, LANIP, r.Ports.SSH, LANIP)
	args = append(args,
		"-netdev", lan,
		"-device", "virtio-net-pci,netdev=lan",
		// WAN: a plain SLIRP network, which is what gives the router a
		// default route and working DNS so the package manager can reach the
		// feeds. It is NAT, so nothing on the host's network is exposed.
		"-netdev", "user,id=wan",
		"-device", "virtio-net-pci,netdev=wan",
	)
	return args
}

// Stop shuts the VM down.
//
// Asking the guest to power itself off, rather than killing QEMU: the overlay
// is a real filesystem with real dirty pages, and pulling the plug on every
// `owlab down` would eventually cost someone their installed packages. The
// kill is the fallback for a router that is wedged badly enough not to answer.
func (v *VM) Stop(ctx context.Context) error {
	pid := v.PID()
	if pid == 0 {
		os.Remove(v.pidPath())
		return nil
	}

	if v.SSH().Reachable() {
		shutdown, cancel := context.WithTimeout(ctx, 10*time.Second)
		// poweroff never returns cleanly — the connection dies with the
		// router — so its error says nothing and is deliberately ignored.
		_, _ = v.SSH().Output(shutdown, "poweroff")
		cancel()
	} else {
		terminate(pid)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !v.Running() {
			os.Remove(v.pidPath())
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	kill(pid)
	os.Remove(v.pidPath())
	return fmt.Errorf("%s did not shut down in time and was killed; its overlay may need an fsck on next boot", v.Router.ID)
}

// Destroy stops the VM and throws its disks away.
func (v *VM) Destroy(ctx context.Context) error {
	if err := v.Stop(ctx); err != nil {
		return err
	}
	return os.RemoveAll(v.Dir)
}

func (v *VM) removeDisks() error {
	for _, p := range []string{v.diskPath(), v.overlayPath(), v.statePath(), v.ConsolePath()} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// qemuImg runs qemu-img, which ships with every QEMU install.
func qemuImg(ctx context.Context, args ...string) error {
	bin := "qemu-img"
	if env := os.Getenv("OWLAB_QEMU_IMG"); env != "" {
		bin = env
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("qemu-img %s: %s", args[0], msg)
	}
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
