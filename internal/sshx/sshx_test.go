package sshx

import (
	"bytes"
	"context"
	"io"
	"slices"
	"testing"
)

// The VM tier's half of #21: ssh gets stdin exactly when one is given, never a
// tty, and stdout and stderr stay apart.
func TestCommandWiresStreams(t *testing.T) {
	target := Local(2222)
	var stdout, stderr bytes.Buffer

	plain := target.command(context.Background(), "uname -a", nil, &stdout, &stderr)
	if plain.Stdin != nil {
		t.Error("no stdin, but cmd.Stdin is set")
	}

	in := bytes.NewReader([]byte("hello-stdin\n"))
	piped := target.command(context.Background(), "cat", in, &stdout, &stderr)
	if piped.Stdin != io.Reader(in) {
		t.Error("stdin was not handed to ssh as given")
	}
	// -t would rewrite LF as CRLF in everything piped through.
	if slices.Contains(piped.Args, "-t") {
		t.Errorf("Stream must never allocate a tty: %v", piped.Args)
	}
	if n := len(piped.Args); n < 2 || piped.Args[n-1] != "cat" || piped.Args[n-2] != "root@127.0.0.1" {
		t.Errorf("args = %v, want the script last, after the destination", piped.Args)
	}
	if piped.Stdout != &stdout || piped.Stderr != &stderr {
		t.Error("stdout and stderr must go to their own writers")
	}
}
