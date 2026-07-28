# Findings

Everything here was observed on a running router. Each entry is a symptom, its
cause, and what owlab does about it. They are grouped by what they look like
when you hit them, because almost none of them presents as its cause.

---

## "The router answers LuCI but nothing works"

### procd jails services, and a container cannot build a jail

**Symptom.** A router with no DNS server. Any package that configures dnsmasq
to forward somewhere — podkop pointing it at sing-box — is configuring a daemon
that is not running. `service dnsmasq status` says `not running` and starting it
appears to succeed.

**Cause.** procd jails a fair number of services, and a jail needs namespaces
the container is not allowed to create:

```console
$ ujail -n test -p -- /bin/true
jail: failed to clone/fork: Operation not permitted
```

procd reads that as a crash and respawns, so the service sits in a permanent
crash loop whose only trace is:

```
procd: Instance dnsmasq::cfg01411c s in a crash loop 6 crashes
```

Running the same binary by hand with the same generated config works perfectly,
which is what makes this hard to find.

**Fix.** owlab removes `/sbin/ujail` from the container image. procd falls back
to running the service directly when it is absent, so this is the fix rather
than a workaround. The alternative — granting the container `CAP_SYS_ADMIN` —
is close enough to root on the host that a throwaway dev box has no business
asking for it. What is lost is a security boundary between services inside a
container that is already one trust domain. A `fidelity: vm` router runs its own
kernel and keeps its jails.

This had been true of every owlab container from the start. It went unnoticed
because LuCI renders the DHCP and DNS pages from config, not from a running
daemon.

### Flow offloading takes the whole firewall down

**Symptom.** TCP connects and is then reset — "connection reset by peer" —
against a `uhttpd` that is up and listening on `0.0.0.0:80`. Only on
ImmortalWrt.

**Cause.** ImmortalWrt enables `flow_offloading` by default; the rule needs a
flowtable, and a flowtable needs `nf_flow_table` in the running kernel. Where
it is missing nft rejects the *entire* ruleset, leaving the base chains with
`policy drop` and no jumps into any zone.

**Fix.** `95_owlab-base` creates a flowtable in a scratch table and deletes it,
and turns the setting off only if that fails. A `modprobe` runs first: OpenWrt
ships no `modules.alias`, so the kernel cannot autoload the module by the alias
nft asks for, and without it the probe failed on a router that was perfectly
capable.

### mwan3 blackholes everything

**Symptom.** LuCI answers 200 while every outbound connection hangs with no
error. `apk` appears frozen.

**Cause.** mwan3 decides the dummy WAN is dead and installs
`2061: from all fwmark 0x3d00/0x3f00 blackhole`.

**Fix.** `95_owlab-base` stops and disables mwan3 and watchcat. The firewall is
deliberately *not* in that list — fw4 works, and turning it off would hide a
whole class of real behaviour.

---

## "The package installed but does nothing"

### `br-lan` did not exist

**Symptom.** podkop installs, starts, reports success, and proxies no traffic.
Its nft rules are present but match nothing.

**Cause.** `source_network_interfaces` defaults to `br-lan`, and the container's
lan was a bare `eth0`. The same is true of a great many firewall snippets and
hotplug scripts.

**Fix.** The container's lan is a bridge named `br-lan` with `eth0` as its port.
See [03 — Networking](03-networking.md).

### Out-of-feed packages installed in the wrong order

**Symptom.** `ERROR: unable to select packages` on a set of packages that are
all present.

**Cause.** They were staged under their own names and installed by a glob,
which is alphabetical. `luci-app-podkop` sorts before `podkop` and requires it.

**Fix.** Staged numbered, and handed to the package manager as one set so it
resolves among them.

### A file conflict silently skipped a package

**Symptom.** `owlab up` prints its green list; the package is not installed.
Only on OpenWrt 24.10.

**Cause.** `luci-app-openclash` pulls `dnsmasq-full`, which ships
`/etc/init.d/dnsmasq` and collides with `dnsmasq`. ImmortalWrt already ships
`dnsmasq-full`, so three of the four routers were unaffected — the same config
produced a different router on one of them.

**Fix.** `--force-overwrite`, and a failure in `extra_packages` is now fatal.

---

## "DNS is broken but the network is fine"

### The engine does not forward outbound UDP port 53

**Symptom.** sing-box exits with
`initial rule-set: ... lookup github.com: context deadline exceeded` on a
router where `https://github.com` answers 200. Switching to DoH does not help —
`lookup dns.google` fails the same way.

**Cause.** Some engines (OrbStack, measured) do not forward outbound UDP/53 at
all. A plain `alpine` container there behaves identically, so it is the engine
and not OpenWrt. DoH fails too because podkop uses `bootstrap_dns_server` as a
plain UDP server on port 53 to resolve the DoH host in the first place.

**Fix.** Not fixable in owlab. `owlab doctor` probes for it and names the
engine's own resolver, which does answer; a package with its own resolver
should bootstrap through that and run DoH or DoT over TCP on top.

### The engine's resolver dies partway through boot

**Cause.** Docker Desktop and WSL2 hand out `127.0.0.11`, implemented with NAT
rules that OpenWrt's own boot flushes.

**Fix.** `/etc/resolv.conf` keeps the engine's resolver first and appends public
ones behind it, with `options timeout:1`. Overwriting it outright breaks
OrbStack, where the public resolvers are unreachable; not appending breaks
Docker Desktop. Reading the file before rewriting it is what makes one image
work on both.

---

## "Wireless does not come up"

### Everything installed after boot needs a boot

**Symptom.** Radios exist as real phys and accept `iw` commands, but
`ubus call network.wireless status` shows `up: false, pending: true` forever.
`wifi up` reports `Command failed: Not found`. No error names a cause.

**Cause.** Three separate things, all the same shape — the packages were
installed onto a system that had already booted:

- **procd** never started `wpad`, because the service did not exist at boot.
- **netifd** had no wireless support loaded, because `/lib/netifd/wireless/*`
  is read at startup.
- **ubusd** had no wpad ACL, because `/usr/share/acl.d/` is read at startup.
  Without it hostapd — running as the unprivileged `network` user — cannot
  publish its ubus object, and the setup script waits for an object that will
  never appear.

The last one is the subtlest: hostapd started *as root* registers fine, which
makes it look like a permissions problem with the socket. The socket is
world-writable; ubusd's ACLs are the gate.

**Fix.** One reboot at the end of VM provisioning, before the overlay is
applied.

### Detection puts every simulated radio on 6 GHz

**Symptom.** `iwinfo` reports `Channel: 0 (unknown GHz)` and `HT Mode: null`.

**Cause.** `wifi config` picks the highest band the phy advertises, and a
simulated phy advertises all of them. Under the default regulatory domain `00`,
6 GHz permits almost nothing.

**Fix.** The `wifi` fixture assigns 2.4 GHz to the first radio and 5 GHz to the
second, and sets a country.

---

## "It works on my machine"

### `--platform` must always be explicit

Only `x86_64` normalises to a real OCI platform (`amd64`). Every other target
publishes a non-standard architecture string (`aarch64_generic`, `mips_24kc`),
so a plain pull on an arm64 host fails with `no matching manifest`.

Worse for ImmortalWrt: **every** tag in `immortalwrt/rootfs` is labelled
`linux/amd64`, including the ones whose contents are aarch64. Their metadata
cannot be used to select an image at all, which is why owlab unpacks their
tarball itself and stamps honest platform metadata.

### busybox `ip` ignores `peer name`

**Symptom.** A container in a restart loop, logging only
`SIOCGIFFLAGS: No such device`.

**Cause.** `ip link add eth1 type veth peer name eth1p` creates the pair and
names the peer `vethN`. Bringing up `eth1p` then fails, and `set -e` takes the
boot with it.

**Fix.** The peer name is read back from `ip -o link show`, and every cosmetic
operation in the entrypoint runs with errexit off.

### Editing `images/` changes nothing until the binary is rebuilt

The build context is embedded with `go:embed`. The running binary carries its
own copy. This has produced a false debugging trail more than once — a fix that
"did not work" because it was never in the image.

### BSD `sed` has no `\b`

A rename's first pass silently left half the occurrences. Worth remembering
whenever a script has to run on both macOS and Linux.

### macOS lets a specific-address bind coexist with a wildcard one

`owlab doctor`'s port check probed `127.0.0.1` and reported every port free
while `up` still failed with "address already in use". It now probes `0.0.0.0`,
which is what Docker binds.

---

## Two upstream behaviours worth knowing

### `kmods` must stay in the feed list

Every widely-copied container recipe strips it, on the reasoning that nothing
in it can load. Measured, that is wrong: the kmods feed resolves the `kmod-*`
*dependencies* of ordinary packages, and without it `apk add luci-app-sqm`
fails with `kmod-ifb (no such package)`. The modules install as files and never
load, which costs a few hundred KB and a kmodloader complaint at boot.

### The feed must be pinned to the exact point release

apk records hard pins in `/etc/apk/world` (`base-files=1707~4ccb782af7`).
Pointing a 25.12.4 rootfs at the 25.12.5 feed fails every install with
`breaks: world[...]`. owlab does not rewrite the feeds at all, which is the
simplest way to keep this right.
