# Architecture

owlab is a single Go binary. It carries the image build context inside itself,
generates a Compose file for container routers, and spawns QEMU directly for VM
ones. There is no daemon and no state outside the project directory and one
image cache.

## The packages

```
cmd/owlab/           the CLI: one file per group of commands
internal/config/     owlab.yaml — parse, merge, validate
internal/compose/    generate the Compose file and the build context
internal/qemu/       the VM tier: images, accelerators, lifecycle, provisioning
internal/sync/       copy the project's source into routers, reload LuCI
internal/sshx/       run commands on a router over ssh
internal/engine/     identify the container engine and what it can do
internal/dockercli/  invoke docker / docker compose
images/              the build context, embedded into the binary
```

`images/` is embedded with `//go:embed all:images`. A developer who runs
`owlab up` inside their own LuCI package has no copy of this repository and
should not need one. The `all:` prefix is required — the overlay contains
`/etc/uci-defaults` files, and `go:embed` silently skips names starting with
`_` or `.` without it.

**Editing anything under `images/` means rebuilding the binary.** The running
binary carries its own copy; changing the file on disk changes nothing until
`go build` runs again. This has produced a false debugging trail more than
once.

## How a command flows

`owlab up`:

1. `config.Find` walks up from the working directory for `owlab.yaml`, so any
   subdirectory works.
2. `config.Load` parses it, merges `defaults:` into each router, applies the
   package arithmetic on top of the stock set, resolves `arch: auto` against
   the host, and validates. After this point a `Router` is fully determined —
   nothing downstream re-derives anything.
3. If any router is a container, Docker is checked and the engine identified.
   A project whose routers are all `fidelity: vm` never touches Docker, and
   demanding it would be a requirement owlab invented.
4. Routers split by tier. Containers go through `compose.Prepare` — which
   extracts the build context, downloads out-of-feed packages and the
   compliance bundle, writes `authorized_keys`, and emits a Compose file — and
   then `docker compose build` and `up`. VMs go through `qemu.New(...).Start`
   and, if the disk is new, `Provision`.
5. Both tiers are then polled on their forwarded HTTP port until LuCI answers.
   A router is "up" when uhttpd serves, not when the process starts: procd has
   to run the whole boot sequence and apply the uci-defaults first.

## The seam between the tiers

Everything above the transport is written once. `internal/sync` defines:

```go
type Exec func(ctx context.Context, script string, stdin []byte, out io.Writer) error
```

and `cmd/owlab/transport.go` returns either a `docker exec` closure or an ssh
one. Sync, `post_sync`, `install`, the LuCI cache drop and the interactive
shell are all written against "run this script, maybe with a tar on stdin", and
only that one function knows which kind of router it is talking to.

That is also why file transfer is a tar on stdin rather than `docker cp` or
`scp`: it is the single operation both transports have. dropbear ships no
sftp-server, so `scp` against a VM would need a package installed for it.

## What is generated, and where

```
<project>/.owlab/
  compose.yaml        generated on every run; never edit
  context/            the embedded build context, extracted
  cache/              out-of-feed packages and compliance files
  authorized_keys     the developer's public keys, concatenated
  vm/<id>/            one directory per VM router — disks, console, pidfile
```

`.owlab/` is gitignored by owlab itself on first use.

VM boot images do **not** live here. They go in the user cache directory
(`~/Library/Caches/owlab/images` on macOS), because they are a quarter of a
gigabyte each and are shared by every project on the machine that boots the
same release. Per-router writes go to a qcow2 overlay in the project, so the
cached image stays read-only and one download serves everything.

## Two rules that shaped the design

**Nothing addresses a router by its bridge IP.** Published ports only. A
container's bridge subnet is routable from the host on native Linux and inside
WSL2 but not on Docker Desktop for Mac or Windows, so `http://localhost:<port>`
is the only form that works identically everywhere.

**Capability is probed, never inferred from the platform.** Which accelerator
QEMU has, whether the kernel can make a flowtable, whether the engine forwards
UDP — each of these has been guessed wrong from the OS at some point in this
project's history. Homebrew's `qemu-system-x86_64` on Apple Silicon reports
`tcg` only, while `qemu-system-aarch64` from the same bottle has `hvf`; no
rule about macOS gets that right.
