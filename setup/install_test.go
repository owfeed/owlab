// Package setup holds no Go: the test below runs install.sh's gh helpers under
// bash against a stand-in gh, because the shell that ships in the action is
// otherwise checked only by `bash -n`.
package setup

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// helpers is install.sh's gh_outage, bounded and gh_read, cut out by name so the
// test runs the shipped text and not a copy of it. The rest of the script
// installs a binary and is not run here.
func helpers(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, name := range []string{"gh_outage", "bounded", "gh_read"} {
		fn := regexp.MustCompile(`(?ms)^` + name + `\(\) \{\n.*?^\}\n`).Find(b)
		if fn == nil {
			t.Fatalf("install.sh has no function %s() at the start of a line", name)
		}
		out.Write(fn)
	}
	return out.String()
}

// fakeGH answers by GH_MODE and counts its calls in GH_CALLS. A hang is an exec
// of sleep, so the process bounded stops is the one holding the pipes, as with
// the real gh. Five seconds rather than forever, so that a script that lost its
// deadline fails the elapsed-time checks instead of hanging the test.
const fakeGH = `#!/bin/sh
n=$(( $(cat "$GH_CALLS" 2>/dev/null || echo 0) + 1 ))
echo "$n" > "$GH_CALLS"
case "$GH_MODE" in
  ok) echo v9.9.9 ;;
  hang-once) [ "$n" -eq 1 ] && exec sleep 5; echo v9.9.9 ;;
  hang) exec sleep 5 ;;
  missing) echo 'release not found' >&2; exit 1 ;;
esac
`

type ghRun struct {
	code           int
	stdout, stderr string
	calls          string
	took           time.Duration
}

func runGHRead(t *testing.T, mode string) ghRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh refuses Windows runners before it reaches gh")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "calls")

	// A one-second deadline and no pause between attempts: the shape of the
	// retries is what is under test, not their length.
	script := "set -euo pipefail\n" + helpers(t) +
		"gh_deadline=1\ngh_retry_delay=0\n" +
		"gh_read release view --repo owfeed/owlab --json tagName --jq .tagName\n"
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_MODE="+mode, "GH_CALLS="+calls)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err = cmd.Run()
	took := time.Since(start)

	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	n, _ := os.ReadFile(calls)
	return ghRun{code, stdout.String(), stderr.String(), strings.TrimSpace(string(n)), took}
}

// The healthy run: one call, its output passed through, nothing said about
// retrying.
func TestGHReadPassesAnAnswerThrough(t *testing.T) {
	r := runGHRead(t, "ok")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "v9.9.9" || r.calls != "1" {
		t.Fatalf("exit %d, stdout %q, %s call(s); want 0, v9.9.9, 1\nstderr: %s", r.code, r.stdout, r.calls, r.stderr)
	}
	if strings.Contains(r.stderr, "retrying") {
		t.Errorf("a healthy call talked about retrying: %s", r.stderr)
	}
}

// A gh call that goes quiet prints nothing for gh_outage to match. It used to
// hold the job until the job's own timeout; now it is stopped at the deadline
// and asked again.
func TestGHReadStopsAStalledCallAndAsksAgain(t *testing.T) {
	r := runGHRead(t, "hang-once")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "v9.9.9" || r.calls != "2" {
		t.Fatalf("exit %d, stdout %q, %s call(s); want 0, v9.9.9, 2\nstderr: %s", r.code, r.stdout, r.calls, r.stderr)
	}
	if !strings.Contains(r.stderr, "no answer from GitHub within 1s") {
		t.Errorf("the retry should say the call ran out of time: %s", r.stderr)
	}
	if r.took > 4*time.Second {
		t.Errorf("took %s: the stalled call was not stopped at its 1s deadline", r.took)
	}
}

// A stall on every attempt is an outage: exit 8, "safe to rerun".
func TestGHReadReportsAStallThatOutlastsTheAttemptsAsAnOutage(t *testing.T) {
	r := runGHRead(t, "hang")
	if r.code != 8 || r.calls != "4" {
		t.Fatalf("exit %d after %s call(s), want 8 after 4\nstdout: %s\nstderr: %s", r.code, r.calls, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "Upstream outage (safe to rerun)") {
		t.Errorf("stdout should carry the outage annotation: %s", r.stdout)
	}
	if r.took > 10*time.Second {
		t.Errorf("took %s for 4 one-second deadlines", r.took)
	}
}

// The deadline must not turn an answer into an outage: a release that does not
// exist is still exit 7 at once.
func TestGHReadStillStopsAtAnAnswer(t *testing.T) {
	r := runGHRead(t, "missing")
	if r.code != 7 || r.calls != "1" {
		t.Fatalf("exit %d after %s call(s), want 7 after 1\nstderr: %s", r.code, r.calls, r.stderr)
	}
}
