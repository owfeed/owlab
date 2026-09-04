# Troubleshooting

[Русская версия](troubleshooting_ru.md)

Everything here was observed on a running router. Each entry is a symptom, its
cause, and what owlab does about it. They are grouped by what they look like
when you hit them, because almost none of them presents as its cause.

---

## "The image build fails"

### The index in the image is older than the layer that uses it

**Symptom.** `owlab up` fails while installing an `extra_packages:` file on a
24.10 router, and names a package nobody asked for:

```
opkg_install_pkg: Checksum or size mismatch for package bash. Either the opkg
or the package index are corrupt. Try 'opkg update'.
owlab: build failed: exit status 1
```

`bash` here is a **dependency** of the staged package, not the staged package.
Only the opkg line does this.

**Cause.** `opkg update` ran in an earlier layer than the install. Buildkit
keeps that layer for as long as the release and the feed package list hold,
while the install layer re-runs on every change to the staged files — so the
index can be months older than the install reading it.

A pinned point release does not save you, because it is not frozen: OpenWrt
rebuilds the packages inside `releases/24.10.8/` in place, without bumping a
version. Measured on 2026-09-04 against an image built 2026-07-29:

```
index in the image     bash 5.2.37-r1  Size 473650  SHA256 f1872e60...
downloads.openwrt.org  bash 5.2.37-r1  Size 473647  SHA256 20eaa220...
```

Same version, different bytes — which is exactly what opkg reports as a
checksum mismatch.

**Fix.** `opkg update` runs in the same layer as the install, and `owlab test`
refreshes the index before installing a local file too. apk needs neither:
apk-tools 3.0.5 revalidates a cached index older than `--cache-max-age`
(4 hours by default) on its own, so `apk add` in a month-old image
re-downloads every APKINDEX before it resolves anything. opkg has no such
policy — it reads whatever the last `opkg update` left and never asks.

### One router's `extra_packages` cancelled every other router

**Symptom.** `owlab up` fails at the extras step of one router, and routers
that were building fine stop with it:

```
#33 ERROR: process "/bin/sh -c set -eu; ... opkg install --force-overwrite ..." exit code: 255
#18 [imm2410 stage-3  2/12] ...   #18 CANCELED
#24 [imm2512 stage-3  2/12] ...   #24 CANCELED
owlab: build failed: exit status 1
```

**Cause.** `docker compose build` puts every router in one buildkit solve, and
buildkit cancels the whole solve on the first target that fails. The extras
step exited non-zero on one staged file, so one package on one router cost the
lab.

Both release lines do this — it is `set -eu`, not the package manager.
Measured 2026-09-04: opkg exits 255 on an unsatisfiable dependency, apk exits
27 on `unable to select packages`, and each cancelled the other routers in the
same run. There is no build-side setting to fall back on: neither `docker
compose build` nor `docker buildx bake` has a `--keep-going`.

**Fix.** The extras step installs the set, and when that fails installs each
file on its own so the good ones still land. What is still missing is named in
the build log, recorded in `/etc/owlab/extras-failed`, reported by `owlab up`
after its table, and failed on by `owlab test`:

```
! owrt2410 is running WITHOUT luci-app-example_1.0_all.ipk
!   Every other router built and started; only these packages are missing.
!   The package manager said why during the build — one router at a time shows it again:
!     owlab up --rebuild owrt2410
```

`owlab up` exits non-zero when it prints that. The lab is running and every
other router is usable, but it is not what the config describes, and a script
that runs `owlab up` before its own checks must not read one as the other.

Ask a running router directly:

```console
$ owlab exec owrt2410 -- cat /etc/owlab/extras-failed
luci-app-example_1.0_all.ipk
```

---

## "The router never answers on its published port"

### The container's interface is in no firewall zone

**Symptom.** `owlab up` reports `did not answer on HTTP in time`. `curl`
against the published port gets nothing back — exit 52, empty reply — while
inside the container `uhttpd` is listening on `0.0.0.0:80` and a local request
is answered. Reported on ImmortalWrt, where two routers were unreachable and
the OpenWrt routers on the same stand were not.

```console
# uclient-fetch -O- http://127.0.0.1/cgi-bin/luci/
HTTP error 403                                              <- LuCI wants a login: correct
$ curl -o /dev/null -w '%{http_code}\n' http://localhost:18026/cgi-bin/luci/
000
```

`owlab logs` stopping at `- generating board file -` is not a clue. That is the
last line a healthy container prints.

**Cause.** fw4 renders a zone as `iifname "<dev>" jump input_<zone>`, and works
out `<dev>` from the zone's `network` list at the moment it builds the ruleset.
A zone that resolves to no device emits no jump line at all, so the packet
falls off the end of the input chain into `jump handle_reject` — a TCP reset.
The host sees an empty reply; a sibling container on the same docker network
sees `Connection refused`.

```console
# nft list ruleset | sed -n '/^\tchain input {/,/^\t}/p'
chain input {
        type filter hook input priority filter; policy drop;
        iifname "dummy0" jump input_wan     # ... and no br-lan line
        jump handle_reject                  # so lan traffic lands here
}
```

**Fix.** `95_owlab-base` names the lan device on that zone explicitly —
`list device 'br-lan'` beside the `network 'lan'` the zone already carries,
`input ACCEPT` on it, and a zone created outright if the image ships none for
the lan network. fw4 takes `device` literally instead of asking netifd, so the
jump is there whatever netifd had done when fw4 read the config, and it
deduplicates the device against the network: one jump line, measured.

The firewall stays on. fw4 works in a container, and turning it off would hide
a whole class of real behaviour.

**Check it.** On the router:

```console
# nft list ruleset | grep 'jump input_lan'
        iifname "br-lan" jump input_lan comment "!fw4: Handle lan IPv4/IPv6 input traffic"
```

### ssh is closed the moment it is opened

**Symptom.** `ssh -p 12226 root@localhost` against an ImmortalWrt router is
closed immediately, while HTTP on the same container answers.

**Cause.** ImmortalWrt ships `option Interface 'lan'` in `/etc/config/dropbear`
and OpenWrt ships no such key, so dropbear binds one address rather than the
wildcard. Measured on one stand, `netstat -ltnp` inside each container:

```console
imm2512    tcp 0 0 192.168.163.5:22  LISTEN  2262/dropbear
owrt2512   tcp 0 0 0.0.0.0:22        LISTEN   889/dropbear
```

That address is correct only for as long as netifd finished with `lan` before
dropbear started and nothing ever arrives by another address — neither of which
a container recreated this often can promise.

**Fix.** `95_owlab-base` deletes the key, so dropbear binds `0.0.0.0:22` on both
distributions.

**Check it.** `netstat -ltn` inside the container must show `0.0.0.0:22`.

---

## "The router answers LuCI but nothing works"

### procd jails services, and a container cannot build a jail

**Symptom.** A router with no DNS server. Any package that configures dnsmasq
to forward somewhere — pointing it at a helper daemon — is configuring a daemon
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

### The router resets every connection

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

### LuCI answers but nothing outbound works

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

**Symptom.** A package installs, starts, reports success, and moves no traffic.
Its nft rules are present but match nothing.

**Cause.** `source_network_interfaces` defaults to `br-lan`, and the container's
lan was a bare `eth0`. The same is true of a great many firewall snippets and
hotplug scripts.

**Fix.** The container's lan is a bridge named `br-lan` with `eth0` as its port.
See [internals](internals.md#br-lan).

### Out-of-feed packages installed in the wrong order

**Symptom.** `ERROR: unable to select packages` on a set of packages that are
all present.

**Cause.** They were staged under their own names and installed by a glob,
which is alphabetical. `luci-app-example` sorts before `example-daemon` and requires it.

**Fix.** Staged numbered, and handed to the package manager as one set so it
resolves among them.

### A file conflict silently skipped a package

**Symptom.** `owlab up` prints its green list; the package is not installed.
Only on OpenWrt 24.10.

**Cause.** `luci-app-openclash` pulls `dnsmasq-full`, which ships
`/etc/init.d/dnsmasq` and collides with `dnsmasq`. ImmortalWrt already ships
`dnsmasq-full`, so three of the four routers were unaffected — the same config
produced a different router on one of them.

**Fix.** `--force-overwrite`, and a package in `extra_packages` that does not
install is named rather than passed over — see "One router's `extra_packages`
cancelled every other router" above for where that is reported.

---

## "DNS is broken but the network is fine"

### The engine does not forward outbound UDP port 53

**Symptom.** A daemon with its own DNS resolver exits with
`initial rule-set: ... lookup github.com: context deadline exceeded` on a
router where `https://github.com` answers 200. Switching to DoH does not help —
`lookup dns.google` fails the same way.

**Cause.** Some engines (OrbStack, measured) do not forward outbound UDP/53 at
all. A plain `alpine` container there behaves identically, so it is the engine
and not OpenWrt. DoH fails too, because such a daemon resolves its DoH host
through a plain UDP server on port 53 before it can use DoH at all.

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

### An installed QEMU that owlab said was not installed

**Symptom.** On Windows, `winget install SoftwareFreedomConservancy.QEMU`
succeeds, `C:\Program Files\qemu\qemu-system-x86_64.exe` exists, and owlab
reports it as not found.

**Cause.** Neither the official installer nor winget puts QEMU on `PATH`, and
owlab looked only there. Being off `PATH` is the normal state of a correct
Windows install, so this was owlab calling a working machine broken.

**Fix.** `PATH` first, then the directories the installers use —
`%ProgramFiles%\qemu`, `%LOCALAPPDATA%\Programs\qemu`, the scoop app directory
and chocolatey's. `owlab doctor` prints the binary and the `qemu-img` it
settled on, and `OWLAB_QEMU` still overrides both.

`qemu-img` is taken from beside the emulator rather than from `PATH`, for the
same reason and one more: the two have to come from the same install, since a
`qemu-img` from a different QEMU can write a qcow2 the emulator then refuses.

### A VM that "started" on Windows and never answered

**Symptom.** `owlab up` reported the router as starting, then timed out waiting
for ssh. Nothing in the logs, because there was no log.

**Cause.** Two of them, both invisible.

QEMU was told `-object rng-random,filename=/dev/urandom`. There is no
`/dev/urandom` on Windows, so QEMU exited before the guest started. It is
`rng-builtin` there now — same virtio-rng for the guest, no character device
for the host to not have.

And the failure could not be seen: on Windows QEMU is started rather than run
to completion, so `Start` returned successfully for a process that was already
dead, and its stderr went nowhere. owlab now waits briefly, notices a QEMU that
has already exited, and reports what it said.

### A router that died with the terminal that started it

**Symptom.** On Windows, closing the PowerShell window took the running VM with
it.

**Cause.** A child process started the ordinary way joins its parent's console
group and receives `CTRL_CLOSE_EVENT` when that window closes. On unix
`-daemonize` had already detached QEMU; Windows has no such flag.

**Fix.** `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`. The second half also
means Ctrl-C during `owlab up` interrupts the wait rather than the router.

### WHPX is not a guarantee

Hardware acceleration on Windows goes through Hyper-V, and there are machines
where a VM will not boot or hangs partway with `whpx` while the same image
works elsewhere. `OWLAB_ACCEL=tcg` forces translation: roughly an order of
magnitude slower, and it always works. Establishing which of the two you are
looking at is worth doing before debugging anything else.

---

## Three upstream behaviours worth knowing

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

### A pinned point release is still a moving target

The pin fixes which release you install from, not which bytes that release
serves. `releases/24.10.8/packages/` is rebuilt in place — the `Packages`
index for a release cut months ago was last modified yesterday — and a package
can be replaced under the same version string. So an index is only good for as
long as it is fresh, and every place that reads one has to refresh it first.
