//go:build windows

package qemu

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Flags for CreateProcess. Spelled out rather than taken from
// golang.org/x/sys/windows, which owlab does not depend on and would not
// otherwise need.
const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// detach arranges for a started process to outlive this one.
//
// DETACHED_PROCESS gives it no console, so closing the terminal owlab was run
// from does not deliver CTRL_CLOSE_EVENT to the VM. CREATE_NEW_PROCESS_GROUP
// does the same for Ctrl-C: without it, interrupting `owlab up` while a router
// is booting kills the router rather than the wait.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup,
	}
}

// Windows has no signals, so liveness and termination both go through the
// process table. tasklist and taskkill ship with every supported Windows and
// need no extra dependency.

func processAlive(pid int) bool {
	id := strconv.Itoa(pid)
	out, err := exec.Command("tasklist", "/FI", "PID eq "+id, "/NH").Output()
	if err != nil {
		return false
	}
	// tasklist prints an informational line rather than failing when nothing
	// matches, so the pid has to appear in the output to count.
	return strings.Contains(string(out), id)
}

func terminate(pid int) { _ = exec.Command("taskkill", "/PID", strconv.Itoa(pid)).Run() }

func kill(pid int) { _ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run() }
