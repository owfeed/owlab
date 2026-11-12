# Internals

[Русская версия](internals_ru.md)

How owlab is put together inside, and why each part behaves the way it does. For contributors who
change owlab. You need none of this to use it: start with the [runbook](runbook.md).

- **One Go binary, no daemon.** State lives only in the project's `.owlab/` and in one image cache.
- **Two tiers per router.** `basic` is a container with procd as PID 1. `vm` is QEMU run as an
  ordinary host process.
- **Routers are reached through published ports only**, never by bridge IP. `localhost` works the same
  on every host.
- **Capability is probed, never inferred from the platform.** Guesses from the OS have been wrong in
  this project before.
- **Editing anything under `images/` means rebuilding the binary.** The binary carries its own copy.

## Find it fast

| Question | Section |
|---|---|
| Which Go package does what? | [Go packages](#go-packages) |
| What happens during `owlab up`? | [How a command flows](#how-a-command-flows) |
| Why one interface for both tiers? | [The seam between the tiers](#the-seam-between-the-tiers) |
| Where do generated files go? | [What is generated, and where](#what-is-generated-and-where) |
| Container or VM: what each can do | [Tiers](#tiers) |
| Why the container's lan is `br-lan` | [`br-lan`](#br-lan) |
| Why DNS and UDP/53 behave oddly | [DNS](#dns), [Outbound UDP port 53](#outbound-udp-port-53) |
| Why a router resets every TCP connection | [Flow offloading](#flow-offloading) |
| How packages get installed, and what a failure costs | [Packages](#packages) |
| Why feeds are never rewritten | [Feeds](#feeds) |
| How the VM boots, provisions and shuts down | [The VM tier](#the-vm-tier) |

## Architecture

owlab is a single Go binary. It carries the image build context inside itself. It generates a
Compose file for container routers and spawns QEMU directly for VM ones. There is no daemon. There is
no state outside the project directory and one image cache.

### Go packages

```
cmd/owlab/           the CLI: one file per group of commands
internal/config/     owlab.yaml — parse, merge, validate
internal/compose/    generate the Compose file and the build context
internal/qemu/       the VM tier: images, accelerators, lifecycle, provisioning
internal/sync/       copy the project's source into routers, reload LuCI
internal/sshx/       run commands on a router over ssh
internal/upstream/   ask the download servers what they publish
internal/engine/     identify the container engine and what it can do
internal/dockercli/  invoke docker / docker compose
internal/pkgmgr/     build the apk/opkg command line
internal/tarx/       build the tar streams pushed into routers
images/              the build context, embedded into the binary
```

`pkgmgr` and `tarx` exist because both tiers do the same thing by different means. A difference
between the tiers there would be a bug nobody could see.

- `pkgmgr` knows that `--allow-untrusted` is apk's alone. It also knows that a missing feed package
  should cost that package rather than the router.
- `tarx` knows the header rules. It writes GNU format, because a LuCI tree reaches paths past the
  100 bytes USTAR allows.

`images/` is embedded with `//go:embed all:images`. A developer who runs `owlab up` inside their own
LuCI package has no copy of this repository and should not need one.

The `all:` prefix is required. The overlay contains `/etc/uci-defaults` files, and without the prefix
`go:embed` silently skips names starting with `_` or `.`.

The overlay is `images/rootfs-extra/`. `images/Dockerfile` copies it to `/` in the image, and a VM gets
it pushed in at provisioning.

| File | Path in this repository |
|---|---|
| `95_owlab-base` | `images/rootfs-extra/etc/uci-defaults/95_owlab-base` |
| `96_owlab-fixtures` | `images/rootfs-extra/etc/uci-defaults/96_owlab-fixtures` |
| The fixture profiles, `60-wifi.sh` among them | `images/rootfs-extra/usr/share/owlab/fixtures/` |
| The container entrypoint | `images/rootfs-extra/usr/lib/owlab/entrypoint.sh` |

**Editing anything under `images/` means rebuilding the binary.** The running binary carries its own
copy. Changing the file on disk changes nothing until `go build` runs again. This has produced a false
debugging trail more than once.

### How a command flows

`owlab up`:

1. `config.Find` walks up from the working directory for `owlab.yaml`, so any subdirectory works.
2. `config.Load` parses it and validates. On the way it merges `defaults:` into each router, applies
   the package arithmetic on top of the stock set, and resolves `arch: auto` against the host. After
   this point a `Router` is fully determined: nothing downstream re-derives anything.
3. If any router is a container, owlab checks Docker and identifies the engine. A project whose
   routers are all `fidelity: vm` never touches Docker. Demanding it would be a requirement owlab
   invented.
4. Routers split by tier.
   - Containers have their base image checked first. `resolveBaseImages` sends a manifest `HEAD` to
     the image's registry. It moves a router onto its rootfs tarball when the tag is missing, or when
     the registry does not answer and the download server has the tarball.
   - Containers then go through `compose.Prepare`. It writes `authorized_keys` and a Compose file and
     extracts the build context. It downloads the rootfs tarballs, the out-of-feed packages and the
     compliance bundle of the routers being built. Then come `docker compose build` and `up`.
   - VMs go through `qemu.New(...).Start` and, if the disk is new, `Provision`.
5. owlab polls both tiers on their forwarded HTTP port until LuCI answers. A router is "up" when
   uhttpd serves, not when the process starts: procd has to run the whole boot sequence and apply the
   uci-defaults first.

#### `Prepare` has two halves

Which half a command needs matters.

| Half | Does | Needs network |
|---|---|---|
| `compose.Render` | writes the Compose file and touches nothing else; no wipe | no |
| `compose.Materialize` | wipes and rewrites the build context, then downloads what goes in it for the routers it is given, and only those | yes |

`up` and `context` need both. `down`, `logs` and `up --no-build` need only `Render`. That is not a
micro-optimisation. Without it, stopping a router or reading its log would fail on a machine with no
network, over files it is not about to build with.

The Dockerfile copies `extra/<id>/` and `compliance/<id>/` of the router it builds. So a router's
layers hash its own files and do not change with the selection.

#### What `up --no-build` checks

`up --no-build` starts the images the engine already holds. It skips the registry probe,
`Materialize` and `compose build`.

Every image `compose build` makes carries an `org.owlab.inputs` label, `compose.Inputs`. It is a
digest of:

- this owlab's version and VCS revision;
- the embedded build context;
- the build args and the platform;
- the release and its rootfs tarball URL;
- the `extra_packages` URLs.

Two builds of one commit with different uncommitted changes share it. `up --no-build` starts an image
only when its label matches that digest for the config it reads now.

What the feeds served during the build is not in the digest, and cannot be. The same inputs built a
week apart install that week's packages.

### The seam between the tiers

Everything above the transport is written once. `cmd/owlab/transport.go` defines what differs per
router:

```go
type tier interface {
	Stream() (stream, error)                                  // run a script; stdin, stdout, stderr apart
	Interactive(ctx context.Context, command ...string) error // hand over the terminal
	State(ctx context.Context) string                         // one word, for `owlab status`
}
```

There are two implementations: `containerTier` over `docker exec` and `vmTier` over ssh. Sync,
`post_sync`, `install`, the LuCI cache drop, `shell` and the status table are written against the
interface. Only the two implementations know which kind of router is on the other end.

#### Why `Stream` takes a reader

`Stream` takes stdin as a reader and keeps stdout and stderr apart. `owlab exec` passes its own stdin
through, and that stdin has no length (`yes | owlab exec r -- head -1`).

The byte-buffer `sync.Exec` that sync, install and the assertions use is built from `Stream` in
`execFor`. So there is still one way each tier runs a script.

A tier adds `docker exec -i` (or gives ssh a stdin) only when stdin is non-nil. It never allocates a
tty: a tty rewrites LF as CRLF in piped data.

#### Why batch operations are not on the interface

`build`, `up`, `down` and `logs` are deliberately **not** on it. They use `splitTiers` and handle each
tier on its own terms.

<details>
<summary>The reasoning</summary>

Compose acts on a whole project at once and gets its network teardown from doing so. The VM tier
spawns one host process per router. Folding `build`, `up`, `down` and `logs` into a per-router shape
would turn one build into N. It would also cost `down` the thing that makes it a teardown.

</details>

#### Why file transfer is a tar on stdin

File transfer uses a tar on stdin, not `docker cp` or `scp`. It is the single operation both
transports have. dropbear ships no sftp-server, so `scp` against a VM would need a package
installed for it.

### What is generated, and where

```
<project>/.owlab/
  compose.yaml        generated on every run; never edit
  context/            the embedded build context, extracted
  cache/              out-of-feed packages and compliance files
  authorized_keys     the developer's public keys, concatenated
  vm/<id>/            one directory per VM router — disks, console, pidfile
```

owlab adds `.owlab/` to gitignore itself on first use.

VM boot images do **not** live here. They go in the user cache directory
(`~/Library/Caches/owlab/images` on macOS). They are a quarter of a gigabyte each, and every project
on the machine that boots the same release shares them.

Per-router writes go to a qcow2 overlay in the project. So the cached image stays read-only, and one
download serves everything.

### Two rules that shaped the design

**Nothing addresses a router by its bridge IP.** Published ports only. A container's bridge subnet is
routable from the host on native Linux and inside WSL2. It is not routable on Docker Desktop for Mac or
Windows. So `http://localhost:<port>` is the only form that works identically everywhere.

**Capability is probed, never inferred from the platform.** Each of these has been guessed wrong from
the OS at some point in this project's history:

- which accelerator QEMU has;
- whether the kernel can make a flowtable;
- whether the engine forwards UDP.

Homebrew's `qemu-system-x86_64` on Apple Silicon reports `tcg` only, while `qemu-system-aarch64` from
the same bottle has `hvf`. No rule about macOS gets that right.

---

## Tiers

There are two: `basic` and `vm`. You choose one per router. Most LuCI work needs nothing more than
the cheap one, and the expensive one is not free.

| | `basic` | `vm` |
|---|---|---|
| what it is | a container: stock rootfs, procd as PID 1 | QEMU as an ordinary host process, real OpenWrt kernel |
| kernel modules | do not load | load |
| real radios | no | two `mac80211_hwsim` phys by default |
| real overlay | no | squashfs `/rom` plus a writable overlay |
| state | — | the disk is the router |
| cost | cheap | about 100 s the first time, about 18 s per start after |

### `basic` — a container

A stock rootfs for the release, with procd as PID 1.

`CMD ["/sbin/init"]` is not negotiable. Running `uhttpd` directly instead of the init system makes
LuCI crash with `left-hand side expression is null` at `runtime.uc:133` (openwrt/luci#8726). The
dispatcher reads board data from procd over ubus, and there is no shortcut. The init system is the
product.

What works:

- ubus, rpcd, uhttpd, netifd, uci, the whole of LuCI;
- `apk` and `opkg`;
- nftables and fw4 (measured: 311 rules on OpenWrt, 324 on ImmortalWrt);
- whatever netfilter features the *engine's* kernel happens to have. tproxy does work on OrbStack and
  on most Linux hosts.

What does not: loading kernel modules, `sysupgrade`, real radios, DSA.

Two container-specific behaviours bite hardest: procd's jails, and outbound UDP. See
[Troubleshooting](troubleshooting.md).

### `vm` — a real OpenWrt kernel

QEMU as an ordinary host process, not inside a container.

That is the whole design. Nested QEMU needs `/dev/kvm` passed into the container. Docker Desktop grants
it on neither macOS nor Windows, which are exactly the hosts that need this tier most.

Run natively, the problem inverts. On Apple Silicon the guest is aarch64 and the accelerator is hvf. A
router boots in nine seconds with no nested virtualisation, no minimum macOS version and no special
hardware.

What it adds:

- **Kernel modules load.** `apk add kmod-nft-tproxy` and `lsmod` shows `nft_tproxy`. This is the
  reason the tier exists.
- **Real radios.** Two `mac80211_hwsim` phys by default. hostapd runs on them, and `iwinfo` reports
  signal, noise and tx power. LuCI's wireless pages show the router rather than its config.
- **A real overlay.** squashfs `/rom` plus a writable overlay, so `firstboot` and `jffs2reset` behave
  as they do on hardware.
- **State that survives.** The disk is the router: `owlab down` is a shutdown, `--rebuild` is the
  factory reset.

What it costs: about 100 seconds the first time (download, extroot, packages, two reboots). After that,
about 18 seconds on every start.

See [The VM tier](#the-vm-tier) below for how it is put together.

### The tier that was removed

An earlier design had a third tier, `full`: a container given real `mac80211_hwsim` phys moved in from
the *host* kernel. It was dropped. It was unavailable on the machines that most need something better
than a container. And once the VM tier had real radios on every host, `full` was a second, worse way
to get the same thing.

<details>
<summary>Why <code>full</code> could not work on most hosts</summary>

It required a Linux kernel the developer controls.

| Host | Could it? |
|---|---|
| native Linux, WSL2 | yes |
| Colima, Lima | after installing `linux-modules-extra` |
| Docker Desktop for macOS | never: its LinuxKit kernel is built with `CONFIG_CFG80211` unset |
| OrbStack | no: its kernel has no wireless stack and no supported way to add modules |

Every candidate (`mac80211_hwsim`, `virt_wifi`, `vwifi`, `wmediumd`) sits on top of cfg80211.
`CONFIG_IKHEADERS` is unset too, so building the module in place is not a way out either. The only
project that ever did this has been dead since 2019.

So the tier would have been unavailable precisely on the machines that most need something better
than a container. Once the VM tier had real radios on *every* host, what remained was a second, worse
way to get the same thing on a subset of hosts. Two tiers that differ in kind beat three where one is
a qualified version of another.

</details>

---

## Networking

### Reaching a router

Published ports, always: `http://localhost:<http>` and `ssh -p <ssh> root@localhost`.

A container's bridge subnet is routable from the host on native Linux and inside WSL2. It is not
routable on Docker Desktop for Mac or Windows, and `--network host` and `macvlan` do not work there
either. So nothing in owlab addresses a router by its bridge IP, and the same command works on every
host.

For a VM the ports are QEMU `hostfwd` entries into the guest's LAN side. The order of the two NICs is
the whole design there; see [The VM tier](#the-vm-tier) below.

### `br-lan`

The container's lan is a bridge named `br-lan`, with `eth0` as its only port.

It is there because packages expect it. Every OpenWrt target with more than one ethernet port builds
`br-lan`, so packages and scripts refer to it by name. An interface list in a package's own config
usually defaults to it. So do a great many firewall snippets, hotplug scripts and forum recipes.

On a router whose lan was a bare `eth0`, all of those applied to nothing. There was no error, only a
feature that did not work.

The entrypoint sets it up before procd starts:

- **The bridge takes `eth0`'s MAC.** It is the only and therefore lowest-MAC port. So the engine's veth
  peer keeps delivering to the address it always did, and nothing on the host side notices.
- **`eth0`'s own address is flushed.** A bridge port cannot carry L3. Leaving the address there gives
  the kernel a connected route out of an interface that no longer transmits.
- **`OWLAB_LAN_BRIDGE=0` falls back to lan on `eth0`.** This is the one interface that must not break.
  An engine that dislikes bridging its veth should be one variable away from working, not one image
  rebuild.

### Why the entrypoint writes the network config at all

The entrypoint writes the network config because of timing. That is why it is not a `uci-defaults`
script.

By the time `/etc/uci-defaults/*` run, `eth0` is down and flushed. A uci-default cannot read the
address the engine assigned, because there is nothing left to read.

`/bin/config_generate` then writes its own config from `/etc/board.json`. On x86 and armsr that means
`br-lan` with a hardcoded 192.168.1.1. netifd applies it and the engine's address is gone. The
container is unreachable, with no console to fix it from.

<details>
<summary>How the entrypoint disarms <code>config_generate</code>, and why it polls</summary>

`config_generate`'s guard is `[ -s /etc/config/network -a -s /etc/config/system ] && exit 0`. So
writing *both* files in the entrypoint is what disarms it. That is why `/etc/config/system` ships in
the image even though nothing in it is interesting: an empty or missing one re-arms the generator.

The address is polled for up to ten seconds rather than sampled once. Engines start the container's
process and configure its interface concurrently. So `eth0` can still be bare when PID 1 begins,
reliably so on a restart or a loaded machine.

Sampling once and finding nothing is not harmless. The config would not be written, `config_generate`
would fire, and the box would come up on 192.168.1.1 with no route to it.

</details>

### DNS

`/etc/resolv.conf` keeps the engine's resolver first and appends public ones behind it. Neither half
is optional, because the engines disagree about which one works:

| Engine | Resolver it hands out | What breaks without the other half |
|---|---|---|
| Docker Desktop, WSL2 | embedded `127.0.0.11`, implemented with NAT rules that OpenWrt's own boot flushes | it stops answering partway through the boot, so something else has to be in the list |
| OrbStack | one that keeps working after boot | direct UDP/53 to the internet is not forwarded at all, so the public resolvers alone resolve nothing |

Overwriting the file the way the Docker Desktop case wants leaves an OrbStack container with no DNS.
Reading the file before overwriting it is what makes one image work on both.

`options timeout:1` is there so a resolver that died mid-boot costs one second rather than five.

### Outbound UDP port 53

Some engines do not forward it at all. Measured on OrbStack:

```console
$ owlab exec pk -- 'dig +short github.com @127.0.0.11; dig +short github.com @8.8.8.8'
140.82.121.4
;; communications error to 8.8.8.8#53: timed out
```

A plain `alpine` container on the same engine behaves identically. So this is the engine and not
OpenWrt, and owlab cannot fix it. TCP is unaffected: `https://github.com` answers 200.

It matters because a package that ships its own resolver then resolves nothing and blames the
destination. One such daemon exits with

```
initial rule-set: ... lookup github.com: context deadline exceeded
```

on a router where GitHub over https is fine.

The fix is always the same. Point the package's bootstrap resolver at the engine's own, which does
answer, and run DoH or DoT over TCP on top of it. `owlab doctor` probes for this through a running
router and names the resolver to use.

### Flow offloading

ImmortalWrt enables it by default (`firewall.@defaults[0].flow_offloading` and `_hw`). OpenWrt does
not ship the key.

The rule needs a flowtable, and a flowtable needs `nf_flow_table` in the *running* kernel. Where it is
missing, nft rejects the entire ruleset:

```
flowtable ft { ... Error: Could not process rule: No such file or directory
meta l4proto { tcp, udp } flow offload @ft;
```

What survives is the base chains with `policy drop` and no jumps into any zone. The symptom is a router
that completes a TCP handshake and then resets. Meanwhile uhttpd is up and listening on `0.0.0.0:80`.
It gets reported as "connection reset by peer" against a box that looks entirely healthy.

`95_owlab-base` probes rather than assumes. It creates a flowtable in a scratch table and deletes it,
and turns the setting off only if that fails. A host that *can* offload keeps the distribution's own
configuration. A VM, whose kernel is OpenWrt's own, keeps it too.

The `modprobe` before the probe is needed. OpenWrt ships no `modules.alias`, so the kernel cannot
autoload the module by the alias nft asks for. Without the `modprobe`, the probe failed on a router
that was perfectly capable.

### Services that get switched off

`95_owlab-base` stops and disables two services. Each was measured taking a box down:

- **mwan3** watches the WAN, decides a dummy interface is dead, and installs ip rules that blackhole
  everything (`2061: from all fwmark 0x3d00/0x3f00 blackhole`). The symptom is a router that answers
  on LuCI while every outbound connection hangs: `apk add` sits there forever.
- **watchcat** reboots or reconnects on a schedule when it thinks the network is down, which in here
  it always does.

They are stopped rather than uninstalled, so their LuCI pages stay.

The firewall is deliberately not in this list. fw4 works, and turning it off would hide a whole class
of real behaviour.

---

## Packages

### The stock set

A router with no `packages:` at all is not a bare rootfs. It is what a shipping OpenWrt device has
installed.

The list is OpenWrt's own `DEFAULT_PACKAGES` and `DEFAULT_PACKAGES.router` from `include/target.mk`.
On top of that comes what the firmware selector adds for a wireless router with LuCI. It was checked
against a real device's package list (netcore n60 pro, mediatek/filogic). It lives in
`internal/config/packages.go`.

Most of it is already in an upstream rootfs tarball, so installing it is usually a no-op. Naming it
anyway makes the set a property of owlab, not of whichever tarball a distribution happens to publish.
So a fork with a thinner rootfs still gives the same router.

There are two deliberate departures from the device's own list.

**`libustream-mbedtls` is left out**, although upstream's list has it. The TLS backend is a build-time
choice, not a fixed name. Every image already carries the variant it was built with, and asking
ImmortalWrt 24.10 for the mbedtls one fails outright.

**Nothing board-specific is included.** `fitblk`, `uboot-envtools`, `kmod-usb3`, `kmod-leds-gpio`,
`kmod-gpio-button-hotplug`, `kmod-crypto-hw-safexcel`, `kmod-mt7915e` and the mt7986 firmware
describe one device's flash layout, bootloader, LEDs and radio silicon. None can function without that
hardware, and a container has no kernel of its own to load them into. If a package under development
needs one present on disk to resolve a dependency, add it per project.

### Arithmetic

The stock set is the *base* for `+`/`-`, not a fallback for an empty list. That is what makes
`packages: ["-ppp"]` mean what it reads as.

```yaml
defaults:
  packages: ["-ppp", "-ppp-mod-pppoe"]   # applied to the stock set
routers:
  - id: a
    packages: ["+luci-app-sqm"]          # applied to the result of the above
```

A list with no prefix at all replaces the set outright. An explicit list is someone saying they know
what they want.

#### One call, then fallbacks

The set is installed in one call. Measured on luci-theme-footstrap's 74 names on 24.10.8:

| Run | One call per package | One call |
|---|---|---|
| first install | 81 s | 58 s |
| second pass, everything installed | 27 s | under one |

If that call fails, owlab drops a name the feeds lack, and on opkg a name an installed package
provides. It asks for the rest again in one call. Only when that fails too is each package installed
on its own, to name the ones that do not install.

#### Failures are reported, not fatal

Feeds differ between releases and between a fork and upstream. So a name missing on 24.10 costs that
one package rather than the whole image:

```
owlab: skip (not in this feed): luci-app-openvpn
```

That line is for a name the feed does not carry. The 2026-10-20 snapshot feed, for example, did not
carry `luci-app-openvpn`.

Any other refusal, such as a file clash or a dependency the feed lacks, prints the package manager's
own output. Then it prints `owlab: FAILED to install <name>`, and the build carries on without it.

Carrying on is not the same as passing it over. The name goes into `/etc/owlab/packages-failed`.
`owlab up` reads that file back off the running router and reports it before exiting 1, as it does for
`extra_packages` below. Ending the layer instead would cancel every other router in the build.

Two kinds of failure get a closer look, because each would otherwise be cached:

- **A download is asked again**: 4 attempts, 2, 4 and 8 s apart, the schedule of `internal/netx`. Once
  the layer exits 0, buildkit keeps it. Every `owlab up` after a dropped fetch would hand back the same
  short router.
- **A name not found after a refresh in which a feed did not answer is a failure**, because that feed
  may carry it. A kmods index alone is the exception. A snapshot image older than the feed's kernel
  window loses it on every build, and it carries only `kmod-*` packages.

### `extra_packages` — anything not in a feed

Your own package, or anything published on GitHub Releases.

Each entry takes two URLs, because the two package managers do not share a naming scheme. The same
release publishes `luci-theme-footstrap-0.11.5-r1.apk` and `luci-theme-footstrap_0.11.5-r1_all.ipk`.

Five things shape how these are installed. Each of them was a bug first.

**Downloaded on the host.** A stock OpenWrt rootfs has no `curl`, and its busybox `wget` cannot do
TLS. So an in-image download would depend on which packages the user happened to choose. On the host,
a truncated download is also reported as itself. In the image it would be a confusing package-manager
error three layers into a build.

**Order is preserved.** Files are staged numbered (`00-`, `01-`), because the image installs whatever
the glob returns and a glob is alphabetical. These packages depend on each other. `luci-app-example`
requires `example-daemon` and sorts before it, which apk refuses outright with
`unable to select packages`.

**All of them in one command.** Handed the whole set, the package manager resolves among them, and the
ordering stops mattering. The retry below is one file at a time, and it leans on the staged order
again.

**`--force-overwrite`.** Their dependencies routinely replace a file the stock image already owns:

```
check_data_file_clashes: Package dnsmasq-full wants to install file
/etc/init.d/dnsmasq. But that file is already provided by package dnsmasq
```

On a real router that check is a guard worth having. Here the replacement is exactly what was asked
for. ImmortalWrt already ships `dnsmasq-full`. Without this flag, the same config produced a different
router on OpenWrt 24.10 than on the other three.

**A failure here is neither swallowed nor fatal**, unlike a feed name.

- **Not swallowed:** these are named by URL. The developer said "install this file". A build that
  reported success without one would hand them a router quietly missing the thing they are testing
  against.
- **Not fatal:** one unusable file used to kill every other router in the lab. See below.

So the set is installed as one, then one file at a time if that failed. What is still missing is
written to `/etc/owlab/extras-failed`. `owlab up` reads it back off the running router and reports it
under its table before exiting non-zero. `owlab test` fails on it as an `extra_packages` step.

The record is what makes tolerance honest rather than silent. A build log scrolls past, and a router
started from a cached image never printed one at all.

<details>
<summary>Why fatal was dropped (measured 2026-09-04)</summary>

Fatal was the first answer and cost more than it saved. `docker compose build` puts every router in one
buildkit solve. buildkit cancels the solve on the first target that fails. So one unusable file killed
every other router in the lab.

Measured 2026-09-04 on a three-router stand:

- opkg exits 255 on an unsatisfiable dependency;
- apk exits 27 on `unable to select packages`;
- both left `CANCELED` neighbours. The cause is `set -eu`, not the package manager.

Nor is there a knob: neither `docker compose build` nor `docker buildx bake` has a `--keep-going`.

</details>

#### No signature verification

These are installed **without signature verification**. Projects publishing this way usually sign with
usign and ship a `.sig` beside the artifact. Checking it needs their public key, which is exactly the
trust decision a dev container should not make on the user's behalf.

Treat anything installed this way as trusted-by-URL. Pin an exact release rather than a `latest` link.
Do not copy the pattern into anything that ships.

### Feeds

**They are left exactly as the rootfs shipped them.** Stripping the `kmods` line breaks installs, and
rewriting any feed risks breaking the version pin.

#### Why the `kmods` line stays

It is tempting to strip the `kmods` line. It is pinned to an exact kernel build hash, and nothing in it
can ever load, because the running kernel is the engine's. Every widely-copied container recipe strips
it.

Measured, that is wrong. The kmods feed is what resolves the `kmod-*` *dependencies* of ordinary
packages. Without it, `apk add luci-app-sqm` fails with `kmod-ifb (no such package)`.

The modules install as files and never load. That costs a few hundred KB and a complaint from
kmodloader at boot. It is far cheaper than a package manager that cannot install half the feed.

#### Why feeds are never rewritten

Leaving them alone also keeps the version pin correct. apk records hard pins in `/etc/apk/world`
(`base-files=1707~4ccb782af7`). So a 25.12.4 rootfs pointed at the 25.12.5 feed fails every install
with `breaks: world[...]`. The feeds that shipped in the image already name that image's exact point
release, and any rewriting risks breaking it.

#### Pin an exact point release

The practical consequence for a project: **pin an exact point release.**

A snapshot rootfs and a snapshot feed are rebuilt daily and independently. They drift apart within a
day, and a library can move to a new ABI in between. On 2026-10-20 the feed's `ucode-mod-html` needed
a `libucode` the image's `ucode` was not linked against. Installing it by name left LuCI answering 502.

So on a rolling release owlab runs `apk upgrade` after the index refresh and before installing
anything. That brings the whole image up to the feed it is about to install from.

A point release is never upgraded. Its ABI does not move, and a user of it installs without upgrading.

A snapshot router is still not repeatable, and `owlab doctor` warns when a router names `snapshot`.

### apk and opkg

The manager is a property of the distribution, not a function of the version. OpenWrt switched in
25.12. A fork can track 25.12 and still build with opkg (Kwrt does, with
`# CONFIG_USE_APK is not set`).

So the precedence is:

1. what the config says;
2. what the distribution pins;
3. the OpenWrt version rule.

Deriving it from the release number alone would hand such a router apk commands and fail every
install.

---

## The VM tier

A real OpenWrt kernel, booted by QEMU as an ordinary process on the developer's machine.

### Why not QEMU in a container

Because `/dev/kvm` has to be passed into that container. Docker Desktop grants it on neither macOS nor
Windows, which are exactly the hosts that need this tier most. Run natively, the problem inverts:

| host | guest | accelerator |
|---|---|---|
| macOS, Apple Silicon | aarch64 (`armsr/armv8`) | hvf |
| macOS, Intel | x86_64 | hvf |
| Linux | matches host | kvm |
| Windows | matches host | whpx |

On Apple Silicon that is a nine-second boot with no nested virtualisation, no minimum macOS version and
no special hardware.

### Choosing the accelerator

The first question is **whether the guest CPU is the host CPU**, not which accelerator the host has. No
accelerator virtualises a foreign architecture. An x86 router on an ARM laptop is translated
instruction by instruction, roughly an order of magnitude slower. `arch: auto` (the default) already
does the right thing, and owlab warns loudly rather than being quietly slow.

Everything else is probed, never inferred from the OS. Homebrew's `qemu-system-x86_64` on Apple
Silicon reports `tcg` only. It is built without hvf, because hvf cannot run an x86 guest on an ARM CPU.
`qemu-system-aarch64` from the same bottle has hvf. No rule about macOS gets that right.

On Linux, being *built* with kvm is not the same as being *allowed* to use it. `/dev/kvm` is
`root:kvm`. A user outside that group gets a permission error from QEMU well past the point where a
clear message would have helped. owlab opens the device to find out.

owlab passes `-cpu host` only when the accelerator is native. Under translation it would ask QEMU to
emulate whatever this machine happens to be. That is both slower and less reproducible than naming a
model.

### VM images

owlab boots upstream combined disk images: kernel, bootloader and rootfs in one file.

```
openwrt-25.12.5-armsr-armv8-generic-squashfs-combined-efi.img.gz
```

**squashfs, not ext4.** squashfs is what a router runs: a read-only `/rom` with a writable overlay on
top. That is what makes `firstboot`, `jffs2reset` and the whole reset-to-defaults story behave as they
do on hardware. An ext4 image is one flat writable filesystem and quietly makes half of that
untestable.

#### Four targets qualify

The bar is that upstream publishes a *combined* image:

- `x86_64`
- `i386_pentium4`
- `aarch64_generic` (armsr/armv8)
- `arm_cortex-a15_neon-vfpv4` (armsr/armv7)

malta publishes a bare kernel and a separate rootfs. mvebu is a real board with no firmware to boot
under QEMU. For either, owlab would have to invent a boot arrangement upstream never tests. "It boots
but not like the real thing" is the one outcome this tier exists to avoid.

#### Firmware for EFI images

armsr publishes EFI-only images, so aarch64 and armv7 need an edk2 build on the host. owlab searches
for it in this order:

1. `OWLAB_FIRMWARE`, when it is set;
2. beside the QEMU binary, which is right for Homebrew, for a manual build and for a distribution
   package alike;
3. the usual distribution locations, under the usual distribution aliases (`AAVMF_CODE.fd`,
   `QEMU_EFI.fd`).

x86 still publishes a legacy-boot combined image, which SeaBIOS handles with nothing to find.

#### Checksums, not signatures

Images are verified against the published `sha256sums`. That file comes from the same server over the
same TLS connection as the image. So it catches a truncated download or a broken mirror, not a
compromised download server.

Upstream also publishes `sha256sums.asc`. Verifying it needs the release keyring, which is a trust
decision beyond what a dev tool should make on the user's behalf.

### VM disks

There are two, and one of them exists because of a hard limit.

| Disk | What it is |
|---|---|
| `disk.qcow2` | a qcow2 overlay whose backing file is the cached image; the base is never written to, so one download serves every router and every project on the machine |
| `overlay.qcow2` | a second disk; `/overlay` is moved onto it with stock OpenWrt extroot, the same mechanism a real router uses for a USB stick |

The second disk is needed, not a convenience. The overlay in an upstream combined image is fixed at
build time (87 MB on armsr). It **cannot be grown from inside a running system**. The filesystem was
created without a resize inode, so an online `resize2fs` stops after the first block group. Measured,
a 900 MB partition yielded 122 MB.

A separate disk has no such history. It costs one reboot during first provisioning and grows sparsely:
a 2 GB disk occupies only what has been written to it.

`disk: 0` turns it off and leaves the image's own overlay.

### VM networking

Two NICs, and the order is the design.

OpenWrt's `board.d` makes the first ethernet port LAN and the second WAN:

- **LAN** gets the static 192.168.1.1. uhttpd and dropbear accept connections there.
- **WAN** gets DHCP. The firewall drops everything inbound there.

Forwarding the host's ports to the WAN side would produce a router that is up, reachable by nothing,
and blamed on QEMU.

The LAN SLIRP network is given the guest's own subnet:

```
-netdev user,id=lan,net=192.168.1.0/24,host=192.168.1.254,hostfwd=tcp::8090-192.168.1.1:80
```

A `hostfwd` can only reach an address SLIRP believes is on its network. The guest's address is fixed by
`config_generate` before owlab gets a chance to say otherwise.

WAN is a plain SLIRP network. It gives the router a default route and working DNS, so the package
manager can reach the feeds. It is NAT, so nothing on the host's network is exposed.

A `virtio-rng-pci` is attached because OpenWrt's boot waits on entropy: urngd, dropbear host keys, the
overlay. A headless VM has almost nothing to fill the kernel pool from.

### VM lifecycle

QEMU is detached with `-daemonize`, which also writes the pidfile.

Windows has no such flag. There owlab starts the process with
`DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP` and writes the pidfile itself. Both halves of that flag
pair earn their place:

- without the first, closing the terminal delivers `CTRL_CLOSE_EVENT` and takes the router with it;
- without the second, Ctrl-C during `owlab up` interrupts the router rather than the wait.

#### The two-second exit check

Starting rather than running to completion costs the one thing `-daemonize` gives for free: an exit
status. QEMU rejecting an argument looked exactly like a successful boot. It surfaced a minute later as
a wait-for-ssh timeout naming nothing.

So owlab waits two seconds. A QEMU that has exited by then is reported with whatever it wrote to
stderr.

#### Liveness and entropy differ per OS

A pidfile outlives the process that wrote it, so liveness is checked rather than trusted: signal 0 on
unix, `tasklist` on Windows, which has no signals.

The entropy source is the other thing that differs. `rng-random` reads a character device, and
`/dev/urandom` does not exist on Windows, so QEMU exits before the guest starts. `rng-builtin` is the
same virtio-rng to the guest with no host file involved. Windows gets `rng-builtin`.

#### Shutdown and console

`owlab down` asks the *guest* to power itself off over ssh rather than killing QEMU. The overlay is a
real filesystem with real dirty pages. Pulling the plug on every `down` would eventually cost someone
their installed packages. The kill is the fallback for a router wedged badly enough not to answer.

The console is captured to a file (`-serial file:`), and that is what `owlab logs` shows. It is the
same stream a container's log is, from the first kernel line onward. Interacting with the router
happens over ssh, where a shell has a real terminal.

### Provisioning

The disk is the cache. Provisioning runs once, on a new disk. The marker written at the end is what
makes every later `owlab up` a plain boot.

The order matters, and every step in it was moved there for a measured reason:

1. **extroot**, then reboot. Everything after this writes to the filesystem it moves.
2. **Packages**, then out-of-feed packages.
3. **Radios**: install `kmod-mac80211-hwsim` and write `/etc/modules.d/mac80211-hwsim` with the
   requested count.
4. **Reboot.** Needed, not cosmetic: see below.
5. **Overlay and fixtures**, then `wifi up`.

### Why the reboot before the overlay

Everything above was installed onto a system that had already finished booting. A good deal of
OpenWrt reads its inputs exactly once, at boot. Each of these was measured on a router that looked
fully provisioned, and not one of them reports anything that names the cause:

| Component | Reads at boot | Without the reboot |
|---|---|---|
| **procd** | the services a package enabled, `wpad` among them | it never notices one that appeared afterwards |
| **netifd** | `/lib/netifd/wireless/*` | `wifi-scripts` installed later leaves `wifi up` reporting `Command failed: Not found` against a netifd with no wireless support loaded |
| **ubusd** | `/usr/share/acl.d/` | wpad's ACL is what lets hostapd, which runs as the unprivileged `network` user, publish its ubus object at all; without that object the wireless setup script waits forever and the radios stay `pending: true` |
| **kmodloader** | `/etc/modules.d/*` | the hwsim radios come into existence only at boot |

The reboot comes *before* the overlay because the fixtures have to see the result. `60-wifi.sh` asks
whether real phys are present and configures them if they are. One reboot earlier it would find none
and write the invented radios a container gets.

### Radios

Two `mac80211_hwsim` phys by default, and they are real:

```console
$ owlab exec real -- iwinfo
phy0-ap0  ESSID: "owlab"
          Mode: Master  Channel: 6 (2.437 GHz)  HT Mode: HT20
          Tx-Power: 20 dBm  Link Quality: 70/70
          Encryption: WPA-PSK (CCMP)
phy1-ap0  ESSID: "owlab"
          Mode: Master  Channel: 36 (5.180 GHz)  width: 80 MHz
          Tx-Power: 23 dBm  Link Quality: 70/70
```

The radios come from `/etc/modules.d/` rather than a `modprobe`. They have to exist before netifd looks
for them, and that is how OpenWrt says so. The count is the module's own parameter. So two radios are
two phys, a dual-band router, rather than one phy pretending to be two bands.

#### Bands and channels are assigned

Bands and channels are assigned rather than detected. `wifi config` puts every hwsim radio on 6 GHz
with channel auto. The simulated phy advertises every band, and 6 GHz is the highest. Under the default
regulatory domain that comes up on channel 0 with no width the UI can name.

So owlab assigns them:

- the first radio gets 2.4 GHz channel 6;
- the second gets 5 GHz channel 36;
- a country is set. With `00` the regulatory domain permits almost nothing, and 5 GHz channels come up
  disabled.
