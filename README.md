# owlab

Dev routers for OpenWrt and ImmortalWrt package development. One file, one
command, working routers — on Linux, WSL2, Docker Desktop for Windows, or macOS.

```console
$ go install github.com/VizzleTF/owlab/cmd/owlab@latest
$ owlab doctor
```

You need Docker (or OrbStack, Colima, Podman, Rancher Desktop) with Compose v2.
For `fidelity: vm` you need QEMU as well: `brew install qemu`, or
`apt install qemu-system-arm qemu-system-x86`.

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

### Install a package that is not in any feed

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
  build: ./build-css.sh htdocs/luci-static/mytheme/cascade.css
```

### Run something on the router after every sync

Registering the package, dropping a cache, restarting a service:

```yaml
project:
  post_sync: |
    uci -q set luci.themes.MyTheme=/luci-static/mytheme
    uci -q commit luci
```

### Work on a theme

```yaml
project:
  theme: mytheme
```

owlab selects it after each sync. Without this a theme is registered but not
shown.

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
$ owlab install owrt2512 dist/luci-app-mine-1.0-r1.apk
```

Uses OpenWrt's SDK. Worth doing before a release: a real build minifies JS and
CSS, which a sync does not, and that difference has broken packages that worked
fine on the dev box.

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
owlab logs        boot and service log
owlab status      what is running and where
owlab open        open LuCI in a browser
owlab doctor      check this machine
```

Every command takes router ids. With none, it acts on all of them.

## When something breaks

Run `owlab doctor` first. It checks ports, ssh keys, line endings, QEMU, and
whether your engine forwards DNS.

Then `docs/06-findings.md`. Most things that go wrong here look like something
else entirely: a router that answers LuCI but resets every connection, a
package that installs and does nothing, radios that exist but never come up.
They are all in there, sorted by symptom.

## Notes

Log in as `root` with an empty password unless you set one.

The ssh host keys are baked into the image and shared by everyone using it. Do
not put these routers on a network you care about.

Pin exact point releases. `snapshot` images and `snapshot` feeds are rebuilt
daily and independently, so installs start failing within a day.

## Docs

[docs/](docs/) has the rest: how owlab works, the two tiers, networking,
packages, the VM tier, and the findings log.

## Licence

GPL-2.0-only.

OpenWrt is a registered trademark of the Software Freedom Conservancy. owlab is
not affiliated with or endorsed by the OpenWrt project or the SFC.
