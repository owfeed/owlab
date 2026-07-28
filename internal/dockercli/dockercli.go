// Package dockercli runs the docker CLI.
//
// owlab shells out rather than speaking to the daemon socket directly. The
// socket path differs per engine (Docker Desktop, OrbStack, Colima and Lima
// each put it somewhere else, and each rewrites the docker context to match),
// and the CLI already resolves all of that. Shelling out also means `owlab up`
// behaves exactly like the `docker compose` the developer would have run by
// hand, which matters when they need to debug it.
package dockercli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes docker commands.
type Runner struct {
	// Verbose echoes each command before running it.
	Verbose bool
}

// Compose runs `docker compose -f <file> <args...>` with stdio attached.
func (r Runner) Compose(ctx context.Context, file string, args ...string) error {
	full := append([]string{"compose", "-f", file}, args...)
	return r.run(ctx, full...)
}

// Run runs a docker command with stdio attached.
func (r Runner) Run(ctx context.Context, args ...string) error {
	return r.run(ctx, args...)
}

// Quiet runs a docker command and captures stdout, discarding stderr and
// reporting failure only through the error.
func (r Runner) Quiet(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.Output()
	return string(out), err
}

func (r Runner) run(ctx context.Context, args ...string) error {
	if r.Verbose {
		fmt.Fprintln(os.Stderr, "+ docker "+strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Check verifies that the docker CLI and the compose plugin are present.
func Check(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("docker is not on PATH — install Docker Desktop, OrbStack, Colima or docker-ce")
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		return errors.New("`docker compose` is not available — owlab needs Compose v2 (the plugin, not the old docker-compose script)")
	}
	return nil
}

// ExitCode extracts the exit status from a failed command, or -1.
func ExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
