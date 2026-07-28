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

type app struct {
	docker dockercli.Runner
	cfg    *config.Config
	eng    engine.Info
}

const usage = `owlab — dev routers for OpenWrt/ImmortalWrt package development

Usage:
  owlab [--config <path>] <command> [routers...] [flags]

Commands:
  up          build and start routers
  down        stop routers
  shell       open a shell on a router
  exec        run a command on a router
  sync        copy this package's source into the routers and reload LuCI
  install     install packages (or a local .apk/.ipk) on a running router
  build       build a real .apk/.ipk with the OpenWrt SDK
  logs        show a router's boot and service log
  releases    what the download servers publish, and how stale the pins are
  status      list routers and where to reach them
  open        open a router's LuCI in a browser
  doctor      check this machine for problems
  version     print the owlab version

Run 'owlab <command> -h' for the flags of one command.

Routers are named by the ids in owlab.yaml. With no ids, commands that
can act on many routers act on all of them.

owlab.yaml is looked for in the working directory and its parents. Point it
somewhere else with --config (or -c), which takes the file or the directory
holding it, or set OWLAB_CONFIG.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	argv, configPath, err := extractConfigFlag(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "owlab: "+err.Error())
		os.Exit(2)
	}
	if len(argv) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := argv[0]
	args := argv[1:]

	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	case "version", "--version", "-v":
		fmt.Print(versionLine())
		return
	}

	if err := run(ctx, cmd, args, configPath); err != nil {
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

func run(ctx context.Context, cmd string, args []string, configPath string) error {
	a := &app{}

	// doctor is the one command that must work when everything else is
	// broken, so it loads nothing up front.
	if cmd == "doctor" {
		return a.doctor(ctx, args, configPath)
	}

	path, err := resolveConfig(configPath)
	if err != nil {
		return err
	}
	a.cfg, err = config.Load(path)
	if err != nil {
		return err
	}

	// The config decides whether a container engine is needed at all. A
	// project whose routers are all fidelity vm runs entirely on QEMU, and
	// refusing to start because Docker Desktop is not running would be a
	// requirement owlab invented.
	if a.cfg.HasContainers() {
		if err := dockercli.Check(ctx); err != nil {
			return err
		}
		a.eng = engine.Detect(ctx)
		if a.eng.Err != nil {
			return a.eng.Err
		}
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
	case "sync":
		return a.sync(ctx, args)
	case "context":
		return a.buildContext(ctx, args)
	case "install":
		return a.install(ctx, args)
	case "build":
		return a.build(ctx, args)
	case "logs":
		return a.logs(ctx, args)
	case "releases":
		return a.releases(ctx, args)
	case "status":
		return a.status(ctx, args)
	case "open":
		return a.open(ctx, args)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}
