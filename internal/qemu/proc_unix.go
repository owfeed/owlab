//go:build !windows

package qemu

import (
	"errors"
	"os/exec"
	"syscall"
)

// detach is a no-op here: QEMU is started with -daemonize on every platform
// that has it, so it has already detached itself by the time Start returns.
func detach(*exec.Cmd) {}

// processAlive reports whether a pid names a live process.
//
// Signal 0 performs the permission and existence checks without delivering
// anything. EPERM counts as alive: the process is there, it just belongs to
// somebody else — which for a pidfile that has been recycled is exactly the
// case worth not misreading as "gone".
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terminate(pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }

func kill(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
