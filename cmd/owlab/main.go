// Command owlab runs OpenWrt-family dev routers described by owlab.yaml.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/VizzleTF/owlab/internal/config"
	"github.com/VizzleTF/owlab/internal/dockercli"
	"github.com/VizzleTF/owlab/internal/engine"
)

// version is stamped by the release build.
var version = "dev"

type app struct {
	docker dockercli.Runner
	cfg    *config.Config
	eng    engine.Info
}

const usage = `owlab — dev routers for OpenWrt/ImmortalWrt package development

Usage:
  owlab <command> [routers...] [flags]

Commands:
  up          build and start routers
  down        stop routers
  shell       open a shell on a router
  exec        run a command on a router
  install     install packages on a running router
  logs        show a router's boot and service log
  status      list routers and where to reach them
  open        open a router's LuCI in a browser
  doctor      check this machine for problems
  version     print the owlab version

Run 'owlab <command> -h' for the flags of one command.

Routers are named by the ids in owlab.yaml. With no ids, commands that
can act on many routers act on all of them.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	case "version", "--version", "-v":
		fmt.Printf("owlab %s\n", version)
		return
	}

	if err := run(ctx, cmd, args); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "owlab: "+err.Error())
		if code := dockercli.ExitCode(err); code > 0 {
			os.Exit(code)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, args []string) error {
	a := &app{}

	// doctor is the one command that must work when everything else is
	// broken, so it loads nothing up front.
	if cmd == "doctor" {
		return a.doctor(ctx, args)
	}

	if err := dockercli.Check(ctx); err != nil {
		return err
	}
	a.eng = engine.Detect(ctx)
	if a.eng.Err != nil {
		return a.eng.Err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	path, err := config.Find(cwd)
	if err != nil {
		return fmt.Errorf("%w\n\nCreate one with the routers you want; see `owlab doctor` for what this machine supports", err)
	}
	a.cfg, err = config.Load(path)
	if err != nil {
		return err
	}

	switch cmd {
	case "up":
		return a.up(ctx, args)
	case "down":
		return a.down(ctx, args)
	case "shell":
		return a.shell(ctx, args)
	case "exec":
		return a.exec(ctx, args)
	case "install":
		return a.install(ctx, args)
	case "logs":
		return a.logs(ctx, args)
	case "status":
		return a.status(ctx, args)
	case "open":
		return a.open(ctx, args)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}
