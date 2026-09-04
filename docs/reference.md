# Configuration reference

[Русская версия](reference_ru.md)

Every key of `owlab.yaml`. For "how do I do X" see the [runbook](runbook.md);
for why the defaults are what they are, [internals](internals.md).

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
  packages: ["+luci-app-sqm"]     # on top of the stock set; see below

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

### What `sync` copies, and with what permissions

`project.install` maps a source directory to a destination on the router. The
default is luci.mk's own mapping, so a package laid out the upstream way needs
no `install:` block at all.

Two things in `/etc` are deliberately never synced: `/etc/config/` and
`/etc/uci-defaults/`. Those are the router's state rather than the package's —
a real install ships them as conffiles and leaves an existing one alone, and
overwriting them on every sync would throw away whatever you just configured.

Permissions come from the destination and the file's contents, never from the
file on your disk: `0755` for anything under `/bin`, `/sbin`, `/usr/bin`,
`/usr/sbin`, `/usr/libexec`, `/etc/init.d`, `/etc/rc.d`, `/etc/hotplug.d` or
`/etc/cron.d`, and for anything starting with `#!`; `0644` for everything else.
Those are luci.mk's own two modes, so a synced tree has the permissions the
built package would have installed.

Deriving them rather than copying them is what makes a sync mean the same thing
on every host. Windows has no execute bit and reports every file as `0666`; a
Windows drive mounted into WSL reports every file as `0777`. Carrying either
across gave a router that behaved differently depending on which machine ran
the sync, and neither failure names permissions: procd reports a
non-executable init script as a service that does not exist.

### `project.build` and `project.post_sync`

```yaml
project:
  build: ./build-css.sh htdocs/luci-static/footstrap/cascade.css
  post_sync: |
    uci -q set luci.themes.Footstrap=/luci-static/footstrap
    uci -q commit luci
```

`build` runs on the host before every sync, in the project directory.
`post_sync` runs on each router after the files arrive and before LuCI's caches
are dropped.

Both are shell command lines, not argument lists — a pipeline, a redirect or a
`&&` all work, because the string is handed to a shell rather than split on
spaces. `post_sync` runs under the router's busybox ash. `build` runs under
`sh`, which on Windows means Git for Windows' `sh.exe` or another POSIX shell
on `PATH`; nothing is substituted for it, since handing a POSIX command line to
`cmd.exe` would quietly run something else. `owlab doctor` reports whether that
shell is there, and whether `build` names a program it can find.

### The stock package set

A router with no `packages:` at all is not a bare rootfs — it is what a
shipping OpenWrt device has installed. The set is OpenWrt's own
`DEFAULT_PACKAGES` and `DEFAULT_PACKAGES.router` from `include/target.mk`, plus
what the firmware selector adds for a wireless router with LuCI:

```
base-files ca-bundle dropbear fstools libc libgcc logd mtd netifd uci
uclient-fetch urandom-seed urngd
dnsmasq firewall4 nftables kmod-nft-offload odhcp6c odhcpd-ipv6only ppp
ppp-mod-pppoe
wpad-basic-mbedtls wifi-scripts
luci luci-app-firewall luci-app-attendedsysupgrade luci-app-package-manager
```

Most of it is already in an upstream rootfs tarball, so installing it is
usually a no-op. Naming it anyway makes the set a property of owlab rather than
of whichever tarball a distribution happens to publish, so a fork with a
thinner rootfs still gives you the same router.

Drop any of it with `-`:

```yaml
packages: ["-ppp", "-ppp-mod-pppoe", "+luci-app-sqm"]
```

Two deliberate departures from a real device's package list. `libustream-mbedtls`
is not named even though upstream's list has it: the TLS backend is a build-time
choice, every image already carries whichever variant it was built with, and
asking ImmortalWrt 24.10 for the mbedtls one fails outright. And nothing
board-specific is included — `fitblk`, `uboot-envtools`, `kmod-usb3`,
`kmod-leds-gpio`, `kmod-gpio-button-hotplug`, `kmod-crypto-hw-safexcel`,
`kmod-mt7915e` and the mt7986 firmware describe one device's flash layout,
bootloader, LEDs and radio silicon. None can function without that hardware.
Add one per project if a package you are developing needs it on disk to
resolve a dependency.

The wireless pair is in the set for a reason that is not about radios: LuCI
gates its entire Network → Wireless menu on `access('/sbin/wifi')`, which comes
from `wifi-scripts`, and builds the encryption matrix by asking `hostapd` what
it supports. With both present the pages render from config, with no radio
present anywhere.

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
outright. What it achieves on a container is worth knowing: LuCI's wireless
pages are almost entirely config-driven — the menu appears when `/sbin/wifi`
exists, the radio list comes straight from UCI, and the encryption capability
matrix is built by asking the `hostapd` binary what it supports. So the menu,
the radio rows, the SSIDs, the modes and the whole edit form render correctly
with no kernel wireless support of any kind. Only signal, scan results and the
associated-stations table stay empty; those need real radios, which means
`fidelity: vm`.

On a VM the profile does something different, and checks rather than assumes:
if real phys are present it deletes the invented config and runs OpenWrt's own
`wifi config` against them. Writing the invented sections there would be
strictly worse than doing nothing — netifd matches a `wifi-device` to a phy by
its `path`, the invented paths name hardware that is not there, and the real
radios would sit unclaimed while LuCI drew rows for radios that do not exist.

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
    image: ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.4-x86_64
```

Everything else still applies on top — your `packages:` are installed (the
package manager reports the ones already there as up to date), fixtures are
re-applied, the theme is set. A prebuilt image is an accelerator, not a
different code path, so nothing behaves differently from a locally built one.

Published tags are `<distro>-<release>-<arch>`:

```
ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.4-x86_64
ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.4-aarch64_generic
ghcr.io/owfeed/owlab-rootfs:openwrt-24.10.8-x86_64
ghcr.io/owfeed/owlab-rootfs:immortalwrt-25.12.1-x86_64
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

## Environment

Nothing here is needed on a working machine. Each one exists for a case owlab
cannot detect: an install somewhere unusual, or a host whose accelerator
misbehaves.

| | |
|---|---|
| `OWLAB_CONFIG` | the project to act on, instead of searching the working directory and its parents |
| `OWLAB_PUBKEY` | the ssh public key(s) to install, instead of every `~/.ssh/id_*.pub` |
| `OWLAB_ROOT_PASSWORD` | set a root password at build time; the default is none |
| `OWLAB_CACHE` | where downloaded VM images live, instead of the user cache directory |
| `OWLAB_QEMU` | the `qemu-system-*` binary to boot with |
| `OWLAB_QEMU_IMG` | the `qemu-img` to create disks with; the default is the one beside the emulator |
| `OWLAB_FIRMWARE` | the UEFI firmware for an aarch64 or armv7 VM |
| `OWLAB_ACCEL` | force an accelerator, e.g. `tcg`, instead of the fastest one this host has |

`OWLAB_QEMU` is rarely needed now that owlab searches the directories the
installers use — `C:\Program Files\qemu` and the scoop and chocolatey
locations on Windows, `/opt/homebrew/bin` on macOS — rather than PATH alone.
`owlab doctor` prints the binary it settled on.

`OWLAB_ACCEL=tcg` is the escape hatch for a host whose hardware acceleration
does not work. That is not hypothetical on Windows: WHPX sits on top of
Hyper-V, and it is reported to hang some machines on an SMP boot while working
on the next one over. Translation is slower by roughly an order of magnitude
and always works.

## owlab test

One command for CI: start the routers, install the package, assert against the
running router, tear everything down, exit 0 or 1.

```console
$ owlab test --release 25.12.5 --release 24.10.8 \
    --install 'dist/*/luci-app-mine-*.apk' \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

| flag | what it does |
|---|---|
| `--release` | a router per release, with no `owlab.yaml` at all. Repeatable, or one value holding several: `--release "25.12.5 24.10.8"` |
| `--distro` | `openwrt` or `immortalwrt`, for the routers `--release` creates |
| `--arch` | architecture for those routers; the host's by default |
| `--packages` | packages for those routers; `+name` adds to the stock set |
| `--fixtures` | fixture profiles for those routers, e.g. `none` or `all` |
| `--install` | a path (globs expanded) is pushed to the router and installed from there; anything else is looked up in the feeds. Repeatable. Each router gets only the format its package manager reads — `.apk` on 25.12+, `.ipk` on 24.10 and earlier — so one glob can cover both release lines. A file left out is named on the router's install line |
| `--assert` | one assertion, repeatable. Every one runs on every router |
| `--feed` | add a package feed before installing, so a name is resolved out of a signed index instead of a file. The index URL for apk, the directory URL for opkg. Goes with `--feed-key` |
| `--feed-key` | the feed's public key file. For opkg the *filename* must be the key id, because that is what opkg looks it up by |
| `--feed-name` | what the feed is registered as on the router; `owlab-feed` by default |
| `--sync` | sync the project sources in first, as `owlab sync` does, instead of (or as well as) installing a built package |
| `--keep` | leave the routers running afterwards |
| `--rebuild` | build the images from scratch |
| `--json` | write the report to stdout and everything else to stderr |
| `--timeout` | budget for the whole run; default 20m |

With no `--release` the routers come from `owlab.yaml`, and router ids select
among them the way they do everywhere else.

Teardown happens whether the run passed or failed, and on its own context — a
failed run that leaves containers holding 8080 and 2222 makes the *next* run
fail for an unrelated reason. The log of any router that failed is printed
first, while the container still exists.

### Testing against a feed you are serving yourself

Installing a built file proves the package works. Installing it *by name* proves
the channel works — that the index parses, the URL does not redirect, and the key
on the router matches the one that signed it. A file install cannot fail those
ways, which is why a feed's own CI wants the second.

That means serving the freshly built tree over HTTP from the machine running
owlab, and then telling the router where that machine is. There is no address
that answers everywhere: a container on a Linux CI runner reaches the host at the
bridge gateway, a container under Docker Desktop has no such gateway, and a
`fidelity: vm` router sees neither because QEMU's user-mode stack puts the host
somewhere else again. Hardcoding `172.17.0.1` is the usual answer and it breaks
on the first developer machine.

Write `{host}` instead and owlab substitutes whatever is correct for the tier:

```sh
python3 -m http.server 8080 --directory out &
owlab test --release 25.12.5 \
  --feed 'http://{host}:8080/releases/25.12/noarch/packages.adb' \
  --feed-key out/keys/feed.pem \
  --install luci-app-mine \
  --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

A URL without the token is passed through untouched, so the same flag points at a
published feed with nothing else changed.

### Assertions

| assertion | passes when |
|---|---|
| `http 200 /cgi-bin/luci/admin/services/mine` | the page answers with that status and is not an error page. `2xx` matches a class |
| `service mined` | `/etc/init.d/<name> running` says so, or ubus does |
| `package luci-app-mine` | the router's own package manager reports it installed |
| `uci mine.@mine[0].enabled` | the value is set — which is how you check that a package's `uci-defaults` really ran |
| `file /usr/share/rpcd/acl.d/luci-app-mine.json` | the path exists on the router |
| `exec pgrep mined \| grep -q .` | the command exits 0 |

Everything but `http` runs over the same transport `sync` and `exec` use, so
assertions work identically on a container and on a `fidelity: vm` router.

`http` logs in as root before fetching, because every page under
`/cgi-bin/luci/admin` is a redirect to the login form without a session — the
reason a hand-written `curl -o /dev/null -w '%{http_code}'` reports 403 for a
healthy page. It also fails a 2xx whose body carries a dispatcher error
("A runtime exception was caught", a lua `stack traceback:`): LuCI answers 200
when it catches an exception, so status alone calls a broken page healthy.

### The JSON report

`--json` puts the report on stdout and moves progress — including docker's own
build output — to stderr, so `owlab test --json | jq` is a pipe.

```json
{
  "schema": "owlab.test/v1",
  "owlab": "0.2.0",
  "project": "luci-app-mine",
  "ok": false,
  "routers": [
    {
      "id": "openwrt-24.10.8",
      "distro": "openwrt",
      "release": "24.10.8",
      "arch": "x86_64",
      "fidelity": "basic",
      "package_manager": "opkg",
      "luci": "http://localhost:8081",
      "ok": false,
      "steps": [
        { "check": "boot", "kind": "up", "ok": true, "seconds": 11.2 },
        { "check": "install luci-app-mine_1.0_all.ipk", "kind": "install", "ok": false,
          "detail": "* satisfy_dependencies_for: Cannot satisfy the following dependencies", "seconds": 3.1 }
      ]
    }
  ]
}
```

`owlab status --json` and `owlab releases --json` carry a `schema` field too
(`owlab.status/v1`, `owlab.releases/v1`, and `owlab.releases.all/v1` for
`--all`). `status` adds the container name and the two ports separately from
the URL; `releases` adds `behind` as a number, so `jq '[.routers[].behind] |
add'` is the whole of "is anything stale".

### In GitHub Actions

```yaml
- uses: owfeed/owlab/action@v0.5.6
  with:
    releases: "25.12.5 24.10.8"
    install: dist/*/luci-app-mine-*.apk
    assert: |
      http 200 /cgi-bin/luci/admin/services/mine
      service mined
```

Every flag above has an input with the same name; `assert` and `install` take
one per line. The action writes a table to the job summary and exposes
`report` (the JSON path), `passed` and `failed` as outputs.
`owfeed/owlab/setup@v0.5.6` installs the binary alone, for a job that drives
`owlab` itself. Both verify the download against this repository's build
attestation before the binary is executed or put on `PATH`.

`ubuntu-latest` works as it is. GitHub's macOS and Windows runners have no
container engine that runs Linux images — Docker Desktop is not installed there
and cannot be — and the action says so rather than failing later with something
about a socket. That is a property of the runners, not of owlab: on a
developer's own macOS or Windows machine, where an engine is installed, every
command works.

A ready-made workflow is in
[examples/workflow/package-ci.yml](../examples/workflow/package-ci.yml).

## Licence and trademarks

owlab is GPL-2.0-only.

owlab is not affiliated with, endorsed by, or sponsored by the OpenWrt
project or the Software Freedom Conservancy, which owns the OpenWrt trademark,
nor by the ImmortalWrt project. OpenWrt and ImmortalWrt are referred to here
only to say what this tool works with.

Images built by owlab contain unmodified OpenWrt or ImmortalWrt software,
which is licensed under the GPL and other licences by its own authors.

