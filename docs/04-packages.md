# Packages

## The stock set

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

## Arithmetic

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

## `extra_packages` — anything not in a feed

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

## Feeds

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

## apk and opkg

The manager is a property of the distribution, not a function of the version.
OpenWrt switched in 25.12; a fork can track 25.12 and still build with opkg
(Kwrt does, with `# CONFIG_USE_APK is not set`). So the precedence is: what the
config says, then what the distribution pins, then the OpenWrt version rule.

Deriving it from the release number alone would hand such a router apk commands
and fail every install.
