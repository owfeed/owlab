// Package sshx runs commands on a router over ssh.
//
// It shells out to the system ssh client rather than speaking the protocol in
// Go. That is deliberate: the developer already has ssh, it already knows
// their keys and their agent, and a Go implementation would have to
// re-implement all of that to end up in the same place. The one thing owlab
// needs beyond the defaults is host-key handling, and that is a flag.
//
// This is the VM tier's equivalent of `docker exec` for containers: the same
// two operations (run a script, stream a tar in) over a different transport.
package sshx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Target is a router reachable on a forwarded local port.
type Target struct {
	Host string
	Port int
	User string
}

// Local builds a target for a port forwarded to localhost, which is the only
// way owlab ever reaches a VM.
func Local(port int) Target {
	return Target{Host: "127.0.0.1", Port: port, User: "root"}
}

// Addr is the host:port this target listens on.
func (t Target) Addr() string { return fmt.Sprintf("%s:%d", t.Host, t.Port) }

// opts are the ssh flags every owlab connection uses.
//
// Host key checking is off, and the known_hosts file is /dev/null. This is not
// a shortcut: the target is a throwaway VM on a forwarded localhost port, its
// host key is regenerated whenever the disk is rebuilt, and the port number is
// reused by the next router that takes that slot. Checking would produce a
// REMOTE HOST IDENTIFICATION HAS CHANGED wall of text on a routine rebuild and
// train the developer to delete lines from known_hosts. Nothing here
// authenticates anything — do not copy this pattern to a real host.
func (t Target) opts() []string {
	return []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=5",
		"-p", fmt.Sprint(t.Port),
	}
}

func (t Target) dest() string { return t.User + "@" + t.Host }

// Run executes a shell script on the router.
//
// BatchMode is on: a stock OpenWrt root account has an empty password and
// dropbear accepts it without a prompt, so a connection that would otherwise
// ask for one has genuinely failed and should say so rather than hang waiting
// for input that no script will type.
func (t Target) Run(ctx context.Context, script string, stdin []byte, stdout, stderr io.Writer) error {
	args := append(t.opts(), "-o", "BatchMode=yes", t.dest(), script)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// Output runs a script and returns its combined output, with the output
// folded into the error when it fails — an exit status on its own tells a
// developer nothing they can act on.
func (t Target) Output(ctx context.Context, script string) (string, error) {
	var buf bytes.Buffer
	err := t.Run(ctx, script, nil, &buf, &buf)
	out := buf.String()
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			return out, err
		}
		return out, fmt.Errorf("%s", msg)
	}
	return out, nil
}

// Interactive hands the terminal to a shell on the router.
func (t Target) Interactive(ctx context.Context, command ...string) error {
	args := append(t.opts(), "-t", t.dest())
	args = append(args, command...)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Wait blocks until the router answers on ssh, or the deadline passes.
//
// Two conditions, not one: the port has to accept a connection AND a command
// has to come back. During boot dropbear starts before the rest of the system
// is up, and a tool that returned at the first successful TCP handshake would
// hand the next step a router whose uci is not ready yet.
func (t Target) Wait(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn, err := net.DialTimeout("tcp", t.Addr(), 2*time.Second)
		if err == nil {
			conn.Close()
			probe, cancel := context.WithTimeout(ctx, 15*time.Second)
			_, err = t.Output(probe, "true")
			cancel()
			if err == nil {
				return nil
			}
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if lastErr != nil {
		return fmt.Errorf("no ssh on %s after %s: %w", t.Addr(), timeout, lastErr)
	}
	return fmt.Errorf("no ssh on %s after %s", t.Addr(), timeout)
}

// Reachable reports whether the router answers right now.
func (t Target) Reachable() bool {
	conn, err := net.DialTimeout("tcp", t.Addr(), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
