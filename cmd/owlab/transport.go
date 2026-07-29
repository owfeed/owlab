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
	// Exec returns the way to run a shell script on this router, optionally
	// feeding it a tar on stdin.
	Exec() (syncpkg.Exec, error)
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

func (c containerTier) Exec() (syncpkg.Exec, error) {
	return func(ctx context.Context, script string, stdin []byte, out io.Writer) error {
		args := []string{"exec"}
		if stdin != nil {
			args = append(args, "-i")
		}
		args = append(args, c.name, "/bin/sh", "-c", script)
		cmd := exec.CommandContext(ctx, "docker", args...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		cmd.Stdout = out
		cmd.Stderr = out
		return cmd.Run()
	}, nil
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

func (v vmTier) Exec() (syncpkg.Exec, error) {
	if !v.vm.Running() {
		return nil, v.notRunning()
	}
	target := v.vm.SSH()
	return func(ctx context.Context, script string, stdin []byte, out io.Writer) error {
		return target.Run(ctx, script, stdin, out, out)
	}, nil
}

func (v vmTier) Interactive(ctx context.Context, command ...string) error {
	if !v.vm.Running() {
		return v.notRunning()
	}
	return v.vm.SSH().Interactive(ctx, command...)
}

func (v vmTier) State(context.Context) string { return v.vm.State() }

// ---------------------------------------------------------------------------

// execFor returns the way to run a shell script on a router.
func (a *app) execFor(r *config.Router) (syncpkg.Exec, error) {
	return a.tierFor(r).Exec()
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
