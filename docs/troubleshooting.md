# Troubleshooting

[Русская версия](troubleshooting_ru.md)

This page takes you from an owlab error or symptom to its cause and fix. It is for anyone whose lab
does not build, start or behave the way `owlab.yaml` describes.

- Everything here was observed on a running router.
- Each entry gives the symptom or the exact message, then the cause, then the fix.
- **Fix** says what owlab now does about it. Where you have to act, the fix says what to run.
- Entries are grouped by what the problem looks like, because almost none of them looks like its cause.
- Search this page for the exact text you see. Messages are quoted verbatim.

## Find it fast

| You see | Go to |
|---|---|
| `Checksum or size mismatch for package bash` on a 24.10 router | [Index older than the layer](#the-index-in-the-image-is-older-than-the-layer-that-uses-it) |
| Other routers end with `CANCELED` when one router's extras fail | [One router's `extra_packages`](#one-routers-extra_packages-cancelled-every-other-router) |
| `! owrt2410 is running WITHOUT luci-app-example_1.0_all.ipk` | [One router's `extra_packages`](#one-routers-extra_packages-cancelled-every-other-router) |
| `ERROR: wget: exited with error 8` and `1 unavailable, 0 stale` before any install | [One unreachable feed](#one-unreachable-feed-stopped-the-build-before-a-package-was-tried) |
| `! owrt2410 is running WITHOUT luci-app-example`, exit 1 | [A router without a package](#owlab-up-exits-1-a-router-is-running-without-a-package-from-packages) |
| `did not answer on HTTP in time`, `curl` gets an empty reply | [No firewall zone](#the-containers-interface-is-in-no-firewall-zone) |
| ssh to ImmortalWrt closes at once | [ssh is closed](#ssh-is-closed-the-moment-it-is-opened) |
| No DNS server, `procd: Instance dnsmasq::cfg01411c s in a crash loop` | [procd jails](#procd-jails-services-and-a-container-cannot-build-a-jail) |
| "connection reset by peer" on ImmortalWrt | [The router resets every connection](#the-router-resets-every-connection) |
| LuCI answers 200, every outbound connection hangs | [Nothing outbound works](#luci-answers-but-nothing-outbound-works) |
| `owlab: the feed under test (owlab-feed: ...) was not read` | [`--feed` install stops](#--feed-install-stops-with-the-feed-under-test--was-not-read) |
| A package starts, reports success, moves no traffic | [`br-lan` did not exist](#br-lan-did-not-exist) |
| `ERROR: unable to select packages`, all packages present | [Wrong install order](#out-of-feed-packages-installed-in-the-wrong-order) |
| Green list, package missing, OpenWrt 24.10 only | [A file conflict](#a-file-conflict-silently-skipped-a-package) |
| `lookup github.com: context deadline exceeded` while HTTPS works | [Outbound UDP port 53](#the-engine-does-not-forward-outbound-udp-port-53) |
| DNS dies partway through boot on Docker Desktop or WSL2 | [The engine's resolver dies](#the-engines-resolver-dies-partway-through-boot) |
| `up: false, pending: true` forever, `wifi up` says `Command failed: Not found` | [Installed after boot](#everything-installed-after-boot-needs-a-boot) |
| `iwinfo` shows `Channel: 0 (unknown GHz)` | [Every radio on 6 GHz](#detection-puts-every-simulated-radio-on-6-ghz) |
| `no matching manifest` on an arm64 host | [`--platform`](#--platform-must-always-be-explicit) |
| Restart loop, only `SIOCGIFFLAGS: No such device` | [busybox `ip`](#busybox-ip-ignores-peer-name) |
| A fix in `images/` "did not work" | [Editing `images/`](#editing-images-changes-nothing-until-the-binary-is-rebuilt) |
| A script edit left half the occurrences on macOS | [BSD `sed`](#bsd-sed-has-no-b) |
| `doctor` says ports are free, `up` says "address already in use" | [macOS bind](#macos-lets-a-specific-address-bind-coexist-with-a-wildcard-one) |
| Windows: QEMU installed, owlab says not found | [Installed QEMU](#an-installed-qemu-that-owlab-said-was-not-installed) |
| Windows: VM "starting", then ssh timeout, no log | [A VM that never answered](#a-vm-that-started-on-windows-and-never-answered) |
| Windows: closing PowerShell kills the VM | [Died with the terminal](#a-router-that-died-with-the-terminal-that-started-it) |
| Windows: VM hangs or does not boot with `whpx` | [WHPX](#whpx-is-not-a-guarantee) |
| `503 Service Unavailable` or `unreachable after 4 attempts` | [503](#503-service-unavailable-the-server-is-failing-not-refusing-) |
| `404 Not Found` on an image, rootfs or package | [404](#404-not-found-on-an-image-a-rootfs-or-a-package) |
| `building <id> from the rootfs tarball` | [A download failed](#a-download-failed) |
| `setup` step exits 8 with `Upstream outage (safe to rerun)` | [`setup` step](#setup-step-upstream-outage-safe-to-rerun-exit-code-8) |
| `kmod-ifb (no such package)` | [`kmods` must stay](#kmods-must-stay-in-the-feed-list) |
| `breaks: world[...]` | [Pin the exact point release](#the-feed-must-be-pinned-to-the-exact-point-release) |

---

## The image build fails

### The index in the image is older than the layer that uses it

**Symptom.** `owlab up` fails while installing an `extra_packages:` file on a 24.10 router. It names a
package nobody asked for:

```
opkg_install_pkg: Checksum or size mismatch for package bash. Either the opkg
or the package index are corrupt. Try 'opkg update'.
owlab: build failed: exit status 1
```

- `bash` here is a **dependency** of the staged package, not the staged package.
- Only the opkg line does this.

**Cause.** `opkg update` ran in an earlier layer than the install. The index in the image can be months
older than the install that reads it.

**Fix.** `opkg update` runs in the same layer as the install. `owlab test` also refreshes the index
before it installs a local file.

<details>
<summary>Why the index goes stale, and why apk is not affected</summary>

Buildkit keeps the `opkg update` layer for as long as the release and the feed package list hold. The
install layer re-runs on every change to the staged files.

A pinned point release does not save you, because it is not frozen. OpenWrt rebuilds the packages
inside `releases/24.10.8/` in place, without bumping a version. Measured on 2026-09-04 against an image
built 2026-07-29:

```
index in the image     bash 5.2.37-r1  Size 473650  SHA256 f1872e60...
downloads.openwrt.org  bash 5.2.37-r1  Size 473647  SHA256 20eaa220...
```

Same version, different bytes. opkg reports exactly that as a checksum mismatch.

apk needs neither fix. apk-tools 3.0.5 revalidates a cached index older than `--cache-max-age`
(4 hours by default) on its own. So `apk add` in a month-old image re-downloads every APKINDEX before it
resolves anything. opkg has no such policy: it reads whatever the last `opkg update` left and never
asks.

</details>

### One router's `extra_packages` cancelled every other router

**Symptom.** `owlab up` fails at the extras step of one router. Routers that were building fine stop
with it:

```
#33 ERROR: process "/bin/sh -c set -eu; ... opkg install --force-overwrite ..." exit code: 255
#18 [imm2410 stage-3  2/12] ...   #18 CANCELED
#24 [imm2512 stage-3  2/12] ...   #24 CANCELED
owlab: build failed: exit status 1
```

**Cause.** `docker compose build` puts every router in one buildkit solve. Buildkit cancels the whole
solve on the first target that fails. The extras step exited non-zero on one staged file, so one
package on one router cost the lab.

**Fix.** The extras step installs the set. If that fails, it installs each file on its own, so the good
ones still land. A file that is still missing is:

- named in the build log;
- recorded in `/etc/owlab/extras-failed`;
- reported by `owlab up` after its table;
- failed on by `owlab test`.

`owlab up` reports it like this:

```
! owrt2410 is running WITHOUT luci-app-example_1.0_all.ipk
!   Every other router built and started; only these packages are missing.
!   The package manager said why during the build — one router at a time shows it again:
!     owlab up --rebuild owrt2410
```

`owlab up` exits non-zero when it prints that. The lab is running and every other router is usable.
But the lab is not what the config describes. A script that runs `owlab up` before its own checks must
not read one as the other.

**Check.** Ask a running router directly:

```console
$ owlab exec owrt2410 -- cat /etc/owlab/extras-failed
luci-app-example_1.0_all.ipk
```

<details>
<summary>Why no build setting can fix this</summary>

Both release lines do this: the cause is `set -eu`, not the package manager. Measured 2026-09-04: opkg
exits 255 on an unsatisfiable dependency, and apk exits 27 on `unable to select packages`. Each
cancelled the other routers in the same run.

There is no build-side setting to fall back on. Neither `docker compose build` nor `docker buildx bake`
has a `--keep-going`.

</details>

### One unreachable feed stopped the build before a package was tried

**Symptom.** `owlab up` fails in stage-3 before a single package is installed. The last thing in the log
is the package manager saying the rest of the feed set is fine:

```
ERROR: wget: exited with error 8
WARNING: .../kmods/6.18.33-1-<hash>/packages.adb: unexpected end of file
1 unavailable, 0 stale; 10206 distinct packages available
owlab: build failed: exit status 1
```

It happens on every run against that image, not now and then.

**Cause.** The image asks for a kmods directory the feed no longer serves, and gets a 404 on that one
sub-index. Both package managers exit non-zero over one unreachable feed. The step ran under a bare
`set -eu`, so that exit ended the layer before the per-package install loop.

**Fix.** The index refresh continues when at least one feed was read. It still fails when none was.
owlab replays the manager's whole output, so the feed that did not answer is named. A line follows that
says what the install is going on with:

```
owlab: apk update: partial refresh, 8 feed(s) read, 11279 packages available; ...
```

**Check.** Ask the router what its refresh does, and count the feeds it opened:

```console
$ owlab exec owrt2512 -- 'apk update 2>&1 | grep -c "^ \["'    # feeds read; 0 means none
$ owlab exec owrt2410 -- 'opkg update 2>&1 | grep -c "^Updated"'
```

| Count | Exit code | Meaning |
|---|---|---|
| 0 | any | the fatal case; owlab will have said so |
| above 0 | non-zero | the partial case |

After a partial refresh, a name the install does not find is reported as a failure. The feed that did
not answer may carry it; see the [next entry](#owlab-up-exits-1-a-router-is-running-without-a-package-from-packages).
When only a kmods index did not answer, only a missing `kmod-*` is a failure.

<details>
<summary>Why the kmods index disappears, and why the package count is not the test</summary>

The kmods index is named after the kernel the image runs (`kmods/6.18.33-1-<hash>/packages.adb`). The
feed keeps only the last handful of kernel builds. An image older than that window gets the 404 until
the image itself is rebuilt. Retrying is not the tool: this is a 404, not a stall.

Measured: `apk update` exits 1 with one bad feed of nine on 25.12.4. `opkg update` exits 1 with one of
eight on 24.10.8. The loop that the exit cut off was already written to survive a package this feed does
not carry.

The count of packages in apk's summary line is deliberately not the whole test. With every feed pointed
at an unresolvable host, apk still reports `8 unavailable, 0 stale; 136 distinct packages available` and exits 8. Those 136 are the installed
database, not a feed. Only the count of feeds read tells the two runs apart.

</details>

### `owlab up` exits 1: a router is running WITHOUT a package from `packages:`

**Symptom.** `owlab up` prints its table, then this, and exits 1:

```
! owrt2410 is running WITHOUT luci-app-example
!   Every other router built and started; only these packages are missing.
!   The package manager said why during the build — one router at a time shows it again,
!   and downloads what failed to download:
!     owlab up --rebuild owrt2410
!   A package the rebuild refuses again cannot go on that router: take it out of the
!   router's packages: (a "-name" entry for one owlab installs by default) or extra_packages:.
```

In 0.6.6 and earlier the same router started with exit 0. The only trace was an
`owlab: FAILED to install` line in the build log.

**Cause.** The package layer installs the set in one call. When that fails, it installs each name on its
own and records the ones that did not install in `/etc/owlab/packages-failed`. The build log gives the
reason above `owlab: FAILED to install <name>`:

| Reason in the build log | Meaning |
|---|---|
| `owlab: <name> did not download in 4 attempts` | A download server or network outage. The packages after it are asked once each, so one outage costs one round of waiting. |
| The package manager's own lines | A file clash, or a dependency the feeds do not carry. |
| `no feed that answered the refresh above carries it` | The name was not found after a `partial refresh`, and the feed that did not answer may carry it. |

**Fix.**

- After an outage, run `owlab up --rebuild owrt2410`. A plain `owlab up` reuses the cached layer and
  reports the same router again.
- For a clash or a missing dependency, remove the name from that router's `packages:`, or add the
  package it needs. For a package owlab installs by default, removing means a `-name` entry.

**Check.** Ask the running router:

```console
$ owlab exec owrt2410 -- cat /etc/owlab/packages-failed
luci-app-example
```

`owlab test` fails that router on its `packages` step with the same names.

---

## The router never answers on its published port

### The container's interface is in no firewall zone

**Symptom.** `owlab up` reports `did not answer on HTTP in time` and exits 1.

- `curl` against the published port gets nothing back: exit 52, empty reply.
- Inside the container, `uhttpd` is listening on `0.0.0.0:80` and a local request is answered.
- Reported on ImmortalWrt. Two routers were unreachable; the OpenWrt routers on the same stand (the set
  of routers running side by side) were not.

```console
# uclient-fetch -O- http://127.0.0.1/cgi-bin/luci/
HTTP error 403                                              <- LuCI wants a login: correct
$ curl -o /dev/null -w '%{http_code}\n' http://localhost:18026/cgi-bin/luci/
000
```

`owlab logs` stopping at `- generating board file -` is not a clue. That is the last line a healthy
container prints.

**Cause.** The lan zone resolved to no device, so fw4 emitted no jump into it. The packet falls off the
end of the input chain into `jump handle_reject`, which sends a TCP reset.

- The host sees an empty reply.
- A sibling container on the same docker network sees `Connection refused`.

```console
# nft list ruleset | sed -n '/^\tchain input {/,/^\t}/p'
chain input {
        type filter hook input priority filter; policy drop;
        iifname "dummy0" jump input_wan     # ... and no br-lan line
        jump handle_reject                  # so lan traffic lands here
}
```

**Fix.** `95_owlab-base` names the lan device on that zone explicitly:

- `list device 'br-lan'` beside the `network 'lan'` the zone already carries;
- `input ACCEPT` on it;
- a zone created outright if the image ships none for the lan network.

The firewall stays on. fw4 works in a container, and turning it off would hide a whole class of real
behaviour.

**Check.** On the router:

```console
# nft list ruleset | grep 'jump input_lan'
        iifname "br-lan" jump input_lan comment "!fw4: Handle lan IPv4/IPv6 input traffic"
```

<details>
<summary>How fw4 builds the zone jump</summary>

fw4 renders a zone as `iifname "<dev>" jump input_<zone>`. It works out `<dev>` from the zone's
`network` list when it builds the ruleset. A zone that resolves to no device emits no jump line
at all.

fw4 takes `device` as written instead of asking netifd. So the jump is there whatever netifd had done
when fw4 read the config. fw4 also deduplicates the device against the network: one jump line,
measured.

</details>

### ssh is closed the moment it is opened

**Symptom.** `ssh -p 12226 root@localhost` against an ImmortalWrt router is closed immediately. HTTP on
the same container answers.

**Cause.** ImmortalWrt ships `option Interface 'lan'` in `/etc/config/dropbear`, and OpenWrt ships no
such key. So dropbear binds one address rather than the wildcard. Measured on one stand, `netstat -ltnp`
inside each container:

```console
imm2512    tcp 0 0 192.168.163.5:22  LISTEN  2262/dropbear
owrt2512   tcp 0 0 0.0.0.0:22        LISTEN   889/dropbear
```

That address is correct only while two things hold: netifd finished with `lan` before dropbear started,
and nothing ever arrives by another address. A container recreated this often can promise neither.

**Fix.** `95_owlab-base` deletes the key, so dropbear binds `0.0.0.0:22` on both distributions.

**Check.** `netstat -ltn` inside the container must show `0.0.0.0:22`.

---

## The router answers LuCI but nothing works

### procd jails services, and a container cannot build a jail

**Symptom.** A router with no DNS server.

- `service dnsmasq status` says `not running`, and starting it appears to succeed.
- Any package that configures dnsmasq to forward to a helper daemon is configuring a daemon that is not
  running.

**Cause.** procd jails a fair number of services. A jail needs namespaces the container is not allowed
to create:

```console
$ ujail -n test -p -- /bin/true
jail: failed to clone/fork: Operation not permitted
```

procd reads that as a crash and respawns. The service sits in a permanent crash loop whose only trace
is:

```
procd: Instance dnsmasq::cfg01411c s in a crash loop 6 crashes
```

Running the same binary by hand with the same generated config works perfectly. That is what makes this
hard to find.

**Fix.** owlab removes `/sbin/ujail` from the container image. procd falls back to running the service
directly when it is absent, so this is the fix rather than a workaround. A `fidelity: vm` router runs
its own kernel and keeps its jails.

<details>
<summary>Why not grant the container the capability instead</summary>

The alternative is granting the container `CAP_SYS_ADMIN`. That is close enough to root on the host
that a throwaway dev box has no business asking for it.

What is lost is a security boundary between services inside a container that is already one trust
domain.

This had been true of every owlab container from the start. It went unnoticed because LuCI renders the
DHCP and DNS pages from config, not from a running daemon.

</details>

### The router resets every connection

**Symptom.** TCP connects and is then reset ("connection reset by peer"). The `uhttpd` is up and
listening on `0.0.0.0:80`. Only on ImmortalWrt.

**Cause.** ImmortalWrt enables `flow_offloading` by default. The rule needs a flowtable, and a flowtable
needs `nf_flow_table` in the running kernel. Where it is missing, nft rejects the *entire* ruleset. The
base chains are left with `policy drop` and no jumps into any zone.

**Fix.** `95_owlab-base` creates a flowtable in a scratch table and deletes it. It turns the setting off
only if that fails.

A `modprobe` runs first. OpenWrt ships no `modules.alias`, so the kernel cannot autoload the module by
the alias nft asks for. Without the `modprobe`, the probe failed on a router that was perfectly capable.

### LuCI answers but nothing outbound works

**Symptom.** LuCI answers 200 while every outbound connection hangs with no error. `apk` appears frozen.

**Cause.** mwan3 decides the dummy WAN is dead and installs
`2061: from all fwmark 0x3d00/0x3f00 blackhole`.

**Fix.** `95_owlab-base` stops and disables mwan3 and watchcat. The firewall is deliberately *not* in
that list: fw4 works, and turning it off would hide a whole class of real behaviour.

**Check.** On the router, `ip rule` lists the `2061` blackhole rule above while mwan3 runs. The boot log
says `owlab: stopped mwan3` when `95_owlab-base` stopped it.

**If mwan3 runs anyway,** stop and disable it by hand, with the two commands `95_owlab-base` runs:

```console
# /etc/init.d/mwan3 stop
# /etc/init.d/mwan3 disable
```

---

## The package installed but does nothing

### `--feed` install stops with "the feed under test ... was not read"

**Symptom.** `owlab test --feed` or `owlab install --feed` fails at the install step with:

```
owlab: the feed under test (owlab-feed: https://.../packages.adb) was not read: ...
Refusing to install by name, because apk would take a same-named package from another feed instead.
```

The package manager's own reason is printed above it:

| Reason above it | Problem |
|---|---|
| `unexpected end of file` or `Failed to download` | 404 |
| `UNTRUSTED signature` or `Signature check failed` | key mismatch |

**Cause.** The router could not read the feed you are testing. The URL is wrong or 404s, or the index is
signed by a key other than `--feed-key`.

**Fix.** Correct the URL or the key, then rerun:

- apk wants the URL of `packages.adb` itself; opkg wants the directory that holds `Packages.gz`.
- For opkg, the key file must be named by its key id (`usign -F -p <key>`).

A distribution feed that does not answer is still only a warning. The check applies to the feed under
test alone.

**Check.** Ask the router about that one feed. The refresh's own output is not enough: both of its
obvious signals were measured to lie.

```console
$ owlab exec owrt2512 -- 'apk update --repositories-file /dev/null -X "$(cat /etc/apk/repositories.d/owlab-feed.list)"; echo rc=$?'
$ owlab exec owrt2410 -- 'rm -f /var/opkg-lists/owlab-feed; opkg update >/dev/null 2>&1; ls -l /var/opkg-lists/owlab-feed'
```

| manager | what looks like success but is not (measured) | what owlab reads instead |
|---|---|---|
| apk 3.0.5 | ` [url]` is printed from the cache when the index 404s on a later run (`0 unavailable, 1 stale`) | exit code of an update of that feed alone: 0 good; 1 for 404, untrusted key, stale |
| opkg (24.10.8) | an index with a bad signature prints `Updated list of available packages in /var/opkg-lists/<name>`, then `Signature check failed`, deletes the list and exits 0; a 404 leaves an older list in place | `/var/opkg-lists/<name>`, removed before the update, exists after it |

To see which feed an installed package came from:

```console
$ owlab exec owrt2512 -- apk query --fields name,version,repositories tree   # lists the feed URL next to lib/apk/db/installed
$ owlab exec owrt2410 -- opkg list-installed tree                             # opkg records no feed; compare the version with the feed's Packages
```

<details>
<summary>What happened before this check, and the duplicate `src/gz` line</summary>

Before this check the run went on and passed. The refresh treats one dead feed as routine. Then
`apk add <name>` / `opkg install <name>` took the same-named package from the distribution feed.
Measured on 25.12.4 and 24.10.8 with a probe feed that 404s: both installed the distribution's
`tree-2.2.1` and exited 0.

Running `owlab install --feed` again on the same router now replaces the earlier `src/gz owlab-feed`
line. Before, opkg kept the first line of a name and skipped the rest
(`Duplicate src declaration ... Skipping.`), so a corrected URL was never read.

</details>

### `br-lan` did not exist

**Symptom.** A package installs, starts, reports success, and moves no traffic. Its nft rules are
present but match nothing.

**Cause.** `source_network_interfaces` defaults to `br-lan`, and the container's lan was a bare `eth0`.
The same is true of a great many firewall snippets and hotplug scripts.

**Fix.** The container's lan is a bridge named `br-lan` with `eth0` as its port. See
[internals](internals.md#br-lan).

### Out-of-feed packages installed in the wrong order

**Symptom.** `ERROR: unable to select packages` on a set of packages that are all present.

**Cause.** They were staged under their own names and installed by a glob, which is alphabetical.
`luci-app-example` sorts before `example-daemon` and requires it.

**Fix.** They are staged numbered and handed to the package manager as one set, so it resolves among
them.

### A file conflict silently skipped a package

**Symptom.** `owlab up` prints its green list; the package is not installed. Only on OpenWrt 24.10.

**Cause.** A package that depends on `dnsmasq-full` pulls it in. `dnsmasq-full` ships
`/etc/init.d/dnsmasq` and collides with `dnsmasq`. ImmortalWrt already ships `dnsmasq-full`, so three of
the four routers were unaffected. The same config produced a different router on one of them.

**Fix.** `--force-overwrite`. A package in `extra_packages` that does not install is named rather than
passed over. For where that is reported, see
[One router's `extra_packages` cancelled every other router](#one-routers-extra_packages-cancelled-every-other-router).

---

## DNS is broken but the network is fine

### The engine does not forward outbound UDP port 53

**Symptom.** A daemon with its own DNS resolver exits with
`initial rule-set: ... lookup github.com: context deadline exceeded`. On the same router,
`https://github.com` answers 200. Switching to DoH does not help: `lookup dns.google` fails the same way.

**Cause.** Some engines (OrbStack, measured) do not forward outbound UDP/53 at all. A plain `alpine`
container there behaves identically, so it is the engine and not OpenWrt. DoH fails too: such a daemon
resolves its DoH host through a plain UDP server on port 53 before it can use DoH at all.

**Fix.** Not fixable in owlab. `owlab doctor` probes for it and names the engine's own resolver, which
does answer. A package with its own resolver should bootstrap through that resolver and run DoH or DoT
over TCP on top.

### The engine's resolver dies partway through boot

**Cause.** Docker Desktop and WSL2 hand out `127.0.0.11`. It is implemented with NAT rules that
OpenWrt's own boot flushes.

**Fix.** `/etc/resolv.conf` keeps the engine's resolver first and appends public ones behind it, with
`options timeout:1`.

- Overwriting the file outright breaks OrbStack, where the public resolvers are unreachable.
- Not appending breaks Docker Desktop.
- Reading the file before rewriting it is what makes one image work on both.

---

## Wireless does not come up

### Everything installed after boot needs a boot

**Symptom.** Radios exist as real phys and accept `iw` commands, but
`ubus call network.wireless status` shows `up: false, pending: true` forever. `wifi up` reports
`Command failed: Not found`. No error names a cause.

**Cause.** The packages were installed onto a system that had already booted. That breaks three
separate things:

- **procd** never started `wpad`, because the service did not exist at boot.
- **netifd** had no wireless support loaded, because `/lib/netifd/wireless/*` is read at startup.
- **ubusd** had no wpad ACL, because `/usr/share/acl.d/` is read at startup. Without it hostapd, running
  as the unprivileged `network` user, cannot publish its ubus object. The setup script waits for an
  object that will never appear.

The last one is the subtlest. hostapd started *as root* registers fine, which makes it look like a
permissions problem with the socket. The socket is world-writable; ubusd's ACLs are the gate.

**Fix.** One reboot at the end of VM provisioning, before the overlay is applied.

### Detection puts every simulated radio on 6 GHz

**Symptom.** `iwinfo` reports `Channel: 0 (unknown GHz)` and `HT Mode: null`.

**Cause.** `wifi config` picks the highest band the phy advertises, and a simulated phy advertises all
of them. Under the default regulatory domain `00`, 6 GHz permits almost nothing.

**Fix.** The `wifi` fixture assigns 2.4 GHz to the first radio and 5 GHz to the second, and sets a
country.

---

## It works on my machine

### `--platform` must always be explicit

**Symptom.** A plain pull on an arm64 host fails with `no matching manifest`.

**Cause.** Only `x86_64` normalises to a real OCI platform (`amd64`). Every other target publishes a
non-standard architecture string (`aarch64_generic`, `mips_24kc`).

ImmortalWrt is worse: **every** tag in `immortalwrt/rootfs` is labelled `linux/amd64`, including the
ones whose contents are aarch64. Their metadata cannot be used to select an image at all.

**Fix.** owlab unpacks the ImmortalWrt tarball itself and stamps honest platform metadata.

### busybox `ip` ignores `peer name`

**Symptom.** A container in a restart loop, logging only `SIOCGIFFLAGS: No such device`.

**Cause.** `ip link add eth1 type veth peer name eth1p` creates the pair and names the peer `vethN`.
Bringing up `eth1p` then fails, and `set -e` takes the boot with it.

**Fix.** The peer name is read back from `ip -o link show`. Every cosmetic operation in the entrypoint
runs with errexit off.

### Editing `images/` changes nothing until the binary is rebuilt

**Symptom.** A fix "did not work", because it was never in the image. This has produced a false
debugging trail more than once.

**Cause.** The build context is embedded with `go:embed`. The running binary carries its own copy.

**Fix.** Rebuild the binary after editing `images/`.

### BSD `sed` has no `\b`

**Symptom.** A rename's first pass silently left half the occurrences.

**Cause.** BSD `sed` has no `\b`. Remember it whenever a script has to run on both macOS and Linux.

### macOS lets a specific-address bind coexist with a wildcard one

**Symptom.** `owlab doctor`'s port check reported every port free, while `up` still failed with
"address already in use".

**Cause.** The check probed `127.0.0.1`.

**Fix.** It now probes `0.0.0.0`, which is what Docker binds.

### An installed QEMU that owlab said was not installed

**Symptom.** On Windows, `winget install SoftwareFreedomConservancy.QEMU` succeeds and
`C:\Program Files\qemu\qemu-system-x86_64.exe` exists. owlab reports it as not found.

**Cause.** Neither the official installer nor winget puts QEMU on `PATH`, and owlab looked only there.
Being off `PATH` is the normal state of a correct Windows install. So owlab was calling a working machine
broken.

**Fix.** owlab looks in `PATH` first, then in the directories the installers use:

- `%ProgramFiles%\qemu`
- `%LOCALAPPDATA%\Programs\qemu`
- the scoop app directory
- chocolatey's directory

`owlab doctor` prints the binary and the `qemu-img` it settled on. `OWLAB_QEMU` still overrides both.

`qemu-img` is taken from beside the emulator rather than from `PATH`, for the same reason and one more.
The two have to come from the same install: a `qemu-img` from a different QEMU can write a qcow2 the
emulator then refuses.

### A VM that "started" on Windows and never answered

**Symptom.** `owlab up` reported the router as starting, then timed out waiting for ssh. Nothing in the
logs, because there was no log.

**Cause.** Two causes, both invisible:

1. QEMU was told `-object rng-random,filename=/dev/urandom`. There is no `/dev/urandom` on Windows, so
   QEMU exited before the guest started.
2. The failure could not be seen. On Windows QEMU is started rather than run to completion. So `Start`
   returned successfully for a process that was already dead, and its stderr went nowhere.

**Fix.**

- On Windows it is `rng-builtin` now: the same virtio-rng for the guest, and no character device for the
  host to not have.
- owlab now waits briefly, notices a QEMU that has already exited, and reports what it said.

### A router that died with the terminal that started it

**Symptom.** On Windows, closing the PowerShell window took the running VM with it.

**Cause.** A child process started the ordinary way joins its parent's console group. It receives
`CTRL_CLOSE_EVENT` when that window closes. On unix `-daemonize` had already detached QEMU; Windows has
no such flag.

**Fix.** `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`. The second half also means Ctrl-C during
`owlab up` interrupts the wait rather than the router.

### WHPX is not a guarantee

**Symptom.** On Windows, a VM will not boot or hangs partway with `whpx`, while the same image works
elsewhere.

**Cause.** Hardware acceleration on Windows goes through Hyper-V, and some machines fail with it.

**Fix.** Set `OWLAB_ACCEL=tcg` to force translation. It is roughly an order of magnitude slower, and it
always works. Establish which of the two you are looking at before you debug anything else.

---

## A download failed

### `503 Service Unavailable (the server is failing, not refusing …)`

**Cause.** The download server or GitHub was failing, and kept failing through owlab's retries
(4 attempts over about 14 seconds). Nothing is wrong with `owlab.yaml`.

**Fix.** Run the command again later. For GitHub, check
[githubstatus.com](https://www.githubstatus.com).

The same applies to these messages:

| Message | Covers |
|---|---|
| `unreachable after 4 attempts (a network or server outage …)` | timeouts, DNS failures and refused connections |
| `the server sent nothing for 1m0s` | an out-of-feed package or a compliance file whose server went quiet |
| `reading the body: unexpected EOF` | a download that broke off |

Each attempt downloads the whole file again.

### `404 Not Found` on an image, a rootfs or a package

**Cause.** The server answered, and the file is not there. owlab asks for a 404 once.

**Fix.** Check the release number and target in `owlab.yaml`. The release may not publish that image
yet, or the out-of-feed package URL is wrong.

### `… did not answer (…); building <id> from the rootfs tarball … instead of <image>`

Not an error. The registry that serves the router's base image refused the connection, timed out, or
answered 5xx or 429 through 4 attempts. That registry is Docker Hub for the default `openwrt/rootfs`, or
the one named in `image:`.

owlab built the router from its release's rootfs tarball instead, which is the same rootfs.

- The build takes longer, because no layer from a build on the image can be reused.
- The image carries the target's honest platform (`linux/arm64` rather than `linux/aarch64_generic`).
- The next run asks the registry again.

**If you see it on every run.** A machine that reaches Docker Hub only through the engine's
`registry-mirrors` sees this every time: owlab asks `registry-1.docker.io` itself. Point `image:` at the
mirror to keep the image path. owlab uses a proxy in `HTTPS_PROXY` as well.

**If it says `… and neither did the rootfs tarball (…); leaving <id> on <image> for the engine …`.** The same check found the download server unreachable too. An offline machine sees this.
owlab leaves the image to the engine, as it did before it asked. The engine pulls it through its own
proxy or mirror, or builds from the copy and layers it holds. If it can do neither, the build fails on
the pull and names the registry. Check the network of the machine running owlab, or set `HTTPS_PROXY`
for it.

### `setup` step: `Upstream outage (safe to rerun)`, exit code 8

**Cause.** `gh release download`, `gh release view` or `gh attestation verify` could not reach GitHub or
Sigstore after 4 attempts. A call that does not finish within 120 seconds counts as one of them, and the
log says `no answer from GitHub within 120s`.

The binary was not checked, which is different from failing verification.

**Fix.** Rerun the job.

`does not verify as built by owfeed/owlab's release workflow`, exit code 1, is the real refusal.

---

## Three upstream behaviours worth knowing

### `kmods` must stay in the feed list

**Symptom.** Without the kmods feed, `apk add luci-app-sqm` fails with `kmod-ifb (no such package)`.

**Cause.** The kmods feed resolves the `kmod-*` *dependencies* of ordinary packages. Every widely-copied
container recipe strips it, on the reasoning that nothing in it can load. Measured, that is wrong.

**Fix.** Keep the kmods feed. The modules install as files and never load. That costs a few hundred KB
and a kmodloader complaint at boot.

### The feed must be pinned to the exact point release

**Symptom.** Pointing a 25.12.4 rootfs at the 25.12.5 feed fails every install with `breaks: world[...]`.

**Cause.** apk records hard pins in `/etc/apk/world` (`base-files=1707~4ccb782af7`).

**Fix.** owlab does not rewrite the feeds at all, which is the simplest way to keep this right.

### A pinned point release is still a moving target

The pin fixes which release you install from, not which bytes that release serves.

- `releases/24.10.8/packages/` is rebuilt in place. The `Packages` index for a release cut months ago was
  last modified yesterday.
- A package can be replaced under the same version string.

So an index is only good for as long as it is fresh. Every place that reads one has to refresh it first.
See [The index in the image is older than the layer that uses it](#the-index-in-the-image-is-older-than-the-layer-that-uses-it).
