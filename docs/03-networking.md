# Networking

## Reaching a router

Published ports, always. `http://localhost:<http>` and
`ssh -p <ssh> root@localhost`.

A container's bridge subnet is routable from the host on native Linux and
inside WSL2, but not on Docker Desktop for Mac or Windows. `--network host` and
`macvlan` do not work there either. So nothing in owlab addresses a router by
its bridge IP, and the same command works on every host.

For a VM the ports are QEMU `hostfwd` entries into the guest's LAN side. The
order of the two NICs is the whole design there; see
[05 — The VM tier](05-vm-tier.md).

## `br-lan`

The container's lan is a bridge named `br-lan`, with `eth0` as its only port.

Not decoration. Every OpenWrt target with more than one ethernet port builds
`br-lan`, so packages and scripts refer to it by name: podkop's
`source_network_interfaces` defaults to it, and so do a great many firewall
snippets, hotplug scripts and forum recipes. On a router whose lan was a bare
`eth0` all of those applied to nothing — no error, just a feature that did not
work.

How it is done, in the entrypoint before procd starts:

- The bridge takes `eth0`'s MAC, being its only and therefore lowest-MAC port,
  so the engine's veth peer keeps delivering to the address it always did and
  nothing on the host side notices.
- `eth0`'s own address is flushed. A bridge port cannot carry L3, and leaving
  the address there gives the kernel a connected route out of an interface that
  no longer transmits.
- `OWLAB_LAN_BRIDGE=0` falls back to lan on `eth0`. This is the one interface
  that must not break, and an engine that dislikes bridging its veth should be
  one variable away from working rather than one image rebuild.

## Why the entrypoint writes the network config at all

The timing is the point, and it is why this is not a `uci-defaults` script.

By the time `/etc/uci-defaults/*` run, `eth0` is down and flushed — a
uci-default cannot read the address the engine assigned, because there is
nothing left to read. And `/bin/config_generate` will then write its own config
from `/etc/board.json`, which on x86 and armsr means `br-lan` with a hardcoded
192.168.1.1. netifd applies it, the engine's address is gone, and the container
is unreachable with no console to fix it from.

`config_generate`'s guard is
`[ -s /etc/config/network -a -s /etc/config/system ] && exit 0`, so writing
*both* files in the entrypoint is what disarms it. That is why
`/etc/config/system` ships in the image even though nothing in it is
interesting: an empty or missing one re-arms the generator.

The address is polled for up to ten seconds rather than sampled once. Engines
start the container's process and configure its interface concurrently, so
`eth0` can still be bare when PID 1 begins — reliably so on a restart or a
loaded machine. Sampling once and finding nothing is not harmless: the config
would not be written, `config_generate` would fire, and the box would come up
on 192.168.1.1 with no route to it.

## DNS

`/etc/resolv.conf` keeps the engine's resolver first and appends public ones
behind it. Neither half is optional, because the engines disagree about which
one works:

- Docker Desktop and WSL2 hand out the embedded resolver `127.0.0.11`, which is
  implemented with NAT rules that OpenWrt's own boot flushes. It stops
  answering partway through the boot, so something else has to be in the list.
- OrbStack hands out a resolver that keeps working after boot — and there,
  direct UDP/53 to the internet is not forwarded at all, so the public
  resolvers alone resolve nothing. Overwriting the file the way the Docker
  Desktop case wants leaves the container with no DNS.

Reading the file before overwriting it is what makes one image work on both.
`options timeout:1` is there so a resolver that died mid-boot costs one second
rather than five.

## Outbound UDP port 53

Some engines do not forward it at all. Measured on OrbStack:

```console
$ owlab exec pk -- 'dig +short github.com @127.0.0.11; dig +short github.com @8.8.8.8'
140.82.121.4
;; communications error to 8.8.8.8#53: timed out
```

A plain `alpine` container on the same engine behaves identically, so this is
the engine and not OpenWrt, and owlab cannot fix it. TCP is unaffected —
`https://github.com` answers 200.

It matters because a package that ships its own resolver then resolves nothing
and blames the destination. podkop's sing-box exits with

```
initial rule-set: ... lookup github.com: context deadline exceeded
```

on a router where GitHub over https is fine. The fix is always the same: point
the package's bootstrap resolver at the engine's own, which does answer, and
run DoH or DoT over TCP on top of it. `owlab doctor` probes for this through a
running router and names the resolver to use.

## Flow offloading

ImmortalWrt enables it by default (`firewall.@defaults[0].flow_offloading` and
`_hw`); OpenWrt does not ship the key. The rule needs a flowtable, and a
flowtable needs `nf_flow_table` in the *running* kernel. Where it is missing,
nft rejects the entire ruleset:

```
flowtable ft { ... Error: Could not process rule: No such file or directory
meta l4proto { tcp, udp } flow offload @ft;
```

What survives is the base chains with `policy drop` and no jumps into any zone.
The symptom is a router that completes a TCP handshake and then resets, while
uhttpd is up and listening on `0.0.0.0:80` — reported as "connection reset by
peer" against a box that looks entirely healthy.

`95_owlab-base` probes rather than assumes: it creates a flowtable in a scratch
table and deletes it, and turns the setting off only if that fails. A host that
*can* offload keeps the distribution's own configuration, and a VM — whose
kernel is OpenWrt's own — keeps it too.

The `modprobe` before the probe is not decoration. OpenWrt ships no
`modules.alias`, so the kernel cannot autoload the module by the alias nft asks
for, and without it the probe failed on a router that was perfectly capable.

## Services that get switched off

`95_owlab-base` stops and disables two, and each was measured taking a box
down:

- **mwan3** watches the WAN, decides a dummy interface is dead, and installs
  ip rules that blackhole everything (`2061: from all fwmark 0x3d00/0x3f00
  blackhole`). The symptom is a router that answers on LuCI while every
  outbound connection hangs — `apk add` sits there forever.
- **watchcat** reboots or reconnects on a schedule when it thinks the network
  is down, which in here it always does.

They are stopped rather than uninstalled, so their LuCI pages stay. The
firewall is deliberately not in this list: fw4 works, and turning it off would
hide a whole class of real behaviour.
