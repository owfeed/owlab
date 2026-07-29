# Runbook

[Русская версия](runbook_ru.md)

Procedures. Each one is a goal, the commands, and how to tell it worked.

For "what does this key mean" see [reference](reference.md). For "why does it
behave like that" see [internals](internals.md). When something is broken, go
to [troubleshooting](troubleshooting.md) — it is sorted by symptom.

---

## Install owlab on this machine

One Go binary, one container engine. What differs per platform is where the
engine comes from and, for `fidelity: vm`, where QEMU comes from.

Either way, `owlab doctor` is the check: it names what is present, what is
missing and what each missing thing costs.

### Linux

```console
$ go install owfeed.org/owlab/cmd/owlab@latest
$ owlab doctor
```

Without a Go toolchain, take `owlab_<version>_linux_<arch>.tar.gz` from the
[latest release](https://github.com/owfeed/owlab/releases/latest) and put the
binary on `PATH`.

Docker, Podman or anything else with Compose v2. For `fidelity: vm`:

```console
$ sudo apt install qemu-system-x86 qemu-system-arm     # or the dnf equivalent
$ sudo usermod -aG kvm "$USER"                          # log out and back in
```

Being in the `kvm` group is the part that is easy to miss — QEMU built with
KVM still falls back to translation without it, and doctor says which of the
two you have.

### WSL2

Install and run owlab **inside** the WSL distribution, not from PowerShell
against `\\wsl$`. The commands are the Linux ones above. Either a Docker
installed in WSL or Docker Desktop with WSL integration enabled works.

Keep the project in the WSL filesystem — `~/src/luci-app-mine`, not
`/mnt/c/Users/...`. A Windows drive is reached over a translation layer that
does not propagate inotify, so `owlab sync --watch` never fires, and every file
read is slow enough to notice on a tree the size of LuCI. Doctor warns when the
working directory is under `/mnt`.

`fidelity: vm` works here: WSL2 exposes `/dev/kvm`, so a router boots on a real
kernel at native speed.

### macOS

```console
$ go install owfeed.org/owlab/cmd/owlab@latest
$ brew install qemu        # only for fidelity: vm
$ owlab doctor
```

Docker Desktop, OrbStack, Colima or Rancher Desktop. On Apple Silicon the
default router architecture is aarch64 and the VM tier runs on hvf, which is a
nine-second boot — asking for an `x86_64` router there is legal and translated,
and doctor says so before you wait for it.

### Windows

owlab is an ordinary console program and runs from PowerShell. There is no
Windows-specific setup beyond putting it on `PATH`.

```powershell
go install owfeed.org/owlab/cmd/owlab@latest
$env:PATH += ";$env:USERPROFILE\go\bin"        # for this session
owlab doctor
```

To keep it across sessions, add that directory in Settings > System > About >
Advanced system settings > Environment Variables, or:

```powershell
[Environment]::SetEnvironmentVariable('PATH', "$env:PATH;$env:USERPROFILE\go\bin", 'User')
```

Without a Go toolchain, unpack `owlab_<version>_windows_<arch>.zip` from the
[latest release](https://github.com/owfeed/owlab/releases/latest) somewhere
permanent and put that directory on `PATH` the same way. Both `amd64` and
`arm64` are published.

Docker Desktop with the WSL2 backend. The containers are Linux containers —
Docker Desktop must not be switched to Windows containers, and `docker version`
has to report a Linux server.

For `fidelity: vm`:

```powershell
winget install SoftwareFreedomConservancy.QEMU
```

Nothing needs to go on `PATH` afterwards. The installer does not add QEMU to
it, and owlab looks in `C:\Program Files\qemu` and the scoop and chocolatey
locations itself; `owlab doctor` prints the binary it settled on. If yours is
somewhere else entirely, `OWLAB_QEMU=C:\path\to\qemu-system-x86_64.exe`.

Then enable **Windows Hypervisor Platform**, which is off by default. In an
elevated PowerShell:

```powershell
dism.exe /Online /Enable-Feature /All /FeatureName:HypervisorPlatform
```

and reboot. Without it QEMU has no `whpx` and falls back to translation —
minutes per boot rather than seconds. Doctor names which of the two you have
rather than leaving you to time it.

If a VM will not boot at all, or hangs partway with acceleration on, try
`$env:OWLAB_ACCEL="tcg"`. WHPX sits on top of Hyper-V and there are machines it
does not work on; translation is slow and always works, and knowing which of
the two you are looking at is the first useful thing to establish.

Two more things doctor checks here, because both are optional Windows features
rather than defaults:

- **OpenSSH Client**, under Settings > System > Optional features. `fidelity:
  vm` routers are reached over ssh; container routers are not, so a
  container-only project never needs it.
- **A POSIX shell**, if the project has a `project.build` command. That command
  is a shell command line and is run by `sh`; Git for Windows ships one. Nothing
  else is substituted for it — handing a POSIX command line to `cmd.exe` would
  run something quietly different.

Line endings are worth one look. The scripts in a package are executed by
busybox, which reads a CRLF shebang as a command name ending in `\r` and reports
`bad interpreter: /bin/sh^M`. Add to the project's `.gitattributes`:

```
* text=auto eol=lf
```

then `git add --renormalize .`. Doctor checks for CRLF and fails on it, since
nothing downstream would name the cause.

Verify, on any of the four:

```console
$ owlab doctor
$ owlab up
$ curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/
200
```

---

## Start work on a package

```console
$ cd ~/src/luci-app-mine
$ owlab up
$ owlab open owrt2512
```

Verify: LuCI answers and your package's menu entry is there.

```console
$ curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/
200
```

If there is no `owlab.yaml` yet, the smallest one that works:

```yaml
version: 1
routers:
  - id: owrt2512
    release: "25.12.5"
```

---

## The edit loop

```console
$ owlab sync --watch
```

Leave it running. Every save copies the tree in and drops LuCI's caches; reload
the page.

Verify: touch a file you can see in the browser, wait for the line

```
  owrt2512   42 files, 846 KB
```

and reload.

If your package generates assets — a theme's `cascade.css`, compiled
translations — declare the build or the sync copies a tree that is missing
exactly the file LuCI asks for:

```yaml
project:
  build: ./build-css.sh htdocs/luci-static/mytheme/cascade.css
```

---

## Test against another release

Add a router and bring it up. Nothing else changes.

```yaml
routers:
  - id: owrt2410
    release: "24.10.8"
```

```console
$ owlab up owrt2410
$ owlab sync owrt2410
```

Verify: `owlab status` lists it as `running`, and note the `PKGMGR` column —
24.10 is opkg, 25.12 is apk. That difference is most of the reason to have the
second box.

---

## Check upstream for newer releases

```console
$ owlab releases
ROUTER      DISTRO    PINNED    NEWEST
owrt2512    openwrt   25.12.4   25.12.5   1 release behind
owrt2410    openwrt   24.10.8   24.10.8   up to date
```

To move a pin: edit `release:` in `owlab.yaml`, then

```console
$ owlab up --rebuild owrt2512
```

Verify: `owlab releases` says up to date, and

```console
$ owlab exec owrt2512 -- 'ubus call system board | grep version'
```

reports the release you pinned.

Do not pin `snapshot`. Snapshot images and snapshot feeds are rebuilt daily and
independently, so installs start failing within a day.

---

## Install a package to try it

```console
$ owlab install owrt2512 luci-app-ttyd
```

Verify: the menu entry appears after a page reload.

This does not survive `owlab up --rebuild`. To keep it, add it to `packages:`
with a `+`:

```yaml
defaults:
  packages: ["+luci-app-ttyd"]
```

---

## Install your own package from a URL

```yaml
defaults:
  extra_packages:
    - name: luci-app-mine
      apk: https://github.com/you/mine/releases/download/v1.0/luci-app-mine-1.0-r1.apk
      ipk: https://github.com/you/mine/releases/download/v1.0/luci-app-mine_1.0-r1_all.ipk
```

```console
$ owlab up --rebuild
```

Verify: the build prints `owlab: installing luci-app-mine-1.0-r1.apk`. A
failure here is fatal — the build stops rather than handing you a router
quietly missing it.

Both URLs, because apk and opkg do not share a naming scheme. Pin an exact
release, never a `latest` link: these are installed without checking
signatures.

---

## Build a real .apk or .ipk

```console
$ owlab build
$ owlab install owrt2512 dist/noarch/luci-app-mine-1.0-r1.apk
```

Verify: the file exists under `dist/`, and the router serves the same thing it
served from a sync.

Worth doing before every release. A real build minifies JS and CSS where a sync
does not, and that difference has broken packages that worked on the dev box.

On Apple Silicon this runs under emulation — every `openwrt/sdk` tag is
`linux/amd64` — so expect it to be slow. owlab says so before it starts.

---

## Check a package in CI

In a package repository, with no `owlab.yaml`:

```yaml
- uses: owfeed/owlab/setup@v0.5.2
- run: owlab build --release 25.12.5 --out dist
- uses: owfeed/owlab/action@v0.5.2
  with:
    releases: "25.12.5 24.10.8"
    install: dist/*/luci-app-mine-*
    assert: |
      package luci-app-mine
      http 200 /cgi-bin/luci/admin/services/mine
```

Verify: the job summary lists a row per router, and a broken package turns one
of them red with the check that failed named in it.

Run the same thing locally before pushing it — it is the same command, and it
needs nothing but Docker:

```console
$ owlab test --release 25.12.5 --install 'dist/*/luci-app-mine-*.apk' \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

Two releases rather than one because that is the axis that breaks: 25.12 ships
apk and 24.10 ships opkg, the artifacts are named differently, and the LuCI they
carry is not the same LuCI.

`--keep` leaves the routers up when a check fails and you want to look at the
page yourself. In CI leave it off: the teardown is what stops a failed run from
holding the ports the next one needs.

A whole workflow to copy is in
[examples/workflow/package-ci.yml](../examples/workflow/package-ci.yml); every
flag is in the [reference](reference.md#owlab-test).

---

## Work with kernel modules

Containers cannot load them. Use a VM router:

```yaml
routers:
  - id: real
    fidelity: vm
    release: "25.12.5"
    packages: ["+kmod-nft-tproxy"]
```

```console
$ owlab up real
$ owlab exec real -- 'uname -r; lsmod | grep nft_tproxy'
6.12.94
nft_tproxy    12288  0
```

Verify: the module is in `lsmod`. In a container it installs as a file and
never loads.

First start is about a minute and a half — download, extroot, packages, two
reboots. After that, twenty seconds. The disk keeps everything across
`owlab down`; only `--rebuild` resets it.

---

## Work with WiFi

A VM router gets two `mac80211_hwsim` radios and they are real.

```console
$ owlab exec real -- iwinfo
phy0-ap0  ESSID: "owlab"  Channel: 6 (2.437 GHz)  HT20  Tx-Power: 20 dBm
phy1-ap0  ESSID: "owlab"  Channel: 36 (5.180 GHz)         Tx-Power: 23 dBm
```

Verify: `ubus call network.wireless status` shows `"up": true` for both.

On a container, `fixtures: [wifi]` seeds a config instead. The menu, the radio
list, the SSIDs, the modes and the whole edit form render; signal, scan results
and associated stations stay empty, because those come from a real radio.

---

## Reach a service that is not HTTP or ssh

owlab publishes only those two. Forward anything else through ssh:

```console
$ IP=$(owlab exec pk2512 -- 'uci get network.lan.ipaddr')
$ ssh -f -N -p 2295 -L 1080:$IP:2080 root@localhost
$ curl -x socks5h://127.0.0.1:1080 https://ifconfig.me
```

Verify: the address differs from a direct `curl https://ifconfig.me`.

---

## A router stopped answering

```console
$ owlab status
$ owlab logs owrt2512 --tail 50
```

Then in order of likelihood:

```console
$ owlab exec owrt2512 -- 'service uhttpd status; netstat -lnt | grep :80'
$ owlab exec owrt2512 -- 'nft list chain inet fw4 input | head'
$ owlab exec owrt2512 -- 'ip route; ping -c1 -W3 8.8.8.8'
```

A router that completes a TCP handshake and then resets, with uhttpd up and
listening, is [flow offloading](troubleshooting.md#the-router-resets-every-connection).
A router that serves LuCI while every outbound connection hangs is
[mwan3](troubleshooting.md#luci-answers-but-nothing-outbound-works).

---

## Start over

```console
$ owlab up --rebuild          # throw the routers away and rebuild them
$ owlab down --purge          # and delete the images and VM disks too
```

`--rebuild` is the factory reset. Anything installed with `owlab install`, any
uci change made by hand, and a VM's whole disk are gone.

---

## Move to another machine

Everything owlab needs is `owlab.yaml` and your source tree. There is no state
outside the project directory and one image cache.

```console
$ owlab doctor
```

Verify: no `FAIL` lines. Warnings are limits of that machine, not errors — read
them, because they say what will not work there.

---

## Point owlab at a config somewhere else

```console
$ owlab --config ../other-project/owlab.yaml up
$ owlab --config ../other-project status        # a directory works too
$ export OWLAB_CONFIG=~/src/other-project
```

Works before or after the command name. Without it, owlab searches the working
directory and its parents.

---

## Publish images, cut a release

See [releasing](releasing.md).
