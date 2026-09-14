package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"owfeed.org/owlab/internal/compose"
	"owfeed.org/owlab/internal/config"
	"owfeed.org/owlab/internal/dockercli"
	"owfeed.org/owlab/internal/qemu"
	syncpkg "owfeed.org/owlab/internal/sync"
)

// tier is how one router runs.
//
// This is the seam between the two tiers. Everything above it — sync, install,
// exec, shell, post_sync, the LuCI cache drop, the status table — is written
// once against these three operations, and only the implementations below know
// whether "run this script" means `docker exec` or ssh.
//
// Deliberately per-router, and deliberately this small. The batch operations —
// build, up, down, logs — are NOT here: compose acts on a whole project at once
// and gets its network teardown from doing so, while the VM tier spawns one
// host process per router. Pretending those are the same shape would turn one
// build into N and cost `owlab down` the thing that makes it a teardown.
type tier interface {
	// Stream returns the way to run a shell script on this router, with stdin,
	// stdout and stderr as three separate streams. It is the only way either
	// tier runs a script: the byte-buffer Exec that sync, install and the
	// assertions use is derived from it in execFor, so `owlab exec` and those
	// callers cannot drift onto two different paths to the same router.
	Stream() (stream, error)
	// Interactive hands the terminal to a shell on this router.
	Interactive(ctx context.Context, command ...string) error
	// State is the one-word status `owlab status` prints.
	State(ctx context.Context) string
}

// tierFor picks the implementation for a router. It touches nothing: both
// implementations are handles, and the work happens when one is used.
func (a *app) tierFor(r *config.Router) tier {
	if r.Fidelity == config.VM {
		return vmTier{router: r, vm: qemu.New(a.cfg, r)}
	}
	return containerTier{
		router: r,
		name:   compose.ContainerName(a.cfg.Project.Name, r.ID),
		docker: a.docker,
	}
}

// ---------------------------------------------------------------------------
// container
// ---------------------------------------------------------------------------

type containerTier struct {
	router *config.Router
	name   string
	docker dockercli.Runner
}

func (c containerTier) Stream() (stream, error) {
	return func(ctx context.Context, script string, stdin io.Reader, stdout, stderr io.Writer) error {
		return containerCommand(ctx, c.name, script, stdin, stdout, stderr).Run()
	}, nil
}

// containerCommand builds the `docker exec` a container router's script runs
// under, apart from running it, so the wiring can be tested without Docker.
//
// -i only when there is a stdin to give. Without -i docker does not forward
// its own stdin at all, which is how `printf x | owlab exec r -- cat` printed
// nothing and exited 0 (#21); with it and nothing attached, the command would
// wait on a stdin nobody closes. Never -t: a pseudo-terminal rewrites LF as
// CRLF and merges stderr into stdout, which breaks every pipe and every
// binary stream through exec — interactive use is `owlab shell`.
func containerCommand(ctx context.Context, name, script string, stdin io.Reader, stdout, stderr io.Writer) *exec.Cmd {
	args := []string{"exec"}
	if stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, name, "/bin/sh", "-c", script)
	cmd := exec.CommandContext(ctx, "docker", args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd
}

func (c containerTier) Interactive(ctx context.Context, command ...string) error {
	args := []string{"exec", "-it", c.name}
	if len(command) == 0 {
		// -l so the login profile runs and the prompt looks like the router's.
		args = append(args, "/bin/sh", "-l")
	} else {
		args = append(args, "/bin/sh", "-c", command[0])
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c containerTier) State(ctx context.Context) string {
	out, err := c.docker.Quiet(ctx, "inspect", "-f", "{{.State.Status}}", c.name)
	if err != nil {
		return "-"
	}
	return strings.TrimSpace(out)
}

// ---------------------------------------------------------------------------
// vm
// ---------------------------------------------------------------------------

type vmTier struct {
	router *config.Router
	vm     *qemu.VM
}

// notRunning is the error both VM operations give when there is no router to
// talk to. A container is started by compose and reported by docker; a VM that
// is not running has nothing listening on its forwarded port, and "connection
// refused" does not say which router or how to start it.
func (v vmTier) notRunning() error {
	return fmt.Errorf("%s is not running (start it with `owlab up %s`)", v.router.ID, v.router.ID)
}

func (v vmTier) Stream() (stream, error) {
	if !v.vm.Running() {
		return nil, v.notRunning()
	}
	return v.vm.SSH().Stream, nil
}

func (v vmTier) Interactive(ctx context.Context, command ...string) error {
	if !v.vm.Running() {
		return v.notRunning()
	}
	return v.vm.SSH().Interactive(ctx, command...)
}

func (v vmTier) State(context.Context) string { return v.vm.State() }

// ---------------------------------------------------------------------------

// stream runs a shell script on a router with its streams kept apart.
//
// stdin is a reader rather than bytes because `owlab exec` passes its own
// stdin through, and that has no length: `yes | owlab exec r -- head -1` must
// end when head does, not after reading an endless input into memory. A nil
// stdin means none — the command reads the null device.
type stream func(ctx context.Context, script string, stdin io.Reader, stdout, stderr io.Writer) error

// execFor returns the way to run a shell script on a router, with stdin as a
// buffer and both output streams into one writer — the shape sync, install
// and the assertions were written against, and still get unchanged.
func (a *app) execFor(r *config.Router) (syncpkg.Exec, error) {
	s, err := a.tierFor(r).Stream()
	if err != nil {
		return nil, err
	}
	return bufferedExec(s), nil
}

// bufferedExec adapts a stream to the byte-buffer Exec.
//
// A nil slice stays a nil reader, and only a nil slice does: the tiers add
// `docker exec -i` exactly when stdin is non-nil, as they did when they took
// bytes, so an empty tar still gets -i and a plain script still does not. A
// nil *bytes.Reader inside a non-nil io.Reader would be attached as stdin and
// panic on the first read.
func bufferedExec(s stream) syncpkg.Exec {
	return func(ctx context.Context, script string, stdin []byte, out io.Writer) error {
		var in io.Reader
		if stdin != nil {
			in = bytes.NewReader(stdin)
		}
		return s(ctx, script, in, out, out)
	}
}

// execStdin is what `owlab exec` hands the command as stdin: its own stdin
// when that is a pipe or a file, and nothing when it is a terminal.
//
// A terminal is left out because exec has no pseudo-terminal on the other end:
// a shell there would read keystrokes with no echo and no line editing, and a
// command that never reads stdin — the common case, `owlab exec r -- uname` —
// would change nothing, so there is no gain to offset it. Before #21 exec never
// attached stdin, so for a terminal this is exactly the old behaviour.
//
// os.ModeCharDevice rather than a terminal library: the null device is a
// character device too, and treating it as "no stdin" is also correct.
func execStdin(f *os.File) io.Reader {
	st, err := f.Stat()
	if err != nil {
		// A closed stdin (`owlab exec r -- cmd <&-`) has nothing to give.
		return nil
	}
	if st.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	return f
}

// interactive hands the terminal to a shell on a router.
func (a *app) interactive(ctx context.Context, r *config.Router, command ...string) error {
	return a.tierFor(r).Interactive(ctx, command...)
}

// splitTiers separates routers by how they run.
//
// Still here, and still needed: the batch operations genuinely differ between
// the tiers, which is exactly why they are not on the tier interface.
func splitTiers(routers []*config.Router) (containers, vms []*config.Router) {
	for _, r := range routers {
		if r.Fidelity == config.VM {
			vms = append(vms, r)
		} else {
			containers = append(containers, r)
		}
	}
	return containers, vms
}
