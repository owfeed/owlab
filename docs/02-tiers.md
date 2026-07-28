# Tiers

Two: `basic` and `vm`. Chosen per router, because most LuCI work needs nothing
more than the cheap one and the expensive one is not free.

## `basic` — a container

A stock rootfs for the release, with procd as PID 1.

`CMD ["/sbin/init"]` is not negotiable. Running `uhttpd` directly instead of
the init system makes LuCI crash with `left-hand side expression is null` at
`runtime.uc:133` (openwrt/luci#8726) — the dispatcher reads board data from
procd over ubus, and there is no shortcut. The init system is the product.

What works: ubus, rpcd, uhttpd, netifd, uci, the whole of LuCI, `apk` and
`opkg`, nftables and fw4 (measured: 311 rules on OpenWrt, 324 on ImmortalWrt),
and whatever netfilter features the *engine's* kernel happens to have — tproxy
does work on OrbStack and on most Linux hosts.

What does not: loading kernel modules, `sysupgrade`, real radios, DSA. See
[06 — Findings](06-findings.md) for the two container-specific behaviours that
bite hardest (procd's jails, and outbound UDP).

## `vm` — a real OpenWrt kernel

QEMU as an ordinary host process, not inside a container.

That is the whole design. Nested QEMU needs `/dev/kvm` passed into the
container, which Docker Desktop grants on neither macOS nor Windows — exactly
the hosts that need this tier most. Run natively the problem inverts: on Apple
Silicon the guest is aarch64, the accelerator is hvf, and a router boots in
nine seconds with no nested virtualisation, no minimum macOS version and no
special hardware.

What it adds:

- **Kernel modules load.** `apk add kmod-nft-tproxy` and `lsmod` shows
  `nft_tproxy`. This is the reason the tier exists.
- **Real radios.** Two `mac80211_hwsim` phys by default; hostapd runs on them,
  `iwinfo` reports signal, noise and tx power, LuCI's wireless pages show the
  router rather than its config.
- **A real overlay.** squashfs `/rom` plus a writable overlay, so `firstboot`
  and `jffs2reset` behave as they do on hardware.
- **State that survives.** The disk is the router: `owlab down` is a shutdown,
  `--rebuild` is the factory reset.

What it costs: about 100 seconds the first time (download, extroot, packages,
two reboots) and about 18 seconds on every start after that.

See [05 — The VM tier](05-vm-tier.md) for how it is put together.

## The tier that was removed

An earlier design had a third, `full`: a container given real
`mac80211_hwsim` phys moved in from the *host* kernel. It was dropped, and the
reasoning is worth keeping.

It required a Linux kernel the developer controls. Native Linux and WSL2 could
do it; Colima and Lima could after installing `linux-modules-extra`. Docker
Desktop for macOS never could — its LinuxKit kernel is built with
`CONFIG_CFG80211` unset, and every candidate (`mac80211_hwsim`, `virt_wifi`,
`vwifi`, `wmediumd`) sits on top of cfg80211. `CONFIG_IKHEADERS` is unset too,
so building the module in place is not a way out either. The only project that
ever did this has been dead since 2019. OrbStack's kernel has no wireless stack
and no supported way to add modules.

So the tier would have been unavailable precisely on the machines that most
need something better than a container — and once the VM tier had real radios
on *every* host, what remained was a second, worse way to get the same thing on
a subset of hosts. Two tiers that differ in kind beats three where one is a
qualified version of another.
