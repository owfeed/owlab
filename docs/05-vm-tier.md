# The VM tier

A real OpenWrt kernel, booted by QEMU as an ordinary process on the developer's
machine.

## Why not QEMU in a container

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

## Choosing the accelerator

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

## Images

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

## Disks

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

## Networking

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

## Lifecycle

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

## Provisioning

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

## Radios

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
