# Internals

[Русская версия](internals.ru.md)

How owlab is put together and why it behaves the way it does. Nothing here is
needed to use it — start with the [runbook](runbook.md).

## Architecture

owlab is a single Go binary. It carries the image build context inside itself,
generates a Compose file for container routers, and spawns QEMU directly for VM
ones. There is no daemon and no state outside the project directory and one
image cache.

### The packages

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

The last two exist because both tiers do the same thing by different means, and
a difference between them would be a bug nobody could see. `pkgmgr` knows that
`--allow-untrusted` is apk's alone and that a missing feed package should cost
that package rather than the router; `tarx` knows the header rules — GNU format,
because a LuCI tree reaches paths past the 100 bytes USTAR allows.

`images/` is embedded with `//go:embed all:images`. A developer who runs
`owlab up` inside their own LuCI package has no copy of this repository and
should not need one. The `all:` prefix is required — the overlay contains
`/etc/uci-defaults` files, and `go:embed` silently skips names starting with
`_` or `.` without it.

**Editing anything under `images/` means rebuilding the binary.** The running
binary carries its own copy; changing the file on disk changes nothing until
`go build` runs again. This has produced a false debugging trail more than
once.

### How a command flows

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
   writes `authorized_keys` and a Compose file, extracts the build context, and
   downloads the out-of-feed packages and the compliance bundle — and then
   `docker compose build` and `up`. VMs go through `qemu.New(...).Start` and,
   if the disk is new, `Provision`.
5. Both tiers are then polled on their forwarded HTTP port until LuCI answers.
   A router is "up" when uhttpd serves, not when the process starts: procd has
   to run the whole boot sequence and apply the uci-defaults first.

`Prepare` is two halves, and which one a command needs matters:

- `compose.Render` writes the Compose file and touches nothing else. No
  network, no wipe.
- `compose.Materialize` wipes and rewrites the build context, then downloads
  what goes in it.

`up` and `context` need both. `down` and `logs` need only the first, and that
is not a micro-optimisation: stopping a router or reading its log would
otherwise fail on a machine with no network, over files it is not about to
build with.

### The seam between the tiers

Everything above the transport is written once. `cmd/owlab/transport.go`
defines what differs per router:

```go
type tier interface {
	Exec() (sync.Exec, error)                                 // run a script, maybe with a tar on stdin
	Interactive(ctx context.Context, command ...string) error // hand over the terminal
	State(ctx context.Context) string                         // one word, for `owlab status`
}
```

with a `containerTier` over `docker exec` and a `vmTier` over ssh. Sync,
`post_sync`, `install`, the LuCI cache drop, `shell` and the status table are
written against that interface, and only the two implementations know which
kind of router is on the other end.

The batch operations are deliberately **not** on it. Compose acts on a whole
project at once and gets its network teardown from doing so, while the VM tier
spawns one host process per router; folding `build`, `up`, `down` and `logs`
into a per-router shape would turn one build into N and cost `down` the thing
that makes it a teardown. Those four use `splitTiers` and handle each tier on
its own terms.

That is also why file transfer is a tar on stdin rather than `docker cp` or
`scp`: it is the single operation both transports have. dropbear ships no
sftp-server, so `scp` against a VM would need a package installed for it.

### What is generated, and where

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

### Two rules that shaped the design

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

---

## Tiers

Two: `basic` and `vm`. Chosen per router, because most LuCI work needs nothing
more than the cheap one and the expensive one is not free.

### `basic` — a container

A stock rootfs for the release, with procd as PID 1.

`CMD ["/sbin/init"]` is not negotiable. Running `uhttpd` directly instead of
the init system makes LuCI crash with `left-hand side expression is null` at
`runtime.uc:133` (openwrt/luci#8726) — the dispatcher reads board data from
procd over ubus, and there is no shortcut. The init system is the product.

What works: ubus, rpcd, uhttpd, netifd, uci, the whole of LuCI, `apk` and
`opkg`, nftables and fw4 (measured: 311 rules on OpenWrt, 324 on ImmortalWrt),
and whatever netfilter features the *engine's* kernel happens to have — tproxy
does work on OrbStack and on most Linux hosts.

What does not: loading kernel modules, `sysupgrade`, real radios, DSA. See
[Troubleshooting](troubleshooting.md) for the two container-specific behaviours that
bite hardest (procd's jails, and outbound UDP).

### `vm` — a real OpenWrt kernel

QEMU as an ordinary host process, not inside a container.

That is the whole design. Nested QEMU needs `/dev/kvm` passed into the
container, which Docker Desktop grants on neither macOS nor Windows — exactly
the hosts that need this tier most. Run natively the problem inverts: on Apple
Silicon the guest is aarch64, the accelerator is hvf, and a router boots in
nine seconds with no nested virtualisation, no minimum macOS version and no
special hardware.

What it adds:

- **Kernel modules load.** `apk add kmod-nft-tproxy` and `lsmod` shows
  `nft_tproxy`. This is the reason the tier exists.
- **Real radios.** Two `mac80211_hwsim` phys by default; hostapd runs on them,
  `iwinfo` reports signal, noise and tx power, LuCI's wireless pages show the
  router rather than its config.
- **A real overlay.** squashfs `/rom` plus a writable overlay, so `firstboot`
  and `jffs2reset` behave as they do on hardware.
- **State that survives.** The disk is the router: `owlab down` is a shutdown,
  `--rebuild` is the factory reset.

What it costs: about 100 seconds the first time (download, extroot, packages,
two reboots) and about 18 seconds on every start after that.

See [The VM tier](#the-vm-tier) below for how it is put together.

### The tier that was removed

An earlier design had a third, `full`: a container given real
`mac80211_hwsim` phys moved in from the *host* kernel. It was dropped, and the
reasoning is worth keeping.

It required a Linux kernel the developer controls. Native Linux and WSL2 could
do it; Colima and Lima could after installing `linux-modules-extra`. Docker
Desktop for macOS never could — its LinuxKit kernel is built with
`CONFIG_CFG80211` unset, and every candidate (`mac80211_hwsim`, `virt_wifi`,
`vwifi`, `wmediumd`) sits on top of cfg80211. `CONFIG_IKHEADERS` is unset too,
so building the module in place is not a way out either. The only project that
ever did this has been dead since 2019. OrbStack's kernel has no wireless stack
and no supported way to add modules.

So the tier would have been unavailable precisely on the machines that most
need something better than a container — and once the VM tier had real radios
on *every* host, what remained was a second, worse way to get the same thing on
a subset of hosts. Two tiers that differ in kind beats three where one is a
qualified version of another.

---

## Networking

### Reaching a router

Published ports, always. `http://localhost:<http>` and
`ssh -p <ssh> root@localhost`.

A container's bridge subnet is routable from the host on native Linux and
inside WSL2, but not on Docker Desktop for Mac or Windows. `--network host` and
`macvlan` do not work there either. So nothing in owlab addresses a router by
its bridge IP, and the same command works on every host.

For a VM the ports are QEMU `hostfwd` entries into the guest's LAN side. The
order of the two NICs is the whole design there; see
[The VM tier](#the-vm-tier) below.

### `br-lan`

The container's lan is a bridge named `br-lan`, with `eth0` as its only port.

Not decoration. Every OpenWrt target with more than one ethernet port builds
`br-lan`, so packages and scripts refer to it by name: podkop's
`source_network_interfaces` defaults to it, and so do a great many firewall
snippets, hotplug scripts and forum recipes. On a router whose lan was a bare
`eth0` all of those applied to nothing — no error, just a feature that did not
work.

How it is done, in the entrypoint before procd starts:

- The bridge takes `eth0`'s MAC, being its only and therefore lowest-MAC port,
  so the engine's veth peer keeps delivering to the address it always did and
  nothing on the host side notices.
- `eth0`'s own address is flushed. A bridge port cannot carry L3, and leaving
  the address there gives the kernel a connected route out of an interface that
  no longer transmits.
- `OWLAB_LAN_BRIDGE=0` falls back to lan on `eth0`. This is the one interface
  that must not break, and an engine that dislikes bridging its veth should be
  one variable away from working rather than one image rebuild.

### Why the entrypoint writes the network config at all

The timing is the point, and it is why this is not a `uci-defaults` script.

By the time `/etc/uci-defaults/*` run, `eth0` is down and flushed — a
uci-default cannot read the address the engine assigned, because there is
nothing left to read. And `/bin/config_generate` will then write its own config
from `/etc/board.json`, which on x86 and armsr means `br-lan` with a hardcoded
192.168.1.1. netifd applies it, the engine's address is gone, and the container
is unreachable with no console to fix it from.

`config_generate`'s guard is
`[ -s /etc/config/network -a -s /etc/config/system ] && exit 0`, so writing
*both* files in the entrypoint is what disarms it. That is why
`/etc/config/system` ships in the image even though nothing in it is
interesting: an empty or missing one re-arms the generator.

The address is polled for up to ten seconds rather than sampled once. Engines
start the container's process and configure its interface concurrently, so
`eth0` can still be bare when PID 1 begins — reliably so on a restart or a
loaded machine. Sampling once and finding nothing is not harmless: the config
would not be written, `config_generate` would fire, and the box would come up
on 192.168.1.1 with no route to it.

### DNS

`/etc/resolv.conf` keeps the engine's resolver first and appends public ones
behind it. Neither half is optional, because the engines disagree about which
one works:

- Docker Desktop and WSL2 hand out the embedded resolver `127.0.0.11`, which is
  implemented with NAT rules that OpenWrt's own boot flushes. It stops
  answering partway through the boot, so something else has to be in the list.
- OrbStack hands out a resolver that keeps working after boot — and there,
  direct UDP/53 to the internet is not forwarded at all, so the public
  resolvers alone resolve nothing. Overwriting the file the way the Docker
  Desktop case wants leaves the container with no DNS.

Reading the file before overwriting it is what makes one image work on both.
`options timeout:1` is there so a resolver that died mid-boot costs one second
rather than five.

### Outbound UDP port 53

Some engines do not forward it at all. Measured on OrbStack:

```console
$ owlab exec pk -- 'dig +short github.com @127.0.0.11; dig +short github.com @8.8.8.8'
140.82.121.4
;; communications error to 8.8.8.8#53: timed out
```

A plain `alpine` container on the same engine behaves identically, so this is
the engine and not OpenWrt, and owlab cannot fix it. TCP is unaffected —
`https://github.com` answers 200.

It matters because a package that ships its own resolver then resolves nothing
and blames the destination. podkop's sing-box exits with

```
initial rule-set: ... lookup github.com: context deadline exceeded
```

on a router where GitHub over https is fine. The fix is always the same: point
the package's bootstrap resolver at the engine's own, which does answer, and
run DoH or DoT over TCP on top of it. `owlab doctor` probes for this through a
running router and names the resolver to use.

### Flow offloading

ImmortalWrt enables it by default (`firewall.@defaults[0].flow_offloading` and
`_hw`); OpenWrt does not ship the key. The rule needs a flowtable, and a
flowtable needs `nf_flow_table` in the *running* kernel. Where it is missing,
nft rejects the entire ruleset:

```
flowtable ft { ... Error: Could not process rule: No such file or directory
meta l4proto { tcp, udp } flow offload @ft;
```

What survives is the base chains with `policy drop` and no jumps into any zone.
The symptom is a router that completes a TCP handshake and then resets, while
uhttpd is up and listening on `0.0.0.0:80` — reported as "connection reset by
peer" against a box that looks entirely healthy.

`95_owlab-base` probes rather than assumes: it creates a flowtable in a scratch
table and deletes it, and turns the setting off only if that fails. A host that
*can* offload keeps the distribution's own configuration, and a VM — whose
kernel is OpenWrt's own — keeps it too.

The `modprobe` before the probe is not decoration. OpenWrt ships no
`modules.alias`, so the kernel cannot autoload the module by the alias nft asks
for, and without it the probe failed on a router that was perfectly capable.

### Services that get switched off

`95_owlab-base` stops and disables two, and each was measured taking a box
down:

- **mwan3** watches the WAN, decides a dummy interface is dead, and installs
  ip rules that blackhole everything (`2061: from all fwmark 0x3d00/0x3f00
  blackhole`). The symptom is a router that answers on LuCI while every
  outbound connection hangs — `apk add` sits there forever.
- **watchcat** reboots or reconnects on a schedule when it thinks the network
  is down, which in here it always does.

They are stopped rather than uninstalled, so their LuCI pages stay. The
firewall is deliberately not in this list: fw4 works, and turning it off would
hide a whole class of real behaviour.

---

## Packages

### The stock set

A router with no `packages:` at all is not a bare rootfs — it is what a
shipping OpenWrt device has installed. The list is OpenWrt's own
`DEFAULT_PACKAGES` and `DEFAULT_PACKAGES.router` from `include/target.mk`, plus
what the firmware selector adds for a wireless router with LuCI, checked
against a real device's package list (netcore n60 pro, mediatek/filogic).

It lives in `internal/config/packages.go`.

Most of it is already in an upstream rootfs tarball, so installing it is
usually a no-op. Naming it anyway makes the set a property of owlab rather than
of whichever tarball a distribution happens to publish, so a fork with a
thinner rootfs still gives the same router.

Two deliberate departures from the device's own list:

**`libustream-mbedtls` is left out** although upstream's list has it. The TLS
backend is a build-time choice, not a fixed name — every image already carries
the variant it was built with, and asking ImmortalWrt 24.10 for the mbedtls one
fails outright.

**Nothing board-specific is included**: `fitblk`, `uboot-envtools`,
`kmod-usb3`, `kmod-leds-gpio`, `kmod-gpio-button-hotplug`,
`kmod-crypto-hw-safexcel`, `kmod-mt7915e` and the mt7986 firmware describe one
device's flash layout, bootloader, LEDs and radio silicon. None can function
without that hardware, and a container has no kernel of its own to load them
into. Add one per project if a package under development needs it present on
disk to resolve a dependency.

### Arithmetic

The stock set is the *base* for `+`/`-`, not a fallback for an empty list. That
is what makes `packages: ["-ppp"]` mean what it reads as.

```yaml
defaults:
  packages: ["-ppp", "-ppp-mod-pppoe"]   # applied to the stock set
routers:
  - id: a
    packages: ["+luci-app-sqm"]          # applied to the result of the above
```

A list with no prefix at all replaces the set outright — an explicit list is
someone saying they know what they want.

Failures are reported, not fatal. Feeds differ between releases and between a
fork and upstream, so a name missing on 24.10 costs that one package rather
than the whole image:

```
owlab: skip (not in this feed): dnsmasq
```

That exact line appears on ImmortalWrt 24.10, which ships `dnsmasq-full` and
refuses the plain variant. It is the intended shape of the message.

### `extra_packages` — anything not in a feed

Your own package, or anything published on GitHub Releases. Two URLs, because
the two package managers do not share a naming scheme: the same release
publishes `luci-theme-footstrap-0.11.5-r1.apk` and
`luci-theme-footstrap_0.11.5-r1_all.ipk`.

Four things about how these are installed, each of which was a bug first.

**Downloaded on the host.** A stock OpenWrt rootfs has no `curl` and its
busybox `wget` cannot do TLS, so an in-image download would depend on which
packages the user happened to choose. Doing it on the host also means a
truncated download is reported as itself rather than as a confusing
package-manager error three layers into a build.

**Order is preserved.** Files are staged numbered (`00-`, `01-`) because the
image installs whatever the glob returns and a glob is alphabetical. These
packages depend on each other — `luci-app-podkop` requires `podkop`, and sorts
before it — which apk refuses outright with `unable to select packages`.

**All of them in one command.** Handed the whole set, the package manager
resolves among them and the ordering stops mattering at all.

**`--force-overwrite`.** Their dependencies routinely replace a file the stock
image already owns:

```
check_data_file_clashes: Package dnsmasq-full wants to install file
/etc/init.d/dnsmasq. But that file is already provided by package dnsmasq
```

On a real router that check is a guard worth having; here the replacement is
exactly what was asked for. ImmortalWrt already ships `dnsmasq-full`, so
without this the same config produced a different router on OpenWrt 24.10 than
on the other three.

**A failure here is fatal**, unlike a feed name. These are named by URL — the
developer said "install this file" — and a build that reported success without
it would hand them a router quietly missing the thing they are testing against.

These are installed **without signature verification**. Projects publishing
this way usually sign with usign and ship a `.sig` beside the artifact, but
checking it needs their public key, which is exactly the trust decision a dev
container should not make on the user's behalf. Treat anything installed this
way as trusted-by-URL, pin an exact release rather than a `latest` link, and do
not copy the pattern into anything that ships.

### Feeds

**They are left exactly as the rootfs shipped them.**

It is tempting to strip the `kmods` line — it is pinned to an exact kernel build
hash and nothing in it can ever load, because the running kernel is the
engine's. Every widely-copied container recipe does exactly that. Measured, it
is wrong: the kmods feed is what resolves the `kmod-*` *dependencies* of
ordinary packages, so without it `apk add luci-app-sqm` fails with
`kmod-ifb (no such package)`. The modules install as files and simply never
load, which costs a few hundred KB and a complaint from kmodloader at boot —
far cheaper than a package manager that cannot install half the feed.

Leaving them alone is also what keeps the version pin correct. apk records hard
pins in `/etc/apk/world` (`base-files=1707~4ccb782af7`), so a 25.12.4 rootfs
pointed at the 25.12.5 feed fails every install with `breaks: world[...]`. The
feeds that shipped in the image already name that image's exact point release;
any rewriting risks breaking it.

The practical consequence for a project: **pin an exact point release.** A
snapshot rootfs and a snapshot feed are rebuilt daily and independently, so
they drift apart within a day and installs start failing. `owlab doctor` warns
when a router names `snapshot`.

### apk and opkg

The manager is a property of the distribution, not a function of the version.
OpenWrt switched in 25.12; a fork can track 25.12 and still build with opkg
(Kwrt does, with `# CONFIG_USE_APK is not set`). So the precedence is: what the
config says, then what the distribution pins, then the OpenWrt version rule.

Deriving it from the release number alone would hand such a router apk commands
and fail every install.

---

## The VM tier

A real OpenWrt kernel, booted by QEMU as an ordinary process on the developer's
machine.

### Why not QEMU in a container

Because `/dev/kvm` has to be passed into that container, and Docker Desktop
grants it on neither macOS nor Windows — exactly the hosts that need this tier
most. Run natively the problem inverts:

| host | guest | accelerator |
|---|---|---|
| macOS, Apple Silicon | aarch64 (`armsr/armv8`) | hvf |
| macOS, Intel | x86_64 | hvf |
| Linux | matches host | kvm |
| Windows | matches host | whpx |

On Apple Silicon that is a nine-second boot with no nested virtualisation, no
minimum macOS version and no special hardware.

### Choosing the accelerator

The first question is not which accelerator the host has but **whether the
guest CPU is the host CPU**. No accelerator virtualises a foreign
architecture: an x86 router on an ARM laptop is translated instruction by
instruction, roughly an order of magnitude slower. `arch: auto` (the default)
already does the right thing, and owlab warns loudly rather than being quietly
slow.

Everything else is probed, never inferred from the OS. Homebrew's
`qemu-system-x86_64` on Apple Silicon reports `tcg` only — it is built without
hvf, because hvf cannot run an x86 guest on an ARM CPU — while
`qemu-system-aarch64` from the same bottle has hvf. No rule about macOS gets
that right.

On Linux, being *built* with kvm is not the same as being *allowed* to use it:
`/dev/kvm` is `root:kvm`, and a user outside that group gets a permission error
from QEMU well past the point where a clear message would have helped. owlab
opens the device to find out.

`-cpu host` only when the accelerator is native; under translation it asks QEMU
to emulate whatever this machine happens to be, which is both slower and less
reproducible than naming a model.

### Images

Upstream combined disk images — kernel, bootloader and rootfs in one file:

```
openwrt-25.12.4-armsr-armv8-generic-squashfs-combined-efi.img.gz
```

**squashfs, not ext4.** squashfs is what a router runs: a read-only `/rom` with
a writable overlay on top, which is what makes `firstboot`, `jffs2reset` and
the whole reset-to-defaults story behave as they do on hardware. An ext4 image
is one flat writable filesystem and quietly makes half of that untestable.

Only four targets qualify, and the bar is that upstream publishes a *combined*
image: `x86_64`, `i386_pentium4`, `aarch64_generic` (armsr/armv8) and
`arm_cortex-a15_neon-vfpv4` (armsr/armv7). malta publishes a bare kernel and a
separate rootfs; mvebu is a real board with no firmware to boot under QEMU.
owlab would have to invent a boot arrangement upstream never tests, and "it
boots but not like the real thing" is the one outcome this tier exists to
avoid.

armsr publishes EFI-only images, so aarch64 and armv7 need an edk2 build on the
host. It is searched for beside the QEMU binary first — right for Homebrew, for
a manual build and for a distribution package alike — then in the usual
distribution locations under the usual distribution aliases (`AAVMF_CODE.fd`,
`QEMU_EFI.fd`). x86 still publishes a legacy-boot combined image, which SeaBIOS
handles with nothing to find.

Images are verified against the published `sha256sums`. That file comes from
the same server over the same TLS connection as the image, so it catches a
truncated download or a broken mirror, not a compromised download server.
Upstream also publishes `sha256sums.asc`; verifying it needs the release
keyring, which is a trust decision beyond what a dev tool should make on the
user's behalf.

### Disks

Two, and one of them exists because of a hard limit.

`disk.qcow2` is a qcow2 overlay whose backing file is the cached image. The
base is never written to, so one download serves every router and every project
on the machine.

`overlay.qcow2` is a second disk, and `/overlay` is moved onto it with stock
OpenWrt extroot — the same mechanism a real router uses for a USB stick. This
is not a convenience. The overlay in an upstream combined image is fixed at
build time (87 MB on armsr) and **cannot be grown from inside a running
system**: the filesystem was created without a resize inode, so an online
`resize2fs` stops after the first block group. Measured, a 900 MB partition
yielded 122 MB. A separate disk has no such history, costs one reboot during
first provisioning, and grows sparsely — a 2 GB disk occupies what is actually
written.

`disk: 0` turns it off and leaves the image's own overlay.

### Networking

Two NICs, and the order is the design.

OpenWrt's `board.d` makes the first ethernet port LAN and the second WAN. LAN
gets the static 192.168.1.1 and is where uhttpd and dropbear accept
connections; WAN gets DHCP and is where the firewall drops everything inbound.
Forwarding the host's ports to the WAN side would produce a router that is up,
reachable by nothing, and blamed on QEMU.

The LAN SLIRP network is given the guest's own subnet:

```
-netdev user,id=lan,net=192.168.1.0/24,host=192.168.1.254,hostfwd=tcp::8090-192.168.1.1:80
```

A `hostfwd` can only reach an address SLIRP believes is on its network, and the
guest's address is fixed by `config_generate` before owlab gets a chance to say
otherwise.

WAN is a plain SLIRP network, which is what gives the router a default route
and working DNS so the package manager can reach the feeds. It is NAT, so
nothing on the host's network is exposed.

A `virtio-rng-pci` is attached because OpenWrt's boot waits on entropy — urngd,
dropbear host keys, the overlay — and a headless VM has almost nothing to fill
the kernel pool from.

### Lifecycle

QEMU is detached with `-daemonize`, which also writes the pidfile; Windows has
no such flag, so there the process is started detached and the pidfile written
by owlab. A pidfile outlives the process that wrote it, so liveness is checked
with signal 0 rather than trusted.

`owlab down` asks the *guest* to power itself off over ssh rather than killing
QEMU. The overlay is a real filesystem with real dirty pages, and pulling the
plug on every `down` would eventually cost someone their installed packages.
The kill is the fallback for a router wedged badly enough not to answer.

The console is captured to a file (`-serial file:`) and that is what
`owlab logs` shows — the same stream a container's log is, from the first
kernel line onward. Interacting with the router happens over ssh, where a shell
has a real terminal.

### Provisioning

The disk is the cache. Provisioning runs once, on a new disk, and the marker
written at the end is what makes every later `owlab up` a plain boot.

The order matters, and every step in it was moved there for a measured reason:

1. **extroot**, then reboot. Everything after this writes to the filesystem it
   moves.
2. **Packages**, then out-of-feed packages.
3. **Radios**: install `kmod-mac80211-hwsim` and write
   `/etc/modules.d/mac80211-hwsim` with the requested count.
4. **Reboot.** Not cosmetic — see below.
5. **Overlay and fixtures**, then `wifi up`.

### Why the reboot before the overlay

Everything above was installed onto a system that had already finished booting,
and a good deal of OpenWrt reads its inputs exactly once, at boot:

- **procd** starts the services a package enabled — `wpad` among them — at
  boot, and never notices one that appeared afterwards.
- **netifd** loads `/lib/netifd/wireless/*` at startup, so `wifi-scripts`
  installed later leaves `wifi up` reporting `Command failed: Not found`
  against a netifd with no wireless support loaded.
- **ubusd** reads `/usr/share/acl.d/` at startup. wpad's ACL is what lets
  hostapd — which runs as the unprivileged `network` user — publish its ubus
  object at all, and without that object the wireless setup script waits
  forever and the radios stay `pending: true`.
- **kmodloader** loads `/etc/modules.d/*` at boot, which is when the hwsim
  radios come into existence.

Each of those was measured on a router that looked fully provisioned, and not
one of them reports anything that names the cause.

It comes *before* the overlay because the fixtures have to see the result:
`60-wifi.sh` asks whether real phys are present and configures them if they
are, and one reboot earlier it would find none and write the invented radios a
container gets.

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

`/etc/modules.d/` rather than a `modprobe`, because the radios have to exist
before netifd looks for them and that is how OpenWrt says so. The count is the
module's own parameter, so two radios are two phys — a dual-band router —
rather than one phy pretending to be two bands.

Bands and channels are assigned rather than detected. `wifi config` puts every
hwsim radio on 6 GHz with channel auto, because the simulated phy advertises
every band and 6 GHz is the highest; under the default regulatory domain that
comes up on channel 0 with no width the UI can name. The first radio gets 2.4
GHz channel 6, the second 5 GHz channel 36, and a country is set — with `00`
the regulatory domain permits almost nothing and 5 GHz channels come up
disabled.

---
