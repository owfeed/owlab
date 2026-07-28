// Command owlab runs OpenWrt-family dev routers described by owlab.yaml.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/VizzleTF/owlab/internal/config"
	"github.com/VizzleTF/owlab/internal/dockercli"
	"github.com/VizzleTF/owlab/internal/engine"
)

type app struct {
	docker dockercli.Runner
	cfg    *config.Config
	eng    engine.Info

	// configPath is what --config or OWLAB_CONFIG said, before resolution.
	// Commands that load a config never see it; doctor does, because it
	// reports on the resolution itself.
	configPath string
}

// command is one owlab subcommand.
//
// A table rather than a switch, because the dispatcher and the help text are
// then the same list. They were two before, and had already drifted: `context`
// was dispatched and undocumented.
type command struct {
	// name is what the user types.
	name string
	// summary is the line in the help text. Empty keeps the command out of it
	// — `context` exists for CI and is not something to offer a developer.
	summary string
	// bare runs without loading a config or looking for a container engine,
	// because the times these commands are needed are the times those are what
	// is broken.
	bare bool
	run  func(*app, context.Context, []string) error
}

var commands = []command{
	{name: "up", summary: "build and start routers", run: (*app).up},
	{name: "down", summary: "stop routers", run: (*app).down},
	{name: "shell", summary: "open a shell on a router", run: (*app).shell},
	{name: "exec", summary: "run a command on a router", run: (*app).exec},
	{name: "sync", summary: "copy this package's source into the routers and reload LuCI", run: (*app).sync},
	{name: "install", summary: "install packages (or a local .apk/.ipk) on a running router", run: (*app).install},
	{name: "build", summary: "build a real .apk/.ipk with the OpenWrt SDK", run: (*app).build},
	{name: "logs", summary: "show a router's boot and service log", run: (*app).logs},
	{name: "releases", summary: "what the download servers publish, and how stale the pins are", run: (*app).releases},
	{name: "status", summary: "list routers and where to reach them", run: (*app).status},
	{name: "open", summary: "open a router's LuCI in a browser", run: (*app).open},
	{name: "doctor", summary: "check this machine for problems", bare: true, run: (*app).doctor},
	{name: "version", summary: "print the owlab version", bare: true, run: (*app).version},

	// Undocumented: this prepares a build context and prints the arguments for
	// `docker buildx build`, which is a thing the publish workflow needs and a
	// developer does not.
	{name: "context", run: (*app).buildContext},
}

func lookupCommand(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

const usageHead = `owlab — dev routers for OpenWrt/ImmortalWrt package development

Usage:
  owlab [--config <path>] <command> [routers...] [flags]

Commands:
`

const usageTail = `
Run 'owlab <command> -h' for the flags of one command.

Routers are named by the ids in owlab.yaml. With no ids, commands that
can act on many routers act on all of them.

owlab.yaml is looked for in the working directory and its parents. Point it
somewhere else with --config (or -c), which takes the file or the directory
holding it, or set OWLAB_CONFIG.
`

func usage() string {
	var b strings.Builder
	b.WriteString(usageHead)
	for _, c := range commands {
		if c.summary == "" {
			continue
		}
		fmt.Fprintf(&b, "  %-11s %s\n", c.name, c.summary)
	}
	b.WriteString(usageTail)
	return b.String()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage())
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
		fmt.Fprint(os.Stderr, usage())
		os.Exit(2)
	}
	cmd := argv[0]
	args := argv[1:]

	// The two spellings people reach for that are not subcommand names.
	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(usage())
		return
	case "--version", "-v":
		cmd = "version"
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
	c, ok := lookupCommand(cmd)
	if !ok {
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage())
	}

	a := &app{configPath: configPath}
	if c.bare {
		return c.run(a, ctx, args)
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
	return c.run(a, ctx, args)
}
