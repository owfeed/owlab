# owlab

[Русская версия](README_ru.md)

owlab runs throwaway OpenWrt and ImmortalWrt routers. One file, one command, working routers on
Linux, WSL2, Docker Desktop for Windows, or macOS.

- **Use it to:** develop a LuCI package against several releases at once. Try a config before it
  touches the router the house depends on. Take screenshots, or learn what a setting does.
- **In CI:** it answers "does my package still install on 24.10?" in [one step](#check-it-in-ci).
- **Needs:** Docker (or OrbStack, Colima, Podman, Rancher Desktop) with Compose v2. QEMU only for
  `fidelity: vm`.
- **Shipping the package too?** The [cookbook](https://owfeed.org/cookbook/) walks the whole path
  (build, test, sign, publish) with files you can copy.

## Quick start

1. Install owlab and check the machine. `go install` needs Go 1.25.3 or later. Other ways:
   [Install](#install).

   ```console
   $ go install owfeed.org/owlab/cmd/owlab@latest
   $ owlab doctor
   ```

2. Put `owlab.yaml` next to your package:

   ```yaml
   version: 1

   routers:
     - id: owrt2512
       release: "25.12.5"
   ```

3. Start the router:

   ```console
   $ owlab up
     owrt2512   http://localhost:8080   ssh -p 2222 root@localhost
   ```

4. Open the URL. Log in as `root`, leave the password blank.

5. Build your package and test it on the routers in `owlab.yaml`:

   ```console
   $ owlab build
   $ owlab test --install 'dist/*/luci-app-mine-*' \
       --assert 'http 200 /cgi-bin/luci/admin/services/mine'
   ```

   - `owlab test` starts the routers, installs the package, runs the assertion, tears the routers
     down, and exits 0 or 1.
   - With no `owlab.yaml`, name the releases instead: `owlab test --release 25.12.5 …` creates one
     router per release.
   - What `--install` and `--assert` accept: [Check it in CI](#check-it-in-ci).

## Install

No Go toolchain? Take a binary from the
[latest release](https://github.com/owfeed/owlab/releases/latest) and put it on `PATH`.

- Builds exist for linux, darwin and windows, amd64 and arm64.
- On Windows it is PowerShell and `owlab.exe`. Nothing else is needed.
- For `fidelity: vm`, install QEMU: `brew install qemu`,
  `apt install qemu-system-arm qemu-system-x86`, or
  `winget install SoftwareFreedomConservancy.QEMU`.
- Per-platform needs (the WSL2 filesystem rule, the two optional Windows features, the `kvm` group
  on Linux) are in one section of the [runbook](docs/runbook.md#install-owlab-on-this-machine).

`owlab doctor` checks all of it and says what each missing piece costs.

## What do you want to do?

| I want to… | Run or set | Details |
|---|---|---|
| test on several releases at once | more entries under `routers:` | [Several releases](#test-against-several-releases-at-once) |
| add or remove packages | `packages:`, `owlab install` | [Add packages](#add-packages) |
| install a package from no feed | `extra_packages:` | [Not in any feed](#install-a-package-that-is-not-in-any-feed) |
| see my edits on a running router | `owlab sync --watch` | [Work on your package](#work-on-your-package-with-the-router-running) |
| work on a LuCI theme | `project.theme:` | [Theme](#work-on-a-theme) |
| load kernel modules or get real WiFi | `fidelity: vm` | [VM router](#load-kernel-modules-or-get-real-wifi) |
| fill LuCI pages with data | `fixtures:` | [Fixtures](#make-the-box-look-inhabited) |
| build a real `.apk` or `.ipk` | `owlab build` | [Build](#build-a-real-apk-or-ipk) |
| check a package in CI | `owfeed/owlab/action`, `owlab test` | [CI](#check-it-in-ci) |
| check a package installs from my feed | `owlab test --feed` | [From a feed](#check-it-installs-from-a-feed) |
| get a shell on the router | `owlab shell` | [Get onto the router](#get-onto-the-router) |
| find stale release pins | `owlab releases` | [Stale pins](#find-out-if-your-pins-are-stale) |
| start over | `owlab up --rebuild` | [Start over](#start-over) |
| fix something broken | `owlab doctor` | [When something breaks](#when-something-breaks) |

## Recipes

### Test against several releases at once

```yaml
routers:
  - id: owrt2512
    release: "25.12.5"
  - id: owrt2410
    release: "24.10.8"
  - id: imm2512
    distro: immortalwrt
    release: "25.12.1"
```

owlab assigns ports by position in `routers:`. The first router gets HTTP 8080 and ssh 2222, the
second 8081 and 2223, and so on. `owlab status` lists them. Set your own with
`ports: { http: 8025, ssh: 2225 }`.

### Add packages

Your router already has what a shipping OpenWrt device has. Add on top with `+`:

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

### Install a package that is not in any feed

Your own, or anything from GitHub Releases. Give both URLs, because apk and opkg name their files
differently:

```yaml
defaults:
  extra_packages:
    - name: luci-app-example
      apk: https://github.com/someone/example/releases/download/1.4.0/luci-app-example-1.4.0-r1.apk
      ipk: https://github.com/someone/example/releases/download/1.4.0/luci-app-example-v1.4.0-r1-all.ipk
```

- Pin an exact release.
- owlab installs these without checking signatures.

### Work on your package with the router running

```console
$ owlab sync
  owrt2512   42 files, 846 KB
```

Reload the page and your changes are there. To sync on every save:

```console
$ owlab sync --watch
```

owlab copies the directories `luci.mk` installs: `htdocs`, `ucode`, `luasrc`, `root`. If your
package lives in a subdirectory, say so:

```yaml
project:
  install:
    my-package/htdocs: /www
    my-package/ucode:  /usr/share/ucode/luci
    my-package/root:   /
```

### Build assets before every sync

Use this when your package generates files that are not in the tree: a theme's `cascade.css`,
compiled translations, a JS bundle.

```yaml
project:
  build: ./build-css.sh htdocs/luci-static/footstrap/cascade.css
```

### Run something on the router after every sync

Use this to register the package, drop a cache or restart a service.

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

owlab selects the theme after each sync.

- Without this, a theme is registered but not shown. Installing a theme package adds
  `luci.themes.<Name>` and deliberately leaves `luci.main.mediaurlbase` alone.
- [examples/luci-theme-footstrap](examples/luci-theme-footstrap/owlab.yaml) is a real one: four
  routers, a build step for the generated CSS, and out-of-feed apps to check the cascade against.

### Load kernel modules, or get real WiFi

```yaml
routers:
  - id: real
    fidelity: vm
    release: "25.12.5"
    packages: ["+kmod-nft-tproxy"]
```

```console
$ owlab up
$ owlab exec real -- 'uname -r; lsmod | grep nft_tproxy'
6.12.94
nft_tproxy    12288  0
```

This router is QEMU with OpenWrt's own kernel, so `kmod-*` packages load.

- **WiFi:** it gets two `mac80211_hwsim` radios, and they work. hostapd runs, iwinfo reports signal
  and channel width, and the wireless pages show the router instead of its config file.
- **Start time:** about a minute and a half the first time, twenty seconds after that.
- **Disk:** keeps everything you installed across `owlab down`.
- **Tuning:** `memory: 512M`, `cpus: 2`, `disk: 2G`, `radios: 2`. Set `radios: 0` for no wireless.

### Make the box look inhabited

An empty router renders almost nothing: no zone badges, no VLANs, no DHCP leases, no tables with
rows in them. Fixtures invent all of it:

```yaml
defaults:
  fixtures: [all]
```

- Individual profiles: `networks`, `clients`, `wireguard`, `portforwards`, `system`, `wifi`.
- `lived-in` is everything except wifi, and is the default.
- `none` turns them off.

### Build a real .apk or .ipk

```console
$ owlab build
$ owlab install owrt2512 dist/noarch/luci-app-mine-1.0-r1.apk
```

`owlab build` uses OpenWrt's SDK. Do it before a release: a real build minifies JS and CSS, a sync
does not, and that difference has broken packages that worked fine on the dev box.

An architecture-independent package lands in two directories:

```
dist/
├── noarch/luci-app-mine-1.0-r1.apk
└── all/luci-app-mine_1.0-r1_all.ipk
```

`--layout flat` restores the old single-directory output.

<details>
<summary>Why the output is split by architecture</summary>

Artifacts go in a directory named for their architecture, because an apk's filename does not carry
one and everything downstream reads the directory. An architecture-independent package produces
both spellings: apk requires `noarch` and rejects `all`, and opkg has only ever known `all`. So one
build writes two directories.

That layout is [owfeed's artifact contract][artifact-contract]. It lets `owlab build` hand its
output straight to a publishing tool without either one knowing about the other.

</details>

[artifact-contract]: https://github.com/owfeed/owfeed/blob/main/docs/artifact-contract.md

### Check it in CI

```yaml
- uses: owfeed/owlab/action@v0.7.1
  with:
    releases: "25.12.5 24.10.8"
    install: dist/*/luci-app-mine-*
    assert: |
      http 200 /cgi-bin/luci/admin/services/mine
      service mined
      uci mine.@mine[0].enabled
```

- No `owlab.yaml` needed: the release numbers are the configuration.
- No setup step needed: the action installs the owlab binary itself.
- The job fails if the package does not install, the page does not render or the service is not
  running. The summary says which router and which check.

The same thing locally, and what the action runs:

```console
$ owlab test --release 25.12.5 --release 24.10.8 \
    --install 'dist/*/luci-app-mine-*' \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

- It starts, installs, asserts, tears down, and exits 0 or 1.
- `--keep` leaves the routers up. `--json` writes a report to stdout.
- owlab prints the log of any router that failed while the container still exists.
- Full assertion list and the JSON shapes: [docs/reference.md](docs/reference.md#owlab-test).

<details>
<summary>Two things it does that a hand-written <code>curl</code> does not</summary>

It **logs into LuCI as root** first. Every page an app exists to serve is under
`/cgi-bin/luci/admin`, and an unauthenticated request there is a redirect to the login form. That is
why the obvious check reports 403 on a healthy page.

It **fails a 200 whose body is an error page**. When LuCI's dispatcher catches an exception it still
answers 200, with the trace in the body.

</details>

### Check it installs from a feed

```console
$ owlab test --release 25.12.5 \
    --feed 'https://example.org/releases/25.12/x86_64/packages.adb' \
    --feed-key ./myfeed.pem \
    --install my-app \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

- A name installed through `--feed` gets no `--allow-untrusted`, so the index's signature has to
  check out. On apk, a file given to `--install` is installed with that flag, because a locally built
  package carries no signature the router knows.
- For opkg the key file's *name* must be the key id, because opkg looks the key up by it.

<details>
<summary>Why test from a feed when a file install passes</summary>

Installing a file proves the package works. Installing it **by name** out of a signed index proves
the channel works. The index parses, the URL does not redirect, and the key on the router matches
the one that signed it. A file install cannot fail in any of those ways, so it cannot detect them.

</details>

### Get onto the router

```console
$ owlab shell owrt2512
$ owlab exec owrt2512 -- 'logread | tail -20'
$ owlab logs owrt2512 -f
$ owlab open owrt2512
```

`ssh -p 2222 root@localhost` works too. owlab installs every `~/.ssh/id_*.pub` it finds. Point it
elsewhere with `OWLAB_PUBKEY=/path/to/key.pub`.

### Set a root password

```console
$ OWLAB_ROOT_PASSWORD=hunter2 owlab up --rebuild
```

Default is no password. That is fine for a box on localhost, and LuCI accepts the empty field.

### Keep the config somewhere else

```console
$ owlab --config ../my-package/owlab.yaml up
$ owlab --config ../my-package status        # a directory works too
$ export OWLAB_CONFIG=~/src/my-package
```

Without it, owlab looks in the working directory and its parents. [examples/](examples/) has
configs you can point at directly.

### Find out if your pins are stale

```console
$ owlab releases
ROUTER          DISTRO    PINNED     NEWEST
owrt2512        openwrt   25.12.4    25.12.5    1 release behind
owrt2410        openwrt   24.10.8    24.10.8    up to date
```

- `owlab releases --all` lists everything the download servers publish.
- Pin exact point releases and move them deliberately. A rootfs and its feed have to name the same
  release. Mixing them fails every install with `breaks: world[...]`.

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

- Every command takes router ids. With none, it acts on all of them.
- `--config <path>` (or `-c`) works on any of them, before or after the command.
- `test`, `status` and `releases` take `--json`.

## When something breaks

1. Run `owlab doctor`. It checks ports, ssh keys, line endings, QEMU, and whether your engine
   forwards DNS.
2. Look up the symptom in [docs/troubleshooting.md](docs/troubleshooting.md). Most failures here
   look like something else: a router that answers LuCI but resets every connection, a package that
   installs and does nothing, radios that exist but never come up. The page sorts them by symptom.
3. For procedures, see [docs/runbook.md](docs/runbook.md): each one is a goal, the commands, and
   how to tell it worked.

## Safety notes

- Log in as `root` with an empty password unless you set one.
- The ssh host keys are baked into the image and shared by everyone using it. Do not put these
  routers on a network you care about.
- Pin exact point releases. `snapshot` images and `snapshot` feeds are rebuilt daily and
  independently, so installs start failing within a day.

## What this sits next to

- **[containerlab](https://containerlab.dev/)** builds network topologies: many nodes, links
  between them. It supports OpenWrt as a node kind. owlab builds one router you develop against:
  no topology, but `sync` with hot reload, fixtures, the SDK build and `test`. Two routers running
  BGP at each other is containerlab. The same LuCI page open on 24.10 and 25.12 while you edit it is
  owlab.
- **[owfeed](https://github.com/owfeed/owfeed)** publishes packages. It builds, signs and indexes an
  apk feed, and `owfeed smoke` installs the result on a real OpenWrt image before you ship it.

<details>
<summary>How owlab and owfeed stay independent</summary>

`owfeed smoke` also starts a container, and the resemblance is deliberate: owlab is the development
cycle, `owfeed smoke` is one gate before a publish. They are independent on purpose, so that "will
this feed install" never depends on whether owlab is installed correctly.

They compose anyway, through a file format instead of a dependency. `owlab build` writes
`dist/<arch>/` and every owfeed stage reads it. owlab holds no keys at any point, and that is the
whole of its side of the boundary. It asserts that a package works, and never that anyone should
trust it.

[ECOSYSTEM.md](https://github.com/owfeed/owfeed/blob/main/docs/ECOSYSTEM.md) is where that
boundary is written down, along with the contracts across it. [docs/STATUS.md](docs/STATUS.md)
says how much of owlab's side of it exists.

</details>

## Docs

- [docs/](docs/) has the rest, in English and Russian: the runbook, the troubleshooting log, the
  config reference, how it all works, and how releases are cut.
- [CONTRIBUTING.md](CONTRIBUTING.md) is what to know before changing something.
- [SECURITY.md](SECURITY.md) is what these routers are and are not.

## Licence

GPL-2.0-only, deliberately. Calling owlab from your CI, your Makefile or your own tool imposes
nothing on your code. The obligation attaches to distributing a modified owlab.

<details>
<summary>Why GPL-2.0-only</summary>

[owfeed](https://github.com/owfeed/owfeed), by the same author, is Apache-2.0. owlab embeds a
`/etc/uci-defaults` and `rc.local` overlay written against OpenWrt's own shell libraries. It ships
that overlay inside every image it builds, and that is derived work.

</details>

OpenWrt is a registered trademark of the Software Freedom Conservancy. owlab is not affiliated with
or endorsed by the OpenWrt project or the SFC.
