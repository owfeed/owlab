# owlab

[Русская версия](README_ru.md)

Throwaway OpenWrt and ImmortalWrt routers. One file, one command, working
routers — on Linux, WSL2, Docker Desktop for Windows, or macOS.

For developing a LuCI package against several releases at once, for trying a
config before it touches the router the house depends on, for screenshots, for
learning what a setting does — and for answering "does my package still install
on 24.10?" in CI, which is [one step](#check-it-in-ci).

```console
$ go install github.com/VizzleTF/owlab/cmd/owlab@latest
$ owlab doctor
```

You need Docker (or OrbStack, Colima, Podman, Rancher Desktop) with Compose v2.
For `fidelity: vm` you need QEMU as well: `brew install qemu`, or
`apt install qemu-system-arm qemu-system-x86`.

Shipping the package too, not just developing it? The
[cookbook](https://owfeed.org/cookbook/) walks the whole path — build, test,
sign, publish — with files you can copy.

## Start

Put `owlab.yaml` next to your package:

```yaml
version: 1

routers:
  - id: owrt2512
    release: "25.12.4"
```

```console
$ owlab up
  owrt2512   http://localhost:8080   ssh -p 2222 root@localhost
```

Open the URL. Log in as `root`, leave the password blank.

## Recipes

### Test against several releases at once

```yaml
routers:
  - id: owrt2512
    release: "25.12.4"
  - id: owrt2410
    release: "24.10.8"
  - id: imm2512
    distro: immortalwrt
    release: "25.12.1"
```

Ports are assigned automatically. Set them yourself with
`ports: { http: 8025, ssh: 2225 }`.

### Add packages

Your router already has what a shipping OpenWrt device has. Add on top:

```yaml
defaults:
  packages: ["+luci-app-sqm", "+luci-app-ddns"]
```

Remove something with `-`:

```yaml
packages: ["-ppp", "-ppp-mod-pppoe", "+luci-app-sqm"]
```

A list without `+` or `-` replaces the default set completely.

To try a package without rebuilding:

```console
$ owlab install owrt2512 luci-app-ttyd
```

That one is gone after `owlab up --rebuild`. `packages:` survives.

## Install a package that is not in any feed

Your own, or anything from GitHub Releases. Both URLs, because apk and opkg
name their files differently:

```yaml
defaults:
  extra_packages:
    - name: luci-app-podkop
      apk: https://github.com/itdoginfo/podkop/releases/download/0.7.21/luci-app-podkop-0.7.21-r1.apk
      ipk: https://github.com/itdoginfo/podkop/releases/download/0.7.21/luci-app-podkop-v0.7.21-r1-all.ipk
```

Pin an exact release. These are installed without checking signatures.

### Work on your package with the router running

```console
$ owlab sync
  owrt2512   42 files, 846 KB
```

Reload the page and your changes are there. To do it on every save:

```console
$ owlab sync --watch
```

owlab copies the same directories `luci.mk` installs — `htdocs`, `ucode`,
`luasrc`, `root`. If your package lives in a subdirectory, say so:

```yaml
project:
  install:
    my-package/htdocs: /www
    my-package/ucode:  /usr/share/ucode/luci
    my-package/root:   /
```

### Build assets before every sync

If your package generates files that are not in the tree — a theme's
`cascade.css`, compiled translations, a JS bundle:

```yaml
project:
  build: ./build-css.sh htdocs/luci-static/footstrap/cascade.css
```

### Run something on the router after every sync

Registering the package, dropping a cache, restarting a service:

```yaml
project:
  post_sync: |
    uci -q set luci.themes.Footstrap=/luci-static/footstrap
    uci -q commit luci
```

### Work on a theme

```yaml
project:
  theme: footstrap
```

owlab selects it after each sync. Without this a theme is registered but not
shown — installing a theme package adds `luci.themes.<Name>` and deliberately
leaves `luci.main.mediaurlbase` alone.

[examples/luci-theme-footstrap](examples/luci-theme-footstrap/owlab.yaml) is a
real one: four routers, a build step for the generated CSS, and third-party
apps to check the cascade against.

### Load kernel modules, or get real WiFi

```yaml
routers:
  - id: real
    fidelity: vm
    release: "25.12.4"
    packages: ["+kmod-nft-tproxy"]
```

```console
$ owlab up
$ owlab exec real -- 'uname -r; lsmod | grep nft_tproxy'
6.12.87
nft_tproxy    12288  0
```

This one is QEMU with OpenWrt's own kernel, so `kmod-*` packages actually load.
It also gets two `mac80211_hwsim` radios, and they work — hostapd runs, iwinfo
reports signal and channel width, the wireless pages show the router instead of
its config file.

First start takes about a minute and a half; after that, twenty seconds. The
disk keeps everything you installed across `owlab down`.

Tune it with `memory: 512M`, `cpus: 2`, `disk: 2G`, `radios: 2`. Set `radios: 0`
for no wireless.

### Make the box look inhabited

An empty router renders almost nothing — no zone badges, no VLANs, no DHCP
leases, no tables with rows in them. Fixtures invent all of it:

```yaml
defaults:
  fixtures: [all]
```

Individual profiles: `networks`, `clients`, `wireguard`, `portforwards`,
`system`, `wifi`. `lived-in` is everything except wifi, and is the default.
`none` turns them off.

### Build a real .apk or .ipk

```console
$ owlab build
$ owlab install owrt2512 dist/noarch/luci-app-mine-1.0-r1.apk
```

Uses OpenWrt's SDK. Worth doing before a release: a real build minifies JS and
CSS, which a sync does not, and that difference has broken packages that worked
fine on the dev box.

Artifacts go in a directory named for their architecture, because an apk's
filename does not carry one and everything downstream reads the directory. An
architecture-independent package produces both spellings — apk requires
`noarch` and rejects `all`, opkg has only ever known `all` — so one build writes
two directories:

```
dist/
├── noarch/luci-app-mine-1.0-r1.apk
└── all/luci-app-mine_1.0-r1_all.ipk
```

That layout is [owfeed's artifact contract][artifact-contract], which is what
lets `owlab build` hand its output straight to a publishing tool without either
one knowing about the other. `--layout flat` restores the old single-directory
output for one release.

[artifact-contract]: https://github.com/VizzleTF/owfeed/blob/main/docs/artifact-contract.md

### Check it in CI

```yaml
- uses: VizzleTF/owlab/action@v0.4.0
  with:
    releases: "25.12.5 24.10.8"
    install: dist/*/luci-app-mine-*.apk
    assert: |
      http 200 /cgi-bin/luci/admin/services/mine
      service mined
      uci mine.@mine[0].enabled
```

No `owlab.yaml` needed — the release numbers are the configuration. The job
fails if the package does not install, the page does not render or the service
is not running, and the summary says which router and which check.

The same thing locally, and what the action runs:

```console
$ owlab test --release 25.12.5 --release 24.10.8 \
    --install 'dist/*/luci-app-mine-*.apk' \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

Start, install, assert, tear down, exit 0 or 1. `--keep` leaves the routers up,
`--json` writes a report to stdout, and the log of any router that failed is
printed while the container still exists.

Two things it does that a hand-written `curl` does not. It **logs into LuCI as
root** first — every page an app exists to serve is under `/cgi-bin/luci/admin`,
and an unauthenticated request there is a redirect to the login form, which is
why the obvious check reports 403 on a perfectly healthy page. And it **fails a
200 whose body is an error page**: when LuCI's dispatcher catches an exception
it still answers 200, with the trace in the body.

Full assertion list and the JSON shapes: [docs/reference.md](docs/reference.md#owlab-test).


### Check it installs from a feed, not just from a file

```console
$ owlab test --release 25.12.5 \
    --feed 'https://example.org/releases/25.12/x86_64/packages.adb' \
    --feed-key ./myfeed.pem \
    --install my-app \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

Installing a file proves the package works. Installing it **by name** out of a
signed index proves the channel does — that the index parses, the URL does not
redirect, and the key on the router matches the one that signed it. A file
install cannot fail in any of those ways, so it cannot detect them.

Nothing is installed with `--allow-untrusted` here: the index's signature is
what makes the package acceptable. For opkg the key file's *name* must be the
key id, because that is what opkg looks it up by.
### Get onto the router

```console
$ owlab shell owrt2512
$ owlab exec owrt2512 -- 'logread | tail -20'
$ owlab logs owrt2512 -f
$ owlab open owrt2512
```

`ssh -p 2222 root@localhost` works too. owlab installs every `~/.ssh/id_*.pub`
it finds. Point it elsewhere with `OWLAB_PUBKEY=/path/to/key.pub`.

### Set a root password

```console
$ OWLAB_ROOT_PASSWORD=hunter2 owlab up --rebuild
```

Default is no password. Fine for a box on localhost, and LuCI accepts the empty
field.

### Keep the config somewhere else

```console
$ owlab --config ../my-package/owlab.yaml up
$ owlab --config ../my-package status        # a directory works too
$ export OWLAB_CONFIG=~/src/my-package
```

Without it, owlab looks in the working directory and its parents.

[examples/](examples/) has configs you can point at directly.

### Find out if your pins are stale

```console
$ owlab releases
ROUTER          DISTRO    PINNED     NEWEST
owrt2512        openwrt   25.12.4    25.12.5    1 release behind
owrt2410        openwrt   24.10.8    24.10.8    up to date
```

`owlab releases --all` lists everything the download servers publish.

Pin exact point releases and move them deliberately. A rootfs and its feed have
to name the same release; mixing them fails every install with
`breaks: world[...]`.

### Start over

```console
$ owlab up --rebuild        # throw the routers away and rebuild
$ owlab down --purge        # and delete the images and VM disks
```

## Commands

```
owlab up          build and start routers
owlab down        stop routers
owlab sync        copy your package in and reload LuCI
owlab shell       open a shell
owlab exec        run a command
owlab install     install packages on a running router
owlab build       build a real .apk/.ipk with the SDK
owlab test        start, install, assert, tear down — one exit code
owlab logs        boot and service log
owlab releases    check the pins against the download servers
owlab status      what is running and where
owlab open        open LuCI in a browser
owlab doctor      check this machine
owlab version     print the owlab version
```

Every command takes router ids. With none, it acts on all of them.
`--config <path>` (or `-c`) works on any of them, before or after the command.
`test`, `status` and `releases` take `--json`.

## When something breaks

Run `owlab doctor` first. It checks ports, ssh keys, line endings, QEMU, and
whether your engine forwards DNS.

Then [docs/troubleshooting.md](docs/troubleshooting.md). Most things that go
wrong here look like something else entirely: a router that answers LuCI but
resets every connection, a package that installs and does nothing, radios that
exist but never come up. They are all in there, sorted by symptom.

[docs/runbook.md](docs/runbook.md) has the procedures — each one a goal, the
commands, and how to tell it worked.

## What this sits next to

[containerlab](https://containerlab.dev/) builds network topologies — many
nodes, links between them — and supports OpenWrt as a node kind. owlab builds
one router you develop against: no topology, but `sync` with hot reload,
fixtures, the SDK build and `test`. Two routers running BGP at each other is
containerlab; the same LuCI page open on 24.10 and 25.12 while you edit it is
this.

[owfeed](https://github.com/VizzleTF/owfeed) publishes packages — it builds,
signs and indexes an apk feed, and `owfeed smoke` installs the result on a real
OpenWrt image before you ship it. That last step also starts a container, and
the resemblance is deliberate: owlab is the development cycle, `owfeed smoke` is
one gate before a publish. They are independent on purpose, so that "will this
feed install" never depends on whether owlab is installed correctly.

They compose anyway, through a file format rather than a dependency: `owlab
build` writes `dist/<arch>/` and every owfeed stage reads it. owlab holds no
keys at any point, which is the whole of its side of the boundary — it asserts
that a package works, and never that anyone should trust it.
[ECOSYSTEM.md](https://github.com/VizzleTF/owfeed/blob/main/docs/ECOSYSTEM.md)
is where that boundary is written down, along with the contracts across it, and
[docs/STATUS.md](docs/STATUS.md) says how much of owlab's side of it exists.

## Notes

Log in as `root` with an empty password unless you set one.

The ssh host keys are baked into the image and shared by everyone using it. Do
not put these routers on a network you care about.

Pin exact point releases. `snapshot` images and `snapshot` feeds are rebuilt
daily and independently, so installs start failing within a day.

## Docs

[docs/](docs/) has the rest, in English and Russian: the runbook, the
troubleshooting log, the config reference, how it all works, and how releases
are cut. [CONTRIBUTING.md](CONTRIBUTING.md) is what to know before changing
something; [SECURITY.md](SECURITY.md) is what these routers are and are not.

## Licence

GPL-2.0-only, deliberately — [owfeed](https://github.com/VizzleTF/owfeed), by
the same author, is Apache-2.0. owlab embeds a `/etc/uci-defaults` and
`rc.local` overlay written against OpenWrt's own shell libraries and ships it
inside every image it builds, and that is derived work. Calling owlab from your
CI, your Makefile or your own tool imposes nothing on your code; the obligation
attaches to distributing a modified owlab.

OpenWrt is a registered trademark of the Software Freedom Conservancy. owlab is
not affiliated with or endorsed by the OpenWrt project or the SFC.
