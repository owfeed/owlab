//go:build windows

package qemu

import (
	"os/exec"
	"strconv"
	"strings"
)

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
