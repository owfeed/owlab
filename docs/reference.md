# Configuration reference

[Русская версия](reference.ru.md)

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

## Licence and trademarks

owlab is GPL-2.0-only.

owlab is not affiliated with, endorsed by, or sponsored by the OpenWrt
project or the Software Freedom Conservancy, which owns the OpenWrt trademark,
nor by the ImmortalWrt project. OpenWrt and ImmortalWrt are referred to here
only to say what this tool works with.

Images built by owlab contain unmodified OpenWrt or ImmortalWrt software,
which is licensed under the GPL and other licences by its own authors.

