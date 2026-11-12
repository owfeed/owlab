# owlab runbook

[Русская версия](runbook_ru.md)

Procedures for someone who develops a LuCI package with owlab. Each one is a goal, the commands,
and how to tell it worked.

- **`owlab doctor` is the check** for any machine. It names what is present, what is missing and
  what each missing thing costs.
- **Every procedure ends with "Verify".** Do not move on until that check passes.
- **Other pages:** what a key means is in the [reference](reference.md). Why owlab behaves as it
  does is in [internals](internals.md). When something is broken, go to
  [troubleshooting](troubleshooting.md): it is sorted by symptom.

## Find it fast

| I want to… | Run | Section |
|---|---|---|
| install owlab | `go install`, then `owlab doctor` | [Install owlab](#install-owlab-on-this-machine) |
| start on a package | `owlab up`, `owlab open` | [Start work](#start-work-on-a-package) |
| see my edits on the router | `owlab sync --watch` | [Edit loop](#the-edit-loop) |
| test on 24.10 as well | add a router, `owlab up <id>` | [Another release](#test-against-another-release) |
| move to a newer OpenWrt release | `owlab releases`, `owlab up --rebuild` | [Newer releases](#check-upstream-for-newer-releases) |
| try someone else's package | `owlab install <router> <package>` | [Try a package](#install-a-package-to-try-it) |
| install my package from a URL | `extra_packages:` | [From a URL](#install-your-own-package-from-a-url) |
| build a real .apk or .ipk | `owlab build` | [Real build](#build-a-real-apk-or-ipk) |
| check a package in CI | `owfeed/owlab/action` | [CI](#check-a-package-in-ci) |
| load a kernel module | `fidelity: vm` | [Kernel modules](#work-with-kernel-modules) |
| test WiFi | VM router, or `fixtures: [wifi]` | [WiFi](#work-with-wifi) |
| reach a service other than HTTP or ssh | ssh forward | [Other services](#reach-a-service-that-is-not-http-or-ssh) |
| fix a router that stopped answering | `owlab status`, `owlab logs` | [Router not answering](#a-router-stopped-answering) |
| reset everything | `owlab up --rebuild` | [Start over](#start-over) |
| move to another machine | `owlab doctor` | [Another machine](#move-to-another-machine) |
| use a config from another directory | `--config`, `OWLAB_CONFIG` | [Config elsewhere](#point-owlab-at-a-config-somewhere-else) |
| publish images, cut a release | see [releasing](releasing.md) | [Releasing](#publish-images-cut-a-release) |

---

## Install owlab on this machine

You need one Go binary and one container engine. Per platform, only two things differ: where the
engine comes from, and where QEMU comes from for `fidelity: vm`.

| Platform | Container engine | VM acceleration (`fidelity: vm`) |
|---|---|---|
| [Linux](#linux) | Docker, Podman or anything else with Compose v2 | KVM, if you are in the `kvm` group |
| [WSL2](#wsl2) | Docker installed in WSL, or Docker Desktop with WSL integration | KVM: WSL2 exposes `/dev/kvm` |
| [macOS](#macos) | Docker Desktop, OrbStack, Colima or Rancher Desktop | hvf |
| [Windows](#windows) | Docker Desktop with the WSL2 backend | whpx, after you enable Windows Hypervisor Platform |

After installing, run the [check for all four](#verify-the-install).

### Linux

1. Install owlab and run the check:

   ```console
   $ go install owfeed.org/owlab/cmd/owlab@latest
   $ owlab doctor
   ```

   Without a Go toolchain, take `owlab_<version>_linux_<arch>.tar.gz` from the
   [latest release](https://github.com/owfeed/owlab/releases/latest) and put the binary on `PATH`.
2. Install a container engine: Docker, Podman or anything else with Compose v2.
3. For `fidelity: vm`, install QEMU and join the `kvm` group:

   ```console
   $ sudo apt install qemu-system-x86 qemu-system-arm     # or the dnf equivalent
   $ sudo usermod -aG kvm "$USER"                          # log out and back in
   ```

The `kvm` group is the step people miss. Without it, QEMU built with KVM still falls back to
translation. Doctor says which of the two you have.

### WSL2

1. Open the WSL distribution. Install and run owlab **inside** it, not from PowerShell against
   `\\wsl$`.
2. Run the [Linux](#linux) commands.
3. Use either a Docker installed in WSL or Docker Desktop with WSL integration enabled.
4. Keep the project in the WSL filesystem: `~/src/luci-app-mine`, not `/mnt/c/Users/...`.

Doctor warns when the working directory is under `/mnt`.

`fidelity: vm` works here. WSL2 exposes `/dev/kvm`, so a router boots on a real kernel at native
speed.

<details>
<summary>Why the project must not live on a Windows drive</summary>

A Windows drive is reached over a translation layer that does not propagate inotify. As a result,
`owlab sync --watch` never fires. Every file read is also slow enough to notice on a tree the size
of LuCI.

</details>

### macOS

```console
$ go install owfeed.org/owlab/cmd/owlab@latest
$ brew install qemu        # only for fidelity: vm
$ owlab doctor
```

Use Docker Desktop, OrbStack, Colima or Rancher Desktop as the engine.

On Apple Silicon the default router architecture is aarch64. The VM tier runs on hvf, which is a
nine-second boot. An `x86_64` router there is legal but translated, and doctor says so before you
wait for it.

### Windows

owlab is an ordinary console program and runs from PowerShell. The only Windows-specific setup is
putting it on `PATH`.

#### Install owlab

1. Install owlab, add it to `PATH` for this session, and run the check:

   ```powershell
   go install owfeed.org/owlab/cmd/owlab@latest
   $env:PATH += ";$env:USERPROFILE\go\bin"        # for this session
   owlab doctor
   ```

2. To keep `PATH` across sessions, add that directory in Settings > System > About > Advanced
   system settings > Environment Variables, or run:

   ```powershell
   [Environment]::SetEnvironmentVariable('PATH', "$env:PATH;$env:USERPROFILE\go\bin", 'User')
   ```

Without a Go toolchain, unpack `owlab_<version>_windows_<arch>.zip` from the
[latest release](https://github.com/owfeed/owlab/releases/latest) somewhere permanent. Put that
directory on `PATH` the same way. Both `amd64` and `arm64` are published.

#### Container engine

Use Docker Desktop with the WSL2 backend. The containers are Linux containers:

- Do not switch Docker Desktop to Windows containers.
- `docker version` must report a Linux server.

#### QEMU for `fidelity: vm`

1. Install QEMU:

   ```powershell
   winget install SoftwareFreedomConservancy.QEMU
   ```

   Nothing needs to go on `PATH` afterwards. The installer does not add QEMU to it. owlab looks in
   `C:\Program Files\qemu` and the scoop and chocolatey locations itself. `owlab doctor` prints the
   binary it settled on. If yours is somewhere else entirely, set
   `OWLAB_QEMU=C:\path\to\qemu-system-x86_64.exe`.
2. Enable **Windows Hypervisor Platform**, which is off by default. In an elevated PowerShell, run:

   ```powershell
   dism.exe /Online /Enable-Feature /All /FeatureName:HypervisorPlatform
   ```

3. Reboot.

Without Windows Hypervisor Platform, QEMU has no `whpx` and falls back to translation: minutes per
boot rather than seconds. Doctor names which of the two you have, so you do not have to time it.

If a VM will not boot at all, or hangs partway with acceleration on, try
`$env:OWLAB_ACCEL="tcg"`. Translation is slow and always works.

<details>
<summary>Why acceleration can fail on some machines</summary>

WHPX sits on top of Hyper-V, and there are machines it does not work on. Knowing which of the two
you are looking at is the first useful thing to establish.

</details>

#### Optional Windows features doctor checks

Both are optional Windows features rather than defaults, so doctor checks them here:

- **OpenSSH Client**, under Settings > System > Optional features. `fidelity: vm` routers are
  reached over ssh. Container routers are not, so a container-only project never needs it.
- **A POSIX shell**, if the project has a `project.build` command. That command is a shell command
  line and is run by `sh`. Git for Windows ships one. Nothing else is substituted for it: handing a
  POSIX command line to `cmd.exe` would run something quietly different.

#### Line endings

The scripts in a package are executed by busybox. busybox reads a CRLF shebang as a command name
ending in `\r` and reports `bad interpreter: /bin/sh^M`.

1. Add to the project's `.gitattributes`:

   ```
   * text=auto eol=lf
   ```

2. Run `git add --renormalize .`.

Doctor checks for CRLF and fails on it, since nothing downstream would name the cause.

### Verify the install

On any of the four platforms:

```console
$ owlab doctor
$ owlab up
$ curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/
200
```

---

## Start work on a package

If there is no `owlab.yaml` yet, this is the smallest one that works:

```yaml
version: 1
routers:
  - id: owrt2512
    release: "25.12.5"
```

Then bring the router up and open it:

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

---

## The edit loop

```console
$ owlab sync --watch
```

Leave it running. Every save copies the tree in and drops LuCI's caches. Reload the page.

Verify: touch a file you can see in the browser, wait for the line below, and reload.

```
  owrt2512   42 files, 846 KB
```

If your package generates assets, such as a theme's `cascade.css` or compiled translations, declare
the build. Otherwise the sync copies a tree that is missing exactly the file LuCI asks for:

```yaml
project:
  build: ./build-css.sh htdocs/luci-static/mytheme/cascade.css
```

---

## Test against another release

Add a router and bring it up. Nothing else changes.

1. Add the router to `owlab.yaml`:

   ```yaml
   routers:
     - id: owrt2410
       release: "24.10.8"
   ```

2. Bring it up and sync:

   ```console
   $ owlab up owrt2410
   $ owlab sync owrt2410
   ```

Verify: `owlab status` lists it as `running`. Note the `PKGMGR` column: 24.10 is opkg, 25.12 is
apk. That difference is most of the reason to have the second box.

---

## Check upstream for newer releases

```console
$ owlab releases
ROUTER      DISTRO    PINNED    NEWEST
owrt2512    openwrt   25.12.4   25.12.5   1 release behind
owrt2410    openwrt   24.10.8   24.10.8   up to date
```

To move a pin:

1. Edit `release:` in `owlab.yaml`.
2. Rebuild the router:

   ```console
   $ owlab up --rebuild owrt2512
   ```

Verify: `owlab releases` says up to date, and this command reports the release you pinned:

```console
$ owlab exec owrt2512 -- 'ubus call system board | grep version'
```

Do not pin `snapshot`. Snapshot images and snapshot feeds are rebuilt daily and independently, so
installs start failing within a day.

### Check the copies of the pin

`images/owlab.yaml` is the pin a person edits. The same release number is also copied into
`images/Dockerfile`, which starts a router without reading that config. The workflows carry no
copy: `ci.yml` reads the releases from `images/owlab.yaml` at run time.

```console
$ sh tools/pins.sh check
images/owlab.yaml pins: 24.10.6 24.10.8 25.12.1 25.12.5
no workflow names a release; every release literal in the pin files is one images/owlab.yaml pins
```

`ci.yml` runs this check on every push. It fails in two cases:

| Fails when | Why it matters |
|---|---|
| **A workflow names any release**, even the pinned one | `pins.yml` pushes with `GITHUB_TOKEN`. GitHub refuses a push from it that changes `.github/workflows/`, so a literal there is a pin nothing automatic can move. Read the release from the `routers` job in `ci.yml` instead. |
| **`images/Dockerfile` names a release `images/owlab.yaml` does not pin** | Nothing else catches that. An old release still builds, so a forgotten copy stays green while CI tests a release nobody runs. |

To move every copy of one release at once:

```console
$ sh tools/pins.sh bump 25.12.4 25.12.5
```

Comments, `examples/`, the READMEs and `docs/` keep the release they name. Both commands leave them
alone.

<details>
<summary>Why release numbers in prose are not moved</summary>

A release number in prose is an illustration. The sample output at the top of this section is a
pin deliberately one release behind.

</details>

### The job that proposes a bump

`pins.yml` runs daily and asks `owlab releases --json` the same question. When the answer is not
"up to date", it opens a pull request:

1. It rewrites the pins.
2. It runs `tools/pins.sh check` and `owlab context --list` over the result.
3. It stops there. It never pushes to `main`.

What proves a new release still builds a router is the CI run on that pull request, not a larger
number in a directory listing.

**It needs one repository setting.** Turn on **Settings → Actions → General → Allow GitHub Actions
to create and approve pull requests**. Without it the job pushes the branch and then stops at:

```
pull request create failed: GraphQL: GitHub Actions is not permitted to create or approve pull requests (createPullRequest)
```

Turning the setting on is the whole fix. The next run replaces the branch it left behind and opens
the pull request.

**Read the dispatched checks, not the held one.** A pull request opened with `GITHUB_TOKEN` gets
its `pull_request` run held in `action_required`. GitHub does that to every such pull request, so
`pins.yml` starts `ci.yml` on the branch itself. Merge when that run is green.

---

## Install a package to try it

```console
$ owlab install owrt2512 luci-app-ttyd
```

Verify: the menu entry appears after a page reload.

The package does not survive `owlab up --rebuild`. To keep it, add it to `packages:` with a `+`:

```yaml
defaults:
  packages: ["+luci-app-ttyd"]
```

---

## Install your own package from a URL

1. Add both URLs to `owlab.yaml`:

   ```yaml
   defaults:
     extra_packages:
       - name: luci-app-mine
         apk: https://github.com/you/mine/releases/download/v1.0/luci-app-mine-1.0-r1.apk
         ipk: https://github.com/you/mine/releases/download/v1.0/luci-app-mine_1.0-r1_all.ipk
   ```

2. Rebuild:

   ```console
   $ owlab up --rebuild
   ```

Verify: the build prints `owlab: installing luci-app-mine-1.0-r1.apk`.

- **Give both URLs**, because apk and opkg do not share a naming scheme.
- **Pin an exact release**, never a `latest` link: these are installed without checking signatures.

If the package cannot be installed, the build carries on and that router starts without it. One
bad file does not cancel the other routers. You see the failure in three places:

- `owlab up` names what is missing after its table and exits non-zero.
- `owlab test` fails that router on its `extra_packages` step.
- The list stays on the router in `/etc/owlab/extras-failed`.

---

## Build a real .apk or .ipk

Do this before every release. A real build minifies JS and CSS where a sync does not. That
difference has broken packages that worked on the dev box.

```console
$ owlab build
$ owlab install owrt2512 dist/noarch/luci-app-mine-1.0-r1.apk
```

Verify: the file exists under `dist/`, and the router serves the same thing it served from a sync.

If the package manager refused the file, `owlab install` exits 1 and names the router. A file whose
dependencies are already on the router is installed without asking a feed. Otherwise owlab
refreshes the feeds and installs again.

On Apple Silicon the build runs under emulation, because every `openwrt/sdk` tag is `linux/amd64`.
Expect it to be slow. owlab says so before it starts.

---

## Check a package in CI

In a package repository, with no `owlab.yaml`:

```yaml
- uses: owfeed/owlab/setup@v0.8.0
- run: owlab build --release 25.12.5 --out dist   # .apk for 25.12
- run: owlab build --release 24.10.8 --out dist   # .ipk for 24.10
- uses: owfeed/owlab/action@v0.8.0
  with:
    releases: "25.12.5 24.10.8"
    install: dist/*/luci-app-mine-*
    assert: |
      package luci-app-mine
      http 200 /cgi-bin/luci/admin/services/mine
```

Verify: the job summary lists a row per router. A broken package turns one of them red, with the
failed check named in it.

Before pushing, run the same check locally. It is the same command and needs nothing but Docker:

```console
$ owlab test --release 25.12.5 --install 'dist/*/luci-app-mine-*.apk' \
    --assert 'http 200 /cgi-bin/luci/admin/services/mine'
```

The `.apk` suffix matches only the apk build, which 25.12 and later install. A 24.10 router reads
`.ipk` and would get nothing to install. To test both release lines, drop the suffix, as the action
step above does.

If a check fails and you want to look at the page yourself, add `--keep`: it leaves the routers up.
In CI leave it off. The teardown stops a failed run from holding the ports the next one needs.

A whole workflow to copy is in
[examples/workflow/package-ci.yml](../examples/workflow/package-ci.yml). Every flag is in the
[reference](reference.md#owlab-test).

<details>
<summary>Why two releases rather than one</summary>

The release is the axis that breaks. 25.12 ships apk and 24.10 ships opkg, the artifacts are named
differently, and the LuCI they carry is not the same LuCI.

</details>

---

## Work with kernel modules

Containers cannot load kernel modules. Use a VM router:

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

Verify: the module is in `lsmod`. In a container it installs as a file and never loads.

- **First start:** about a minute and a half. That covers the download, extroot, packages and two
  reboots.
- **After that:** twenty seconds.
- **The disk** keeps everything across `owlab down`. Only `--rebuild` resets it.

---

## Work with WiFi

A VM router gets two `mac80211_hwsim` radios, and they are real.

```console
$ owlab exec real -- iwinfo
phy0-ap0  ESSID: "owlab"  Channel: 6 (2.437 GHz)  HT20  Tx-Power: 20 dBm
phy1-ap0  ESSID: "owlab"  Channel: 36 (5.180 GHz)         Tx-Power: 23 dBm
```

Verify: `ubus call network.wireless status` shows `"up": true` for both.

On a container, `fixtures: [wifi]` seeds a config instead:

- **Renders:** the menu, the radio list, the SSIDs, the modes and the whole edit form.
- **Stays empty:** signal, scan results and associated stations, because those come from a real
  radio.

---

## Reach a service that is not HTTP or ssh

owlab publishes only HTTP and ssh. Forward anything else through ssh:

```console
$ IP=$(owlab exec pk2512 -- 'uci get network.lan.ipaddr')
$ ssh -f -N -p 2295 -L 1080:$IP:2080 root@localhost
$ curl -x socks5h://127.0.0.1:1080 https://ifconfig.me
```

Verify: the address differs from a direct `curl https://ifconfig.me`.

---

## A router stopped answering

1. Look at the status and the log:

   ```console
   $ owlab status
   $ owlab logs owrt2512 --tail 50
   ```

2. Check, in order of likelihood:

   ```console
   $ owlab exec owrt2512 -- 'service uhttpd status; netstat -lnt | grep :80'
   $ owlab exec owrt2512 -- 'nft list chain inet fw4 input | head'
   $ owlab exec owrt2512 -- 'ip route; ping -c1 -W3 8.8.8.8'
   ```

| Symptom | Cause |
|---|---|
| The TCP handshake completes and then resets, with uhttpd up and listening | [flow offloading](troubleshooting.md#the-router-resets-every-connection) |
| LuCI is served, but every outbound connection hangs | [mwan3](troubleshooting.md#luci-answers-but-nothing-outbound-works) |

---

## Start over

```console
$ owlab up --rebuild          # throw the routers away and rebuild them
$ owlab down --purge          # and delete the images and VM disks too
```

`--rebuild` is the factory reset. Anything installed with `owlab install`, any uci change made by
hand, and a VM's whole disk are gone.

---

## Move to another machine

Everything owlab needs is `owlab.yaml` and your source tree. There is no state outside the project
directory and one image cache.

On the new machine, run:

```console
$ owlab doctor
```

Verify: no `FAIL` lines. Warnings are limits of that machine, not errors. Read them: they say what
will not work there.

---

## Point owlab at a config somewhere else

```console
$ owlab --config ../other-project/owlab.yaml up
$ owlab --config ../other-project status        # a directory works too
$ export OWLAB_CONFIG=~/src/other-project
```

`--config` works before or after the command name. Without it, owlab searches the working directory
and its parents.

---

## Publish images, cut a release

See [releasing](releasing.md).
