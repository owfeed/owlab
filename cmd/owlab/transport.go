package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/VizzleTF/owlab/internal/config"
	"github.com/VizzleTF/owlab/internal/qemu"
	syncpkg "github.com/VizzleTF/owlab/internal/sync"
)

// vm returns the VM handle for a router, or nil when it is a container.
func (a *app) vm(r *config.Router) *qemu.VM {
	if r.Fidelity != config.VM {
		return nil
	}
	return qemu.New(a.cfg, r)
}

// execFor returns the way to run a shell script on a router.
//
// This is the seam between the two tiers. Everything above it — sync,
// install, post_sync, the LuCI cache drop — is written once against "run this
// script", and only this function knows whether that means `docker exec` or
// ssh.
func (a *app) execFor(r *config.Router) (syncpkg.Exec, error) {
	if r.Fidelity != config.VM {
		container := a.containerName(r.ID)
		return func(ctx context.Context, script string, stdin []byte, out io.Writer) error {
			args := []string{"exec"}
			if stdin != nil {
				args = append(args, "-i")
			}
			args = append(args, container, "/bin/sh", "-c", script)
			cmd := exec.CommandContext(ctx, "docker", args...)
			if stdin != nil {
				cmd.Stdin = bytes.NewReader(stdin)
			}
			cmd.Stdout = out
			cmd.Stderr = out
			return cmd.Run()
		}, nil
	}

	v := qemu.New(a.cfg, r)
	if !v.Running() {
		return nil, fmt.Errorf("%s is not running (start it with `owlab up %s`)", r.ID, r.ID)
	}
	target := v.SSH()
	return func(ctx context.Context, script string, stdin []byte, out io.Writer) error {
		return target.Run(ctx, script, stdin, out, out)
	}, nil
}

// interactive hands the terminal to a shell on a router.
func (a *app) interactive(ctx context.Context, r *config.Router, command ...string) error {
	if r.Fidelity == config.VM {
		v := qemu.New(a.cfg, r)
		if !v.Running() {
			return fmt.Errorf("%s is not running (start it with `owlab up %s`)", r.ID, r.ID)
		}
		return v.SSH().Interactive(ctx, command...)
	}
	args := []string{"exec", "-it", a.containerName(r.ID)}
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

// splitTiers separates routers by how they run.
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
