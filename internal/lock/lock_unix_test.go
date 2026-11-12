//go:build !windows

package lock

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A project's gates take `flock -x <file>`, and the whole feature rests on
// owlab and them excluding each other. Checked against the real flock(1), in
// both directions.
func TestExcludesFlockCommand(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("no flock(1) on this host")
	}
	p := filepath.Join(t.TempDir(), "stands-r1.lock")

	// The gate holds the lock until its stdin closes.
	gate := exec.Command("flock", "-x", p, "sh", "-c", "echo held; cat >/dev/null")
	in, err := gate.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := gate.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Start(); err != nil {
		t.Fatal(err)
	}
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "held\n" {
		t.Fatalf("flock -x printed %q, want held", line)
	}
	if _, _, err := tryFor(t, 100*time.Millisecond, p); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire while flock -x holds the file: err = %v, want a deadline", err)
	}
	in.Close()
	if err := gate.Wait(); err != nil {
		t.Fatal(err)
	}

	release, _, err := tryFor(t, 2*time.Second, p)
	if err != nil {
		t.Fatalf("Acquire after flock -x exited: %v", err)
	}
	err = exec.Command("flock", "-n", p, "true").Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("flock -n while owlab holds the file: %v, want exit status 1", err)
	}
	release()
	if err := exec.Command("flock", "-n", p, "true").Run(); err != nil {
		t.Fatalf("flock -n after release: %v", err)
	}
}
