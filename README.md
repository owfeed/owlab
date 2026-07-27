# owlab

Dev routers for OpenWrt and ImmortalWrt package development. Describe the
releases you want to test against in one file, run one command, get working
routers — on Linux, WSL2, Docker Desktop for Windows, or macOS on either Intel
or Apple Silicon.

```yaml
# owlab.yaml
version: 1

defaults:
  packages: [luci, luci-app-firewall]

routers:
  - id: owrt2512
    release: "25.12.4"
  - id: owrt2410
    release: "24.10.8"
  - id: imm2512
    distro: immortalwrt
    release: "25.12.1"
```

```console
$ owlab up
  owrt2512   http://localhost:8080   ssh -p 2222 root@localhost
  owrt2410   http://localhost:8081   ssh -p 2223 root@localhost
  imm2512    http://localhost:8082   ssh -p 2224 root@localhost

  log in as root, empty password   (set OWLAB_ROOT_PASSWORD to require one)
```

**Log in as `root` with an empty password.** That is what a stock OpenWrt
rootfs ships with, and it is the right default for a throwaway box on
localhost — LuCI still shows its login form, it just accepts a blank field.
Set `OWLAB_ROOT_PASSWORD` before `owlab up` if you need one, e.g. for tooling
that authenticates for real.

ssh works by key. owlab installs **every** `~/.ssh/id_*.pub` it finds — all of
them, not the first one, so reaching for a key you own never gets you
"Permission denied (publickey)" from a box that holds another of your keys.
Point it elsewhere with `OWLAB_PUBKEY=/path/to/key.pub` (several allowed,
separated the way `PATH` is). Only `id_*.pub` is collected: `~/.ssh` routinely
holds other people's keys, and those are not yours to install. If nothing is
found, `owlab up` says so and tells you how to fix it.

The ssh *host* keys are baked into the image and shared by everyone using it —
they authenticate nothing, they only stop ssh from prompting. Do not put these
routers on a network you care about.

Each of those is a stock rootfs for that release with procd as PID 1 — the
same userland your package will ship against, not an approximation. `apk` and
`opkg` work, `ubus` answers, netifd manages the interfaces, LuCI renders.

## Why

LuCI's own wiki still recommends running OpenWrt under QEMU and editing files
over SCP. The forum consensus is "build an ipk in the SDK, then copy it to a
real router". Neither gives you three releases side by side, and neither
survives being handed to a contributor on a different operating system.

owlab exists to make that setup a file you commit next to your package.

## Install

Prebuilt binaries and packages are published per release. Until then:

```console
$ go install github.com/VizzleTF/owlab/cmd/owlab@latest
```

`owlab` needs Docker (or OrbStack, Colima, Podman, Rancher Desktop) with
Compose v2. Run `owlab doctor` to see what your machine supports.

## Fidelity

How closely a router has to resemble real hardware is chosen per router,
because the cheap tier is enough for most LuCI work and the expensive ones are
not available everywhere.

| `fidelity` | what it is | where it runs |
|---|---|---|
| `basic` (default) | container, procd as PID 1 | everywhere |
| `full` | plus real `mac80211_hwsim` radios from the host kernel | Linux, WSL2, Colima, Lima |
| `vm` | a real OpenWrt kernel under QEMU on the host | everywhere, accelerator permitting |

`owlab doctor` will tell you plainly when a tier is unavailable rather than
starting something quietly less than what you asked for. On Docker Desktop for
macOS, for instance, `full` can never work: that VM's kernel is built with
`CONFIG_CFG80211` unset, so `mac80211_hwsim` — and `virt_wifi`, and `vwifi`,
and `wmediumd`, which all sit on top of cfg80211 — cannot load at all.

### `fidelity: vm`

A real OpenWrt kernel, booted by QEMU as a normal process on your machine —
not inside a container. That is the point: nested QEMU needs `/dev/kvm` passed
into the container, which Docker Desktop grants on neither macOS nor Windows,
i.e. exactly the hosts that need this tier most.

```yaml
routers:
  - id: real
    fidelity: vm
    release: "25.12.4"
    packages: [luci, luci-app-firewall, kmod-nft-tproxy]
    ports: { http: 8090, ssh: 2290 }
```

```console
$ owlab up
== real (vm, OpenWrt 25.12.4 on aarch64_generic)
  downloading openwrt-25.12.4-armsr-armv8-generic-squashfs-combined-efi.img.gz
  moving /overlay onto the 2G disk
  rebooting onto the new overlay
  installing 3 packages
  applying the owlab overlay and fixtures
  real           http://localhost:8090   ssh -p 2290 root@localhost
```

What you get that a container cannot give you is **kernel modules that
actually load**:

```console
$ owlab exec real -- 'uname -r; lsmod | grep nft_tproxy'
6.12.87
nft_tproxy    12288  0
```

Requirements and behaviour:

- **QEMU on the host.** `brew install qemu`, `apt install qemu-system-arm
  qemu-system-x86`, `winget install SoftwareFreedomConservancy.QEMU`.
  `owlab doctor` reports the version, the accelerator it picked and the
  firmware it found.
- **The guest architecture should be the host architecture.** No accelerator
  virtualises a foreign CPU, so an `x86_64` router on an ARM laptop is
  translated instruction by instruction. `arch: auto` (the default) already
  does the right thing; owlab warns loudly rather than being quietly slow.
  Homebrew's `qemu-system-x86_64` on Apple Silicon reports `tcg` only — it is
  built without hvf, because hvf cannot run an x86 guest on an ARM CPU.
- **The disk is the state.** A VM keeps everything installed on it across
  `owlab down`; only `--rebuild` is a factory reset. Boot images are cached
  once per release under your user cache directory and shared by every project.
- **`disk: 2G`** adds a second virtual disk and moves `/overlay` onto it, which
  is stock OpenWrt extroot. Without it you get the image's own overlay — 87 MB
  on armsr, fixed at build time and not growable from inside a running system.
  It costs one reboot during first provisioning and grows sparsely. `disk: 0`
  turns it off; `memory:` and `cpus:` are there too.
- **Reached the same way as a container**: `localhost:<port>`, forwarded to the
  router's LAN side. `sync`, `install`, `exec`, `shell` and `logs` all work,
  over ssh instead of `docker exec`.

Not yet working: `mac80211_hwsim` radios inside a VM appear as real phys and
accept `iw` commands, but netifd's wireless setup does not complete for them
on 25.12. Use `fidelity: basic`, whose wireless pages render from config.

## Commands

```
owlab up          build and start routers
owlab down        stop routers
owlab shell       open a shell on a router
owlab exec        run a command on a router
owlab install     install packages on a running router
owlab logs        show a router's boot and service log
owlab status      list routers and where to reach them
owlab open        open a router's LuCI in a browser
owlab build       build a real .apk/.ipk with the OpenWrt SDK
owlab doctor      check this machine for problems
```

`packages:` in the config is what the image is *built* with and survives a
rebuild. `owlab install` is for trying something out — containers hold no
volumes, so `owlab up --rebuild` is a factory reset and anything installed that
way is gone.

## The inner loop

```console
$ owlab sync                 # copy your sources in, reload LuCI
  owrt2512   42 files, 846 KB

$ owlab sync --watch         # ...and keep doing it as you edit
```

`sync` puts files exactly where `luci.mk`'s install rules would, then drops
the caches its postinst drops (`/tmp/luci-indexcache*`,
`/tmp/luci-modulecache`, `rpcd reload`). Without that last part an edited
template keeps rendering its old text, which reads like the sync silently
failed.

It does **not** build a real package — that is a separate, slower verification
step. What it gives you is edit, `sync`, reload.

Two things it will not overwrite: `/etc/config/` and `/etc/uci-defaults/`.
Those are the router's state, not your package's; a real install ships them as
conffiles and leaves an existing one alone.

### If your package builds its assets

Many do. A theme's `cascade.css` is concatenated from `styles/`, translations
are compiled from `.po`, JS gets bundled. Syncing the tree as-is copies
everything *except* the files LuCI actually asks for, and the router serves
404s for its own stylesheet — which looks like a broken sync rather than a
missing build.

```yaml
project:
  build: ./build-css.sh htdocs/luci-static/footstrap/cascade.css --dev
```

Runs in the project directory before every sync, including every `--watch`
iteration. `--no-build` skips it.

There is a matching `post_sync:`, which runs **on the router** after the files
land — for the half of an install that copying files does not cover:

```yaml
project:
  post_sync: |
    uci -q set luci.themes.Footstrap=/luci-static/footstrap
    uci -q commit luci
```

A package's own `root/etc/uci-defaults/` is not synced (that directory is the
router's state), so whatever it would have registered goes here.

`--watch` polls rather than using filesystem events: inotify does not
propagate from a Windows drive into WSL, and the desktop engines' shared
filesystems have their own gaps. A poll is slower to notice but it notices
everywhere. When a build step is configured the whole project is watched, not
just the `install:` directories — the files a build *reads* are by definition
not the files it installs.

Routers are named by the ids in `owlab.yaml`. With no ids, commands that
can act on many routers act on all of them.

## Configuration

```yaml
version: 1

project:
  name: luci-theme-example        # defaults to the directory name
  theme: example                  # for themes: activate after a sync
  install:                        # defaults to luci.mk's own mapping
    htdocs: /www
    ucode:  /usr/share/ucode/luci
    luasrc: /usr/lib/lua/luci
    root:   /

defaults:                         # merged into every router
  fidelity: basic
  arch: auto                      # matches the host; no emulation
  packages: [luci-light]

routers:
  - id: owrt2512
    distro: openwrt               # openwrt | immortalwrt
    release: "25.12.4"
    packages: ["+luci-app-sqm"]   # + adds to defaults, - removes
    ports: { http: 8025, ssh: 2225 }

  - id: real                      # fidelity vm only:
    fidelity: vm
    release: "25.12.4"
    memory: 512M                  # qemu -m
    cpus: 2                       # qemu -smp
    disk: 2G                      # extroot disk; 0 to use the image's own
    ports: { http: 8090, ssh: 2290 }
```

A package list with `+`/`-` entries is applied on top of the defaults; a list
without any prefix replaces them.

### Packages that are in no feed

Most of what a router needs is in OpenWrt's own feeds and goes in `packages:`.
Your own package is not, and neither is anything published on GitHub Releases.
That is what `extra_packages:` is for:

```yaml
project:
  theme: footstrap          # activate it once installed; see below

defaults:
  extra_packages:
    - name: luci-theme-footstrap
      apk: https://github.com/VizzleTF/luci-theme-footstrap/releases/download/v0.11.5/luci-theme-footstrap-0.11.5-r1.apk
      ipk: https://github.com/VizzleTF/luci-theme-footstrap/releases/download/v0.11.5/luci-theme-footstrap_0.11.5-r1_all.ipk
```

```console
$ owlab up
owlab: fetching https://github.com/VizzleTF/luci-theme-footstrap/releases/download/v0.11.5/luci-theme-footstrap-0.11.5-r1.apk
owlab: installing luci-theme-footstrap-0.11.5-r1.apk
  owrt2512   http://localhost:8025   ssh -p 2225 root@localhost
```

Two URLs, because the two package managers do not share a naming scheme — the
same release publishes `luci-theme-footstrap-0.11.5-r1.apk` and
`luci-theme-footstrap_0.11.5-r1_all.ipk`. owlab picks the right one per router
and never hands a `.apk` to a 24.10 box. Either key may be omitted; a router
whose manager has no build is told so and carries on.

Downloads happen on the host and are cached in `.owlab/cache/`, so `up` does
not re-fetch them. They have to: a stock OpenWrt rootfs has no `curl`, and its
busybox `wget` cannot do TLS.

A failure here is fatal, unlike a name in `packages:` that a particular feed
happens not to carry. These are named by URL — you said "install this file" —
and a build that reported success without it would hand you a router quietly
missing the thing you are testing against. They are also installed with
`--force-overwrite`, because their dependencies routinely replace a file the
stock image already owns: `luci-app-openclash` pulls `dnsmasq-full`, which
ships `/etc/init.d/dnsmasq` and collides with `dnsmasq`. Without it the same
config produces a different router on OpenWrt 24.10 than on the other three.

**These are installed without signature verification.** Projects publishing
this way usually sign with usign and ship a `.sig` beside the artifact, but
checking it needs their public key — a trust decision a dev container should
not be making on your behalf. Treat anything installed this way as
trusted-by-URL, pin an exact release rather than a `latest` link, and do not
copy the pattern into anything that ships.

#### Themes need one more step

Installing a theme package only *registers* it: it adds `luci.themes.<Name>`
and leaves `luci.main.mediaurlbase` alone, because a package has no business
changing what the user is looking at. On a dev box that is backwards — the
reason the theme is there is to be looked at. Set `project.theme` and owlab
activates it on first boot, after the package's own uci-defaults have run.

The value is the media directory name, i.e. `footstrap` for
`/www/luci-static/footstrap`. If that directory does not exist, owlab says so
in the boot log rather than pointing `mediaurlbase` at nothing — LuCI resolves
templates through it, so a wrong value crashes the dispatcher instead of
falling back.

### Fixtures

LuCI renders almost nothing on its own. The sections, tabs, tables, badges and
forms a theme or an app exists to style only appear when there is config
behind them — a container with one interface and no clients shows a fraction
of the widget surface, and a whole class of styling bugs simply cannot be seen
on it.

Fixture profiles invent that config:

| profile | what it adds |
|---|---|
| `networks` | a WAN, a guest bridge, two VLANs, a disabled interface, static routes, DHCP pools, firewall zones, and a four-port "Port status" card |
| `clients` | static leases, local hostnames, and at boot the DHCP leases and ARP neighbours that put rows in the data tables |
| `portforwards` | port forwards, one of them disabled |
| `system` | LEDs, mounts, swap, cron entries |
| `wireguard` | a wg0 tunnel with two peers and its zone |
| `wifi` | radios that render on a box with no radios |

`lived-in` (the default) is `networks clients portforwards system`. `all` is
everything. `none` gives a bare router. Dependencies are pulled in
automatically, so `fixtures: [portforwards]` also gets `networks`.

The `wifi` profile is opt-in because it claims `/etc/config/wireless`
outright. It is worth knowing what it achieves: LuCI's wireless pages are
almost entirely config-driven — the menu appears when `/sbin/wifi` exists, the
radio list comes straight from UCI, and the encryption capability matrix is
built by asking the `hostapd` binary what it supports. So the menu, the radio
rows, the SSIDs, the modes and the full edit form all render correctly with no
kernel wireless support of any kind. Only signal, scan results and the
associated-stations table stay empty; those need real radios (`fidelity:
full`) or a VM.

**Nothing in a fixture may touch `lan`.** That is eth0, with the container
engine's own address on it, and it is the only way in — there is no console to
recover from. Every invented network sits on a dummy device or a VLAN that
carries no traffic.

### Pin point releases

`release: "25.12.4"`, not `"25.12"`. The package manager records hard version
pins (`base-files=1707~4ccb782af7` in `/etc/apk/world`), so a 25.12.1 rootfs
pointed at the 25.12.5 feed fails every install with `breaks: world[...]`.
owlab pins the feed to the exact release the rootfs came from, and old
point releases stay available upstream, so this always works.

`release: "snapshot"` is supported but not recommended: snapshot rootfs images
and snapshot feeds are rebuilt daily and independently, and drift apart within
a day.

### Prebuilt images

Assembling a router from the upstream rootfs takes about a minute, most of it
installing the LuCI set. Starting from a published image takes seconds:

```yaml
routers:
  - id: owrt2512
    release: "25.12.4"
    arch: x86_64
    image: ghcr.io/vizzletf/owlab-rootfs:openwrt-25.12.4-x86_64
```

Everything else still applies on top — your `packages:` are installed (the
package manager reports the ones already there as up to date), fixtures are
re-applied, the theme is set. A prebuilt image is an accelerator, not a
different code path, so nothing behaves differently from a locally built one.

Published tags are `<distro>-<release>-<arch>`:

```
ghcr.io/vizzletf/owlab-rootfs:openwrt-25.12.4-x86_64
ghcr.io/vizzletf/owlab-rootfs:openwrt-25.12.4-aarch64_generic
ghcr.io/vizzletf/owlab-rootfs:openwrt-24.10.8-x86_64
ghcr.io/vizzletf/owlab-rootfs:immortalwrt-25.12.1-x86_64
...
```

One tag is one target — there is no multi-arch manifest and there should not
be. An OCI image index has no field for "distro version", and most OpenWrt
architecture names (`aarch64_generic`, `mips_24kc`) have no valid GOARCH
mapping, so a manifest list cannot express this matrix. Upstream does not try
either.

Each image carries its own provenance at `/usr/share/owlab/compliance/`: the
package manifest, a CycloneDX SBOM with per-package licences, and the
buildinfo files pinning the tree and every feed to an exact commit. That
material exists upstream but is *not* inside the rootfs — the tarball ships no
licence texts and no copyright notices at all — so owlab assembles it at build
time rather than leaving every downstream user to reconstruct it.

### Architecture

`arch: auto` picks whatever runs natively — `aarch64_generic` on Apple
Silicon, `x86_64` on Intel and AMD. Anything else runs under emulation, which
works but is slow enough to notice; `owlab doctor` warns when you have asked
for it.

## Building a real package

`sync` is the development loop; `build` is the verification step.

```console
$ owlab build
building luci-theme-footstrap for OpenWrt 25.12.4 (x86_64)
  sdk    openwrt/sdk:x86-64-25.12.4

built:
  luci-theme-footstrap-0.260727.54272.apk  (149 KB)

$ owlab install owrt2512 dist/luci-theme-footstrap-0.260727.54272.apk
```

They are not the same thing, and the difference is measurable. luci.mk sets
`LUCI_MINIFY_JS` and `LUCI_MINIFY_CSS` by default, so a real build runs your
sources through jsmin and csstidy:

```
from the package:  120358 bytes   (minified)
from owlab sync:   418930 bytes   (as written)
```

Code that works unminified and breaks minified is invisible until someone
builds a package — which, without this, is your users.

`owlab install` takes a path as readily as a package name, so the output of
`build` goes straight into a router.

**This runs under emulation on Apple Silicon.** Every `openwrt/sdk` tag is
`linux/amd64` — the SDK is a cross-compiler and upstream publishes only
Linux-x86_64 SDK tarballs. owlab says so before it starts, because an OpenWrt
package build is long enough that unexplained slowness is worth a sentence.

## Kernel modules do not load

A container shares the host's kernel. The `kmod-*` packages in OpenWrt's feed
are built against *OpenWrt's* kernel, so their vermagic never matches and
`kmodloader` cannot load them:

```console
$ owlab exec owrt2512 -- 'uname -r; ls /lib/modules'
7.0.11-orbstack-...     # the engine's kernel, actually running
6.12.87                 # what the modules in the image were built for
```

They still *install*, which is what matters for dependency resolution — this
is why owlab leaves the `kmods` feed in place rather than stripping it the way
most container recipes do. Without it, `apk add luci-app-sqm` fails outright
with `kmod-ifb (no such package)`.

What actually decides whether a netfilter feature works is **the engine's
kernel**, not the package. nftables and `fw4` generally do work; so does
`tproxy` on OrbStack and on most Linux hosts. Check any of them directly:

```console
$ owlab exec owrt2512 -- 'nft add table inet t; \
    nft add chain inet t c "{ type filter hook prerouting priority -150; }"; \
    nft add rule inet t c meta l4proto tcp tproxy to :5678 && echo OK; \
    nft delete table inet t'
```

If something is missing: on Linux `modprobe` it on the host (`nft_tproxy`,
`nf_tproxy_ipv4`); on Docker Desktop for macOS the LinuxKit kernel is fixed
and there is no portable workaround. `fidelity: vm` runs a real OpenWrt
kernel, where every `kmod-*` loads for real:

```console
$ owlab exec real -- 'apk add kmod-nft-tproxy; lsmod | grep nft_tproxy'
nft_tproxy    12288  0
```

The same reasoning decides flow offloading. ImmortalWrt enables it by default
(`firewall.@defaults[0].flow_offloading`), the rule needs a flowtable, and a
flowtable needs `nf_flow_table` in the *running* kernel. Where it is missing,
nft rejects the entire ruleset and what is left is the base chains with
`policy drop` and no jumps into any zone — a router that completes a TCP
handshake and then resets, while `uhttpd` is up and listening. owlab probes for
it on first boot and turns the setting off only where the kernel cannot honour
it, so a host that *can* offload keeps the distribution's own configuration.

## What else a container changes

Two things bite packages that bring their own daemon, and neither announces
itself.

**procd jails services, and a container cannot build a jail.** `ujail` needs
namespaces the container is not allowed to create, procd reports the failure
as a crash, and the service ends up respawning forever:

```
procd: Instance dnsmasq::cfg01411c s in a crash loop 6 crashes
```

Nothing downstream mentions dnsmasq. You get a router with no DNS server, and
any package that configures dnsmasq to forward somewhere is configuring a
daemon that is not running. owlab removes `ujail` from the image so procd runs
those services directly — a lost security boundary inside a container that is
already one trust domain, in exchange for services that work. On a
`fidelity: vm` router the kernel is OpenWrt's own and jails work as on
hardware, so nothing is removed there.

**Outbound UDP port 53 does not leave the container.** The engine's own
resolver works; anything else times out:

```console
$ owlab exec pk -- 'dig +short github.com @127.0.0.11; dig +short github.com @8.8.8.8'
140.82.121.4
;; communications error to 8.8.8.8#53: timed out
```

TCP is unaffected — `https://github.com` answers fine. So a package with its
own resolver has to bootstrap through the engine's, and can then use DoH or
DoT over TCP normally. For podkop that is `bootstrap_dns_server` set to the
first `nameserver` in `/etc/resolv.conf`, with `dns_type doh` on top; its
defaults (`77.88.8.8`, plain UDP) resolve nothing, and sing-box exits with
`initial rule-set: ... lookup github.com: context deadline exceeded` — a
message that points at GitHub rather than at the transport.

## How it reaches the routers

Only through published ports. A container's bridge IP is routable from the
host on native Linux and inside WSL2, but not on Docker Desktop for Mac or
Windows — so nothing in owlab addresses a router that way, and
`http://localhost:<port>` works identically everywhere.

## Licence and trademarks

owlab is GPL-2.0-only.

owlab is not affiliated with, endorsed by, or sponsored by the OpenWrt
project or the Software Freedom Conservancy, which owns the OpenWrt trademark,
nor by the ImmortalWrt project. OpenWrt and ImmortalWrt are referred to here
only to say what this tool works with.

Images built by owlab contain unmodified OpenWrt or ImmortalWrt software,
which is licensed under the GPL and other licences by its own authors.
