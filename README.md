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
