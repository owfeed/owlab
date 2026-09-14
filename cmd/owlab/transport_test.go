package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// #21: exec never attached stdin, so a pipe into it was dropped and the command
// exited 0 on an empty input. A pipe and a redirected file are both "not a
// terminal" and both have to reach the command.
func TestExecStdinAttachesPipesAndFiles(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if got := execStdin(r); got == nil {
		t.Error("a pipe on stdin must be attached")
	}

	path := filepath.Join(t.TempDir(), "in")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := execStdin(f); got == nil {
		t.Error("a file redirected onto stdin must be attached")
	}
}

// The other direction: a character device — a terminal, or the null device,
// which is the one a test can open everywhere — is not attached, which is
// exactly what exec did for a terminal before #21.
func TestExecStdinLeavesACharacterDeviceAlone(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("%s is not reported as a character device here", os.DevNull)
	}
	// Compared against untyped nil on purpose: a nil *os.File inside the
	// interface would pass `!= nil` in the tier and get `docker exec -i`.
	if got := execStdin(f); got != nil {
		t.Errorf("execStdin(%s) = %v, want no stdin", os.DevNull, got)
	}

	closed, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	if got := execStdin(closed); got != nil {
		t.Error("a closed stdin must not be attached")
	}
}

func TestContainerCommandAddsDashIOnlyWithStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer

	plain := containerCommand(context.Background(), "owlab-p-r", "uname -a", nil, &stdout, &stderr)
	if slices.Contains(plain.Args, "-i") {
		t.Errorf("no stdin, but args = %v", plain.Args)
	}
	if plain.Stdin != nil {
		t.Error("no stdin, but cmd.Stdin is set")
	}

	in := bytes.NewReader([]byte("hello-stdin\n"))
	piped := containerCommand(context.Background(), "owlab-p-r", "cat", in, &stdout, &stderr)
	want := []string{"docker", "exec", "-i", "owlab-p-r", "/bin/sh", "-c", "cat"}
	if !slices.Equal(piped.Args, want) {
		t.Errorf("args = %v, want %v", piped.Args, want)
	}
	if piped.Stdin != io.Reader(in) {
		t.Error("stdin was not handed to docker as given")
	}
	// -t would merge the two streams and rewrite LF as CRLF in piped data.
	if slices.Contains(piped.Args, "-t") || slices.Contains(piped.Args, "-it") {
		t.Errorf("exec must never allocate a tty: %v", piped.Args)
	}
	if piped.Stdout != &stdout || piped.Stderr != &stderr {
		t.Error("stdout and stderr must go to their own writers")
	}
}

// sync, install and the assertions pass stdin as bytes and read both streams
// from one writer. Moving the tiers onto a stream must not change what they
// send: nil stays no stdin (no -i), a non-nil slice — even an empty one —
// stays attached, and out receives both streams.
func TestBufferedExecKeepsByteCallersAsTheyWere(t *testing.T) {
	type call struct {
		attached       bool
		body           string
		stdout, stderr io.Writer
	}
	var got []call
	fake := func(_ context.Context, _ string, stdin io.Reader, stdout, stderr io.Writer) error {
		c := call{stdout: stdout, stderr: stderr}
		if stdin != nil {
			c.attached = true
			b, err := io.ReadAll(stdin)
			if err != nil {
				t.Fatal(err)
			}
			c.body = string(b)
		}
		got = append(got, c)
		return nil
	}

	var out bytes.Buffer
	run := bufferedExec(fake)
	for _, stdin := range [][]byte{nil, {}, []byte("tar bytes")} {
		if err := run(context.Background(), "tar -x", stdin, &out); err != nil {
			t.Fatal(err)
		}
	}

	if got[0].attached {
		t.Error("nil stdin was attached")
	}
	if !got[1].attached || got[1].body != "" {
		t.Errorf("empty stdin: %+v, want attached and empty", got[1])
	}
	if !got[2].attached || got[2].body != "tar bytes" {
		t.Errorf("stdin body = %q, want %q", got[2].body, "tar bytes")
	}
	for i, c := range got {
		if c.stdout != &out || c.stderr != &out {
			t.Errorf("call %d: both streams must go to out", i)
		}
	}
}
