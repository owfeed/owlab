# Configuration reference

[Русская версия](reference_ru.md)

Every key of `owlab.yaml`, every environment variable, and the flags of `owlab exec` and `owlab test`.
For someone who has an owlab project and needs to look up one value.

- Steps for a task ("how do I do X") are in the [runbook](runbook.md).
- The reasons behind the defaults are in [internals](internals.md).
- A router needs only `id` and `release`. Everything else has a default.
- A package list with `+`/`-` entries is applied on top of the defaults. A list without any prefix
  replaces them.

## Find it fast

| You want | Section |
|---|---|
| A whole `owlab.yaml` to copy | [Example owlab.yaml](#example-owlabyaml) |
| One key, its values and default | [owlab.yaml fields](#owlabyaml-fields) |
| File permissions after a sync | [What `sync` copies](#what-sync-copies-and-with-what-permissions) |
| What a router has installed by default | [The stock package set](#the-stock-package-set) |
| A package that did not install | [When a package does not install](#when-a-package-does-not-install) |
| A package from a URL | [Packages that are in no feed](#packages-that-are-in-no-feed) |
| Config that makes LuCI pages render | [Fixtures](#fixtures) |
| Faster builds from a published image | [Prebuilt images](#prebuilt-images) |
| `OWLAB_*` variables | [Environment](#environment) |
| Flags for CI runs | [owlab test](#owlab-test) |
| GitHub Actions inputs | [In GitHub Actions](#in-github-actions) |

## Example owlab.yaml

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
    release: "25.12.5"
    packages: ["+luci-app-sqm"]   # + adds to defaults, - removes
    ports: { http: 8025, ssh: 2225 }

  - id: real                      # fidelity vm only:
    fidelity: vm
    release: "25.12.5"
    memory: 512M                  # qemu -m
    cpus: 2                       # qemu -smp
    disk: 2G                      # extroot disk; 0 to use the image's own
    ports: { http: 8090, ssh: 2290 }
```

## owlab.yaml fields

`defaults:` takes every router key. Its values are merged into every router, and a router's own value
wins.

| Field | What it does | Values | Default | Example |
|---|---|---|---|---|
| `version` | Config format version | `1` | `1` | `version: 1` |
| `project.name` | Project name | string | the directory name | `luci-theme-example` |
| `project.theme` | Theme to activate on first boot and after every sync. See [Themes](#themes-need-one-more-step) | media directory name | none | `footstrap` |
| `project.install` | Maps a source directory to a destination on the router. See [What `sync` copies](#what-sync-copies-and-with-what-permissions) | map of directory → path | luci.mk's own mapping | `htdocs: /www` |
| `project.build` | Shell command line run on the host, in the project directory, before every sync | string | none | `./build-css.sh …` |
| `project.post_sync` | Shell command line run on each router after the files arrive | string | none | `uci -q commit luci` |
| `routers[].id` | Router name used by every command | string | required | `owrt2512` |
| `distro` | Distribution | `openwrt`, `immortalwrt` | `openwrt` | `immortalwrt` |
| `release` | Exact point release. See [Pin point releases](#pin-point-releases) | point release or `snapshot` | required | `"25.12.5"` |
| `arch` | Architecture. See [Architecture](#architecture) | `auto` or an OpenWrt package architecture | `auto` | `x86_64` |
| `fidelity` | Container or QEMU virtual machine | `basic`, `vm` | `basic` | `vm` |
| `image` | Prebuilt image to start from. See [Prebuilt images](#prebuilt-images) | image reference | owlab's published image | `ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.5-x86_64` |
| `packages` | Feed packages. See [The stock package set](#the-stock-package-set) | list; `+name` adds, `-name` removes, no prefix replaces | the stock set | `["-ppp", "+luci-app-sqm"]` |
| `extra_packages` | Packages downloaded by URL. See [Packages that are in no feed](#packages-that-are-in-no-feed) | list of `name`, `apk`, `ipk` | none | see the section |
| `fixtures` | Invented config profiles. See [Fixtures](#fixtures) | profile names, `lived-in`, `all`, `none` | `lived-in` | `[all]` |
| `ports` | Host ports forwarded to the router | `http`, `ssh` (integers) | `8080` and `2222` plus the router's index in the list | `{ http: 8025, ssh: 2225 }` |
| `hostname` | Router hostname | string | `id` with `_` and `.` replaced by `-` | `owrt2512` |
| `package_manager` | Overrides the package manager | `apk`, `opkg` | derived from the distribution and the release | `opkg` |
| `memory` | VM memory, passed to `qemu -m`. `fidelity: vm` only | size | `512M` | `512M` |
| `cpus` | VM CPUs, passed to `qemu -smp`. `fidelity: vm` only | integer | `2` | `2` |
| `disk` | Size of a second disk mounted as `/overlay` (extroot). `fidelity: vm` only | size, or `0` / `none` to use the image's own overlay | `2G` | `2G` |
| `radios` | Number of `mac80211_hwsim` radios. `fidelity: vm` only | integer; `0` for no wireless | `2` | `2` |

## What `sync` copies, and with what permissions

- **Mapping.** `project.install` maps a source directory to a destination on the router. The default
  is luci.mk's own mapping. A package laid out the upstream way needs no `install:` block.
- **Never synced.** `/etc/config/` and `/etc/uci-defaults/` are skipped on purpose.
- **`0755`.** Anything under `/bin`, `/sbin`, `/usr/bin`, `/usr/sbin`, `/usr/libexec`, `/etc/init.d`,
  `/etc/rc.d`, `/etc/hotplug.d` or `/etc/cron.d`, and anything starting with `#!`.
- **`0644`.** Everything else.

Permissions come from the destination and the file's contents, never from the file on your disk.
These are luci.mk's own two modes, so a synced tree has the permissions the built package would have
installed.

<details>
<summary>Why <code>/etc/config/</code> is skipped and permissions are derived</summary>

`/etc/config/` and `/etc/uci-defaults/` are the router's state rather than the package's. A real
install ships them as conffiles and leaves an existing one alone. Overwriting them on every sync
would throw away whatever you configured on the router.

Deriving permissions rather than copying them makes a sync mean the same thing on every host.
Windows has no execute bit and reports every file as `0666`. A Windows drive mounted into WSL reports
every file as `0777`. Carrying either across gave a router that behaved differently depending on which
machine ran the sync.

Neither failure names permissions: procd reports a non-executable init script as a service that does
not exist.

</details>

## `project.build` and `project.post_sync`

```yaml
project:
  build: ./build-css.sh htdocs/luci-static/footstrap/cascade.css
  post_sync: |
    uci -q set luci.themes.Footstrap=/luci-static/footstrap
    uci -q commit luci
```

| Key | Runs where | Runs when | Shell |
|---|---|---|---|
| `build` | on the host, in the project directory | before every sync | `sh`; on Windows, Git for Windows' `sh.exe` or another POSIX shell on `PATH` |
| `post_sync` | on each router | after the files arrive, before LuCI's caches are dropped | the router's busybox ash |

- Both are shell command lines. The string goes to a shell and is not split on spaces, so a pipeline,
  a redirect or `&&` work.
- Nothing is substituted for `sh` on Windows. Handing a POSIX command line to `cmd.exe` would quietly
  run something else.
- `owlab doctor` reports whether that shell is there, and whether `build` names a program it can
  find.

## The stock package set

A router with no `packages:` at all gets what a shipping OpenWrt device has installed. It is not a
bare rootfs.

The set is OpenWrt's own `DEFAULT_PACKAGES` and `DEFAULT_PACKAGES.router` from `include/target.mk`.
On top come the packages the firmware selector adds for a wireless router with LuCI:

```
base-files ca-bundle dropbear fstools libc libgcc logd mtd netifd uci
uclient-fetch urandom-seed urngd
dnsmasq firewall4 nftables kmod-nft-offload odhcp6c odhcpd-ipv6only ppp
ppp-mod-pppoe
wpad-basic-mbedtls wifi-scripts
luci luci-app-firewall luci-app-attendedsysupgrade luci-app-package-manager
```

Drop any of it with `-`:

```yaml
packages: ["-ppp", "-ppp-mod-pppoe", "+luci-app-sqm"]
```

Two departures from a real device's package list are deliberate:

- **`libustream-mbedtls` is not named**, though upstream's list has it.
- **Nothing board-specific is included:** `fitblk`, `uboot-envtools`, `kmod-usb3`, `kmod-leds-gpio`,
  `kmod-gpio-button-hotplug`, `kmod-crypto-hw-safexcel`, `kmod-mt7915e` and the mt7986 firmware. If a
  package you are developing needs one of them on disk to resolve a dependency, add it per project.

<details>
<summary>Why the set is named, and why these departures</summary>

Most of the set is already in an upstream rootfs tarball, so installing it is usually a no-op.
Naming it anyway makes the set a property of owlab rather than of whichever tarball a distribution
happens to publish. A fork with a thinner rootfs still gives you the same router.

The TLS backend is a build-time choice. Every image already carries whichever variant it was built
with, and asking ImmortalWrt 24.10 for the mbedtls one fails outright.

The board-specific packages describe one device's flash layout, bootloader, LEDs and radio silicon.
None can function without that hardware, and a container has no kernel of its own to load them into.

The wireless pair is in the set for a reason that is not about radios. LuCI gates its entire
Network → Wireless menu on `access('/sbin/wifi')`, which comes from `wifi-scripts`. It builds the
encryption matrix by asking `hostapd` what it supports. With both present the pages render from
config, with no radio present anywhere.

</details>

### When a package does not install

The set is installed in one call. The outcomes:

| Case | Result |
|---|---|
| The release's feeds do not carry a name | One-line skip, not an error. The name is dropped and the rest is asked for again in one call |
| On opkg, a package already on the router provides the name | Skip, dropped from the call the same way. apk counts the provider as the name by itself |
| The call fails for any other reason | Each package is installed on its own, so a failure costs that package and not the router |
| A package still does not install | Failure: a file clash, a dependency the feeds lack, or a download that still fails after 4 attempts, 2, 4 and 8 s apart (the retries owlab's own downloads use) |
| A name is not found after a refresh in which a feed did not answer | Failure, because that feed may carry it. The exception is a kmods index alone: it carries only `kmod-*` packages |

A skip for a name the feeds do not carry:

```
owlab: skip (not in this feed): luci-app-example
```

ImmortalWrt ships `dnsmasq-full`, which provides `dnsmasq`. Without the skip, opkg would install the
real `dnsmasq` beside it and refuse on a file clash:

```
owlab: skip (provided by dnsmasq-full, already installed): dnsmasq
```

On a failure:

- owlab prints the package manager's output, and the build carries on.
- The name is recorded on the router in `/etc/owlab/packages-failed`.
- `owlab up` names it after its table and exits 1.
- `owlab test` fails that router on its `packages` step.

```
! owrt2410 is running WITHOUT luci-app-example
```

A plain `owlab up` reuses the cached build and reports the same names again. `owlab up --rebuild
<router>` builds that router from scratch.

## Packages that are in no feed

`extra_packages:` installs a package from a URL. Use it for your own package and for anything
published on GitHub Releases. Everything in OpenWrt's own feeds goes in `packages:`.

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

| Key | What it is | Default |
|---|---|---|
| `name` | Name for messages and for the downloaded file | the basename of whichever URL is used |
| `apk` | URL used on releases that ship apk (25.12 and later) | none |
| `ipk` | URL used on releases that ship opkg (24.10 and earlier) | none |

- **Two URLs.** The two package managers do not share a naming scheme. The same release publishes
  `luci-theme-footstrap-0.11.5-r1.apk` and `luci-theme-footstrap_0.11.5-r1_all.ipk`.
- **One per router.** owlab picks the right one and never hands a `.apk` to a 24.10 box. Either key may
  be omitted. A router whose manager has no build is told so and carries on.
- **Downloads happen on the host** and are cached in `.owlab/cache/`, so `up` does not re-fetch them.
- **Only the routers being built are fetched for.** `owlab up owrt2512` downloads owrt2512's packages
  and compliance files and nothing for the other routers in `owlab.yaml`. A package only a 24.10 router
  installs cannot fail it.
- **Retries.** A download that sends nothing for 60 seconds, or breaks off halfway, starts again from
  the first byte, 4 attempts in all.
- **`--force-overwrite`.** These packages are installed with it. Without it the same config produces
  a different router on OpenWrt 24.10 than on the other three.

**These are installed without signature verification.** Treat anything installed this way as
trusted-by-URL. Pin an exact release rather than a `latest` link, and do not copy the pattern into
anything that ships.

### When an extra package fails

A failure here is neither passed over nor fatal:

1. The set is installed together.
2. If that failed, owlab installs one file at a time, so the good ones still land.
3. `owlab up` names whatever is still missing after its table and exits non-zero. The lab is running,
   and it is not what your config describes.
4. `owlab test` fails on it.
5. The names are left on the router in `/etc/owlab/extras-failed`.

<details>
<summary>Why on the host, why not fatal, why <code>--force-overwrite</code>, why no signatures</summary>

Downloads have to happen on the host: a stock OpenWrt rootfs has no `curl`, and its busybox `wget`
cannot do TLS.

A name in `packages:` may be missing from a particular feed. These packages are named by URL instead:
you said "install this file", so owlab will not report success without one.

owlab will not fail the build either. Every router in a lab is built in one buildkit solve, and one
failing target cancels all the others. A single package would take the whole lab down.

Their dependencies routinely replace a file the stock image already owns. A package that depends on
`dnsmasq-full` pulls it in, and `dnsmasq-full` ships `/etc/init.d/dnsmasq` and collides with
`dnsmasq`.

Projects publishing this way usually sign with usign and ship a `.sig` beside the artifact. Checking
it needs their public key: a trust decision a dev container should not be making on your behalf.

</details>

### Themes need one more step

Installing a theme package only *registers* it. It adds `luci.themes.<Name>` and leaves
`luci.main.mediaurlbase` alone.

Set `project.theme` and owlab activates the theme:

- on first boot, after the package's own uci-defaults have run;
- after every sync.

The value is the media directory name: `footstrap` for `/www/luci-static/footstrap`. If that
directory does not exist, owlab says so in the boot log. It does not point `mediaurlbase` at nothing.

<details>
<summary>Why a theme needs activating</summary>

A package has no business changing what the user is looking at. On a dev box that is backwards: the
theme is there to be looked at.

LuCI resolves templates through `mediaurlbase`, so a wrong value crashes the dispatcher instead of
falling back.

</details>

## Fixtures

Fixture profiles invent the config that makes LuCI render sections, tabs, tables, badges and forms.

```yaml
defaults:
  fixtures: [all]
```

| profile | what it adds |
|---|---|
| `networks` | a WAN, a guest bridge, two VLANs, a disabled interface, static routes, DHCP pools, firewall zones, and a four-port "Port status" card |
| `clients` | static leases, local hostnames, and at boot the DHCP leases and ARP neighbours that put rows in the data tables |
| `portforwards` | port forwards, one of them disabled |
| `system` | LEDs, mounts, swap, cron entries |
| `wireguard` | a wg0 tunnel with two peers and its zone |
| `wifi` | radios that render on a box with no radios |

| alias | means |
|---|---|
| `lived-in` | `networks clients portforwards system`. The default |
| `all` | every profile |
| `none` | a bare router |

- Dependencies are pulled in automatically: `fixtures: [portforwards]` also gets `networks`.
- `wifi` is opt-in, because it claims `/etc/config/wireless` outright.
- **`wifi` on a container:** the menu, the radio rows, the SSIDs, the modes and the whole edit form
  render, with no kernel wireless support. Signal, scan results and the associated-stations table stay
  empty. Those need real radios, which means `fidelity: vm`.
- **`wifi` on a VM:** if real phys are present, it deletes the invented config and runs OpenWrt's own
  `wifi config` against them.

**Nothing in a fixture may touch `lan`.** That is eth0, with the container engine's own address on it,
and it is the only way in. There is no console to recover from. Every invented network sits on a dummy
device or a VLAN that carries no traffic.

<details>
<summary>Why fixtures exist, and how <code>wifi</code> works</summary>

LuCI renders almost nothing on its own. The sections, tabs, tables, badges and forms a theme or an app
exists to style appear only when there is config behind them. A container with one interface and no
clients shows a fraction of the widget surface, and a whole class of styling bugs cannot be seen on
it.

LuCI's wireless pages are almost entirely config-driven. The menu appears when `/sbin/wifi` exists,
the radio list comes straight from UCI, and the encryption capability matrix is built by asking the
`hostapd` binary what it supports.

On a VM, writing the invented sections would be strictly worse than doing nothing. netifd matches a
`wifi-device` to a phy by its `path`. The invented paths name hardware that is not there. The real
radios would sit unclaimed while LuCI drew rows for radios that do not exist.

</details>

## Pin point releases

Write `release: "25.12.5"`, not `"25.12"`. The rootfs and its feed must name the same release.

- The package manager records hard version pins (`base-files=1707~4ccb782af7` in `/etc/apk/world`).
  A 25.12.4 rootfs pointed at the 25.12.5 feed fails every install with `breaks: world[...]`.
- owlab pins the feed to the exact release the rootfs came from. Old point releases stay available
  upstream, so this always works.
- `release: "snapshot"` is supported but not recommended. Snapshot rootfs images and snapshot feeds
  are rebuilt daily and independently, and drift apart within a day. `owlab doctor` warns about it.

## Prebuilt images

Assembling a router from the upstream rootfs takes about a minute, most of it installing the LuCI set.
Starting from a published image takes seconds:

```yaml
routers:
  - id: owrt2512
    release: "25.12.5"
    arch: x86_64
    image: ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.5-x86_64
```

Everything else still applies on top:

- your `packages:` are installed (the package manager reports the ones already there as up to date);
- fixtures are re-applied;
- the theme is set.

A prebuilt image is an accelerator, not a different code path. Nothing behaves differently from a
locally built one.

### When the registry does not answer

Before a build, owlab asks the image's registry whether the image is there: a manifest `HEAD`, which
Docker Hub does not count as a pull.

| Registry | Download server | Result |
|---|---|---|
| does not answer (a refused connection, a timeout, or a 5xx or 429 through 4 attempts) | has the release's rootfs tarball | built from the tarball, and owlab says so. Holds for the default image and for `image:` alike |
| reports the tag missing (404), default image | has the tarball | built from the tarball |
| reports the tag missing (404), `image:` | any | the build fails: a name you wrote that does not exist is a mistake to fix |
| does not answer | does not answer either | the image stays |

<details>
<summary>Why the tarball is a safe fallback, and why the image stays offline</summary>

The tarball is the same rootfs, and the tarball path pulls nothing from any registry.

A machine that reaches neither server is offline, or reaches the internet only through the engine's
own proxy or mirror. The engine may still pull the image, or build from the image and layers it
already holds. The tarball could not be downloaded at all.

</details>

### Published tags

Tags are `<distro>-<release>-<arch>`:

```
ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.5-x86_64
ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.5-aarch64_generic
ghcr.io/owfeed/owlab-rootfs:openwrt-24.10.8-x86_64
ghcr.io/owfeed/owlab-rootfs:immortalwrt-25.12.1-x86_64
...
```

- **Moving tag.** It is rebuilt weekly and picks up feed changes within the same release.
- **Versioned tag.** A tag with a `-v0.1.0` suffix keeps the same bytes. Use it when you need the same
  bytes twice.
- **One tag is one target.** There is no multi-arch manifest.
- **Provenance.** Each image carries it at `/usr/share/owlab/compliance/`: the package manifest, a
  CycloneDX SBOM with per-package licences, and the buildinfo files pinning the tree and every feed to
  an exact commit.

<details>
<summary>Why there is no multi-arch manifest, and why owlab ships compliance files</summary>

An OCI image index has no field for "distro version". Most OpenWrt architecture names
(`aarch64_generic`, `mips_24kc`) have no valid GOARCH mapping, so a manifest list cannot express this
matrix. Upstream does not try either.

The compliance material exists upstream but is *not* inside the rootfs. The tarball ships no licence
texts and no copyright notices at all. owlab assembles it at build time rather than leaving every
downstream user to reconstruct it.

</details>

## Architecture

`arch: auto` picks whatever runs natively: `aarch64_generic` on Apple Silicon, `x86_64` on Intel and
AMD. Anything else runs under emulation, which works but is slow enough to notice. `owlab doctor` warns
when you have asked for it.

| Architectures | Commands |
|---|---|
| `x86_64`, `i386_pentium4`, `aarch64_generic`, `arm_cortex-a15_neon-vfpv4`, `arm_cortex-a9_vfpv3-d16`, `mips_24kc`: the targets upstream publishes a rootfs for | every command |
| any other OpenWrt package architecture (`aarch64_cortex-a53`, `mipsel_24kc`, …) | `owlab build` only |

- `owlab build` uses `openwrt/sdk:<arch>-<release>`, which upstream maps to one board of that arch.
  That works for userland packages, not for kernel modules of a different board.
- `up`, `test` and every other command that starts a router refuse a build-only router by name. When
  no router ids are given, they skip it.

```yaml
routers:
  - id: filogic
    release: "24.10.8"
    arch: aarch64_cortex-a53   # owlab build only
```

<details>
<summary>Why other architectures are build-only</summary>

Real boards ship firmware images and an SDK, but nothing a container or a VM can start.

</details>

## Environment

A working machine needs none of these. Each one covers a case owlab cannot detect: an install somewhere
unusual, or a host whose accelerator misbehaves.

| Variable | What it does | Default |
|---|---|---|
| `OWLAB_CONFIG` | the project to act on | search the working directory and its parents |
| `OWLAB_PUBKEY` | the ssh public key(s) to install | every `~/.ssh/id_*.pub` |
| `OWLAB_ROOT_PASSWORD` | set a root password at build time | no password |
| `OWLAB_CACHE` | where downloaded VM images live | the user cache directory |
| `OWLAB_QEMU` | the `qemu-system-*` binary to boot with | searched for, see below |
| `OWLAB_QEMU_IMG` | the `qemu-img` to create disks with | the one beside the emulator |
| `OWLAB_FIRMWARE` | the UEFI firmware for an aarch64 or armv7 VM | not stated |
| `OWLAB_ACCEL` | force an accelerator, for example `tcg` | the fastest one this host has |

- **`OWLAB_QEMU`** is rarely needed. owlab searches the directories the installers use, not PATH alone:
  `C:\Program Files\qemu` and the scoop and chocolatey locations on Windows, `/opt/homebrew/bin` on
  macOS. `owlab doctor` prints the binary it settled on.
- **`OWLAB_ACCEL=tcg`** is the escape hatch for a host whose hardware acceleration does not work.
  Translation is slower by roughly an order of magnitude and always works.

<details>
<summary>Why <code>OWLAB_ACCEL</code> exists</summary>

On Windows the problem is real. WHPX sits on top of Hyper-V, and it is reported to hang some machines on
an SMP boot while working on the next one over.

</details>

## owlab exec

`owlab exec <router> -- <command>` runs a command on a running router. What follows `--` is read one of
two ways, by the number of words:

| after `--` | runs as | example |
|---|---|---|
| one word | a shell script, as written | `owlab exec r -- 'ps \| grep uhttpd'` |
| two or more words | a command and its arguments, each quoted | `owlab exec r -- sh -c 'exit 3'` |

- **Pipe, redirect or `;`:** quote the whole command as one word.
- **An operator typed as a separate word** is passed to the command as an argument:
  `owlab exec r -- cat /etc/passwd '|' grep root` gives `|` to `cat`.
- **Exit status** is the command's own.
- **stdin** is passed through when it is a pipe or a file. stderr stays on stderr.

## owlab test

One command for CI: start the routers, install the package, assert against the running router, tear
everything down, exit 0 or 1.

```console
$ owlab test --release 25.12.5 --release 24.10.8 \
    --install 'dist/*/luci-app-mine-*' \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

| flag | what it does |
|---|---|
| `--release` | a router per release, with no `owlab.yaml` at all. Repeatable, or one value holding several: `--release "25.12.5 24.10.8"` |
| `--distro` | `openwrt` or `immortalwrt`, for the routers `--release` creates |
| `--arch` | architecture for those routers; the host's by default |
| `--packages` | packages for those routers; `+name` adds to the stock set |
| `--fixtures` | fixture profiles for those routers, for example `none` or `all` |
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

- **No `--release`:** the routers come from `owlab.yaml`, and router ids select among them the way they
  do everywhere else.
- **Teardown** happens whether the run passed or failed, on its own context.
- **The log of any router that failed** is printed first, while the container still exists.

<details>
<summary>Why teardown runs on its own context</summary>

A failed run that leaves containers holding 8080 and 2222 makes the *next* run fail for an unrelated
reason.

</details>

### Testing against a feed you are serving yourself

Write `{host}` in a `--feed` URL, and owlab substitutes the host machine's address for the tier:

```sh
python3 -m http.server 8080 --directory out &
owlab test --release 25.12.5 \
  --feed 'http://{host}:8080/releases/25.12/noarch/packages.adb' \
  --feed-key out/keys/feed.pem \
  --install luci-app-mine \
  --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

A URL without the token is passed through untouched. The same flag points at a published feed with
nothing else changed.

<details>
<summary>Why install by name, and why <code>{host}</code></summary>

Installing a built file proves the package works. Installing it *by name* proves the channel works:
the index parses, the URL does not redirect, and the key on the router matches the one that signed it.
A file install cannot fail those ways, which is why a feed's own CI wants the second.

That means serving the freshly built tree over HTTP from the machine running owlab, then telling the
router where that machine is. No address answers everywhere:

- a container on a Linux CI runner reaches the host at the bridge gateway;
- a container under Docker Desktop has no such gateway;
- a `fidelity: vm` router sees neither, because QEMU's user-mode stack puts the host somewhere else
  again.

Hardcoding `172.17.0.1` is the usual answer, and it breaks on the first developer machine.

</details>

### Assertions

| assertion | passes when |
|---|---|
| `http 200 /cgi-bin/luci/admin/services/mine` | the page answers with that status and is not an error page. `2xx` matches a class |
| `service mined` | `/etc/init.d/<name> running` says so, or ubus does |
| `package luci-app-mine` | the router's own package manager reports it installed |
| `uci mine.@mine[0].enabled` | the value is set. This checks that a package's `uci-defaults` ran |
| `file /usr/share/rpcd/acl.d/luci-app-mine.json` | the path exists on the router |
| `exec pgrep mined \| grep -q .` | the command exits 0 |

- Everything but `http` runs over the same transport `sync` and `exec` use. Assertions work identically
  on a container and on a `fidelity: vm` router.
- `http` logs in as root before fetching.
- `http` also fails a 2xx whose body carries a dispatcher error ("A runtime exception was caught", a
  lua `stack traceback:`).

<details>
<summary>Why <code>http</code> logs in and reads the body</summary>

Every page under `/cgi-bin/luci/admin` is a redirect to the login form without a session. That is why a
hand-written `curl -o /dev/null -w '%{http_code}'` reports 403 for a healthy page.

LuCI answers 200 when it catches an exception, so status alone calls a broken page healthy.

</details>

### The JSON report

`--json` puts the report on stdout and moves progress, including docker's own build output, to stderr.
`owlab test --json | jq` is a pipe.

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

Other commands with a `schema` field:

| Command | `schema` | Adds |
|---|---|---|
| `owlab status --json` | `owlab.status/v1` | the container name, and the two ports separately from the URL |
| `owlab releases --json` | `owlab.releases/v1` | `behind` as a number: `jq '[.routers[].behind] \| add'` is the whole of "is anything stale" |
| `owlab releases --json --all` | `owlab.releases.all/v1` | — |

### In GitHub Actions

```yaml
- uses: owfeed/owlab/action@v0.7.1
  with:
    releases: "25.12.5 24.10.8"
    install: dist/*/luci-app-mine-*
    assert: |
      http 200 /cgi-bin/luci/admin/services/mine
      service mined
```

- **Inputs.** Every flag above has an input with the same name, except `--release`: its input is
  `releases`, a space- or comma-separated list. `assert` and `install` take one per
  line.
- **Outputs.** `report` (the JSON path), `passed` and `failed`. The action also writes a table to the
  job summary.
- **Binary only.** `owfeed/owlab/setup@v0.7.1` installs the binary alone, for a job that drives `owlab`
  itself.
- **Verification.** Both verify the download against this repository's build attestation before the
  binary is executed or put on `PATH`.
- **Version.** The tag in `uses:` is the version. `action@v0.7.1` and `setup@v0.7.1` install owlab
  v0.7.1, so a dependabot bump of that line moves the binary too. Set `version:` only to run a different
  release.
- **Branch or SHA.** Called at a branch or a SHA, both install the latest release and say so in a
  warning.
- **Runners.** `ubuntu-latest` works as it is. On GitHub's macOS and Windows runners the action says
  there is no container engine, rather than failing later with something about a socket.

A ready-made workflow is in [examples/workflow/package-ci.yml](../examples/workflow/package-ci.yml).

<details>
<summary>Why macOS and Windows runners do not work</summary>

GitHub's macOS and Windows runners have no container engine that runs Linux images. Docker Desktop is
not installed there and cannot be. That is a property of the runners, not of owlab: on a developer's
own macOS or Windows machine, where an engine is installed, every command works.

</details>

#### Start a router from a saved image

A job that drives `owlab` itself can save the router image once and start later jobs from it. Building
a router is most of a job's network: the base image, every package from the feeds, the
`extra_packages` files.

```console
$ mkdir -p ~/.cache/owlab-stand
$ docker save -o ~/.cache/owlab-stand/owrt2512.tar owlab-<project>-owrt2512:latest
$ docker load -i ~/.cache/owlab-stand/owrt2512.tar
$ owlab up --no-build owrt2512
```

`--no-build` fetches and builds nothing. It starts the image the engine holds, waits for LuCI and
reports missing packages as a plain `owlab up` does.

It refuses:

- an image whose `org.owlab.inputs` label is not what this owlab and this `owlab.yaml` would build: one
  from another owlab, another router config, another `OWLAB_ROOT_PASSWORD` or a build from the rootfs
  tarball;
- an image with no label;
- a `fidelity: vm` router, which has no image.

A refusal over the image exits 1 and names `owlab up <id>`, which builds it. A script can fall back to
that.

The label does not cover the feeds: a saved image keeps the packages it was built with. Key the cache
by a date as well as by `owlab.yaml` and owlab's version, to bound how stale a router can get.

## Licence and trademarks

owlab is GPL-2.0-only.

owlab is not affiliated with, endorsed by, or sponsored by the OpenWrt project or the Software Freedom
Conservancy, which owns the OpenWrt trademark, nor by the ImmortalWrt project. OpenWrt and ImmortalWrt
are referred to here only to say what this tool works with.

Images built by owlab contain unmodified OpenWrt or ImmortalWrt software, which is licensed under the
GPL and other licences by its own authors.
