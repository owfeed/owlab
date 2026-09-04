# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

`owlab.yaml` is the compatibility surface. A `version: 1` config that works
today keeps working across minor and patch releases; a change that would break
one waits for a major.

## [0.6.0] - 2026-09-04

### Added

- **A release pin that goes stale now fails a build.** `sh tools/pins.sh check`
  reads the pins out of `images/owlab.yaml` and refuses any release literal in
  `images/Dockerfile` or a workflow that names a release the config does not
  pin; `ci.yml` runs it on every push. Nothing caught this before, because an
  old release still builds — on 2026-09-04 `ARG BASE_IMAGE` and both release
  literals in `ci.yml` still said 25.12.4 while the config had been on 25.12.5
  since it was written, and every job was green the whole time. Comments,
  `examples/`, the READMEs and `docs/` are outside the check on purpose: a
  release number there is an illustration, and the README's sample `owlab
  releases` output shows a pin one release behind deliberately.
- **`pins.yml` proposes the bump upstream has made available.** Daily, it asks
  `owlab releases --json` — the report that has existed since 0.2.0 and that
  nothing read — and when `stale` is not zero it rewrites the pins with
  `tools/pins.sh bump`, validates the result with `tools/pins.sh check` and
  `owlab context --list`, and opens a pull request. It never pushes to `main`:
  what proves a new release still builds a router is the CI run on that pull
  request. It then dispatches `ci.yml` on the pin branch, because a pull request
  authored by `app/github-actions` has its `pull_request` run held in
  `action_required` under this repository's approval policy, and
  `workflow_dispatch` is GitHub's own documented exception to that.

### Changed

- The e2e matrix, the `action` job's `releases:` input and `ARG BASE_IMAGE` move
  from 25.12.4 to 25.12.5, which is what `images/owlab.yaml` has pinned all
  along. `openwrt/rootfs:x86_64-25.12.5` was not on Docker Hub when this landed
  — upstream tags the download server first — and it does not matter: `owlab
  context` asks the registry and falls back to the rootfs tarball, which is the
  path the published 25.12.5 images were already built through.

### Fixed

- **One router's `extra_packages` no longer cancels every other router.**
  `docker compose build` puts the whole lab in one buildkit solve, and buildkit
  cancels the solve on the first target that fails — so the extras step, which
  ran under `set -eu` with no guard, turned one unusable file into a failed
  lab. Measured 2026-09-04 on a three-router stand: one `.ipk` with an
  unsatisfiable dependency exited 255 and took `#16 CANCELED`, `#13 CANCELED`
  and `owlab: build failed: exit status 1` with it. Both release lines did it —
  it is `set -eu`, not the package manager: the same stand with a `.apk` that
  apk answers `unable to select packages` exited 27 and cancelled its
  neighbour. There is nothing to configure around it, either: neither `docker
  compose build` nor `docker buildx bake` has a `--keep-going`.

  The extras step now installs the set as before, and only if that fails
  installs each file on its own, in the staged order, so the good ones still
  land. The successful path is byte-for-byte the run it was.

- **A package that did not install is now said out loud instead of being
  fatal.** Tolerating a failure silently would be worse than the cancelled
  builds it replaces, so the retry names each file it could not install, writes
  the list to `/etc/owlab/extras-failed`, and `owlab up` reads it back off each
  running router and prints it under the ready table:
  `! owrt2410 is running WITHOUT luci-app-example_1.0_all.ipk`. `owlab up`
  exits non-zero when it prints that — the lab is up, and it is not what the
  config describes — and `owlab test` gains an `extra_packages` step that fails
  the router before any assertion runs against a box missing what it was told
  to have. Read as-built at any time with
  `owlab exec <router> -- cat /etc/owlab/extras-failed`.

## [0.5.6] - 2026-09-04

### Fixed

- **A 24.10 image build no longer fails on a package nobody asked for.** `opkg update`
  ran in an earlier layer than the install of the `extra_packages:` files, and buildkit
  keeps that layer for as long as the release and the feed package list hold — while the
  install layer re-runs on every change to the staged files. The index the install read
  could therefore be months old, and a pin does not make that safe: OpenWrt rebuilds the
  packages inside `releases/24.10.8/` in place, under the same version string. Measured
  on 2026-09-04 against an image built 2026-07-29, `bash 5.2.37-r1` was 473650 bytes /
  `f1872e60…` in the image's index and 473647 bytes / `20eaa220…` on the server, which
  opkg reports as `Checksum or size mismatch for package bash` and the build as
  `exit code: 255`. `opkg update` now runs in the same layer as the install, and
  `owlab test` refreshes the index before installing a local file too — a package
  installed by path still resolves its dependencies out of the index. Nothing that
  cached before stops caching: the refresh is inside the branch that has something to
  install, in the layer that was re-running anyway, and costs about 1.6 s and a megabyte
  of gzipped indexes per opkg router. The apk line needs none of this, measured:
  apk-tools 3.0.5 revalidates a cached index older than `--cache-max-age` (4 hours by
  default) on its own, and re-downloaded every APKINDEX before resolving in a month-old
  image.

## [0.5.5] - 2026-09-04

### Fixed

- **A router whose lan network resolves to no firewall zone is reachable again.** fw4
  renders a zone as `iifname "<dev>" jump input_<zone>` and takes `<dev>` from the
  zone's `network` list as it builds the ruleset; a zone that resolves to no device
  emits no jump, so lan traffic falls off the end of the input chain into
  `jump handle_reject` and the router answers a published port with a TCP reset —
  an empty reply from the host, `Connection refused` from a sibling container —
  while `uhttpd` is up and listening on `0.0.0.0:80` (owfeed/owlab#3, reported on
  ImmortalWrt with the OpenWrt routers on the same stand unaffected). `95_owlab-base`
  now names the lan device on that zone (`list device 'br-lan'`), sets its `input` to
  `ACCEPT`, and creates the zone if the image ships none for the lan network. `device`
  is taken literally rather than resolved through netifd, so the jump does not depend
  on what netifd had done when fw4 read the config; fw4 deduplicates it against the
  network, leaving one jump line. **The firewall stays on** — fw4 works in a container,
  and disabling it would hide a whole class of real behaviour.
- **dropbear listens on `0.0.0.0:22` on ImmortalWrt too.** ImmortalWrt ships
  `option Interface 'lan'` in `/etc/config/dropbear` and OpenWrt does not, so dropbear
  bound `192.168.163.5:22` instead of the wildcard and ssh to the published port was
  closed as it opened. `95_owlab-base` deletes the key.

- **`owlab test --install` no longer hands a router the other release line's package
  format.** The glob was expanded once on the host and the whole result given to every
  router, so a run covering both lines gave the apk box an `.ipk` and the opkg box an
  `.apk` — which is exactly the layout `owfeed build` produces (`dist/noarch/*.apk`
  beside `dist/all/*.ipk`). apk answered with `v2 package format error`, and because
  every file goes into one command it failed the package that would have installed
  along with it, taking every assertion after it down too. Each router now receives
  only the format its package manager reads, filtered **before** the file is pushed;
  anything with another extension passes through as before, and a single-release run
  is unchanged. **A file left out is named** — `owlab: skipping foo.ipk (this router
  uses apk)` — because a package manager invoked with no arguments succeeds, so a glob
  that matched only the other line would otherwise be a green line claiming an install
  that never happened. A router left with nothing to install is reported as a skip
  rather than run. `owlab install` filters the same way, from the same place in
  `internal/pkgmgr`, so the two tiers cannot drift apart.

## [0.5.4] - 2026-09-01

### Fixed

- **`owlab test` no longer fails because something else on the host holds 2222.** Its
  synthesised routers took `8080 + index` and `2222 + index` unconditionally, so one
  unrelated listener — an ssh container, a tunnel, another project's stand — failed the
  whole run with `driver failed programming external connectivity`, naming no port. They
  now take the first free port from that base, remembering what the same pass has already
  handed out: probing alone gave two routers the same port, which the config gate caught
  as "openwrt-25.12.4 and openwrt-24.10.8 both use host ssh port 2223". **Configured
  routers are untouched** — a port written in `owlab.yaml` is a promise, and quietly
  serving a different one is worse than failing; the probe would also have made the port
  a router gets depend on what else the machine happens to be running, which `internal/qemu`
  caught as a test expecting 2222 and getting 2223.

## [0.5.3] - 2026-07-29

`fidelity: vm` on Windows. It compiled there from the beginning and had
per-platform process handling written for it, which is not the same as working:
the first thing it did on a real Windows host was hand QEMU a path only unix
has, and the second was fail to mention that QEMU had exited.

### Fixed

- **A VM would not boot on Windows at all.** QEMU was given
  `-object rng-random,filename=/dev/urandom` for the entropy OpenWrt's boot
  waits on. Windows has no `/dev/urandom`, so QEMU exited before the guest
  started. It is `rng-builtin` there now — the same virtio-rng to the guest,
  with no host character device involved.
- **A QEMU that failed to start was reported as started.** On Windows the
  process is launched rather than run to completion, so `Start` returned
  success for a process that was already dead and its stderr went nowhere. The
  developer got a wait-for-ssh timeout naming nothing. owlab now waits, notices
  a QEMU that has already exited, and reports what it said.
- **A VM died with the terminal that started it.** A child process joins its
  parent's console group and receives `CTRL_CLOSE_EVENT` when that window
  closes. It is now started with `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`,
  which also means Ctrl-C during `owlab up` interrupts the wait rather than the
  router.
- **QEMU installed correctly was reported as not installed.** Neither the
  official Windows installer nor `winget` puts QEMU on `PATH`, and owlab looked
  only there — so a developer who had just installed it exactly as instructed
  was told to install it. `PATH` is still first, followed by the directories
  the installers actually use: `%ProgramFiles%\qemu`, `%LOCALAPPDATA%\Programs\qemu`,
  the scoop app directory, chocolatey's, and `/opt/homebrew/bin` on macOS.
- **`qemu-img` is taken from beside the emulator** rather than from `PATH`. The
  two have to come from the same install — a `qemu-img` from a different QEMU
  can write a qcow2 the emulator then refuses — and on Windows neither is on
  `PATH` for a lookup to find.
- **UEFI firmware is looked for beside the binary too.** A unix install puts
  `edk2-*.fd` in `../share/qemu`; the Windows build keeps it next to
  `qemu-system-*.exe`.

### Added

- `OWLAB_ACCEL` forces an accelerator. `OWLAB_ACCEL=tcg` is the escape hatch
  for a host where hardware acceleration does not work, which on Windows is not
  hypothetical: WHPX sits on top of Hyper-V and is reported to hang some
  machines on an SMP boot while working on the next one over.
- `owlab doctor` prints the `qemu-system-*` and `qemu-img` it settled on, so
  "found outside PATH" is visible rather than merely true, and names the exact
  `dism.exe` command that enables Windows Hypervisor Platform when `whpx` is
  missing.
- The install hint is now chosen by which package manager is on the machine —
  winget, scoop or chocolatey; brew or port; apt, dnf, pacman, zypper or apk —
  rather than guessed from the OS. A missing QEMU also names every directory
  searched and the `OWLAB_QEMU` override.
- An `Environment` section in the reference listing every `OWLAB_*` variable,
  and troubleshooting entries for each failure above, by symptom.

## [0.5.2] - 2026-07-29

The README has said "Linux, WSL2, Docker Desktop for Windows, or macOS" since the
first release, and CI proved a third of it: the unit tests ran on Linux, the other
platforms got a cross-compile. A cross-compile shows the per-platform code parses.
Everything below is what it does not show.

### Fixed

- **A synced file's permissions no longer come from the host's filesystem.** They
  are derived from the destination and the contents — `0755` under `/etc/init.d`,
  `/usr/bin` and the rest, or for anything starting with `#!`; `0644` otherwise.
  These are luci.mk's own two modes, so a synced tree now matches what the built
  package installs. Windows has no execute bit and reports every file as `0666`,
  which meant an init script synced from Windows arrived non-executable and procd
  reported the service as not existing at all. A Windows drive mounted into WSL
  had the opposite problem, reporting everything as `0777`.
- **`project.build` on Windows.** It is run by `sh`, which is not on `PATH` there
  by default. owlab now looks for `sh` and then `bash` — Git for Windows ships
  both — and when neither is present says so, naming the cause, instead of failing
  with a bare "file not found". Nothing is substituted: handing a POSIX command
  line to `cmd.exe` would quietly run something else.
- **ssh to a `fidelity: vm` router from Windows.** The known-hosts file was the
  literal `/dev/null`, which Win32-OpenSSH resolves against the current drive and
  turns into `C:\dev\null` — a path it cannot open, complained about on every
  connection. It is now `os.DevNull`, which is `NUL` there.

### Added

- `owlab doctor` checks the two things Windows ships as optional features rather
  than defaults: an ssh client (needed only for `fidelity: vm`) and a POSIX shell
  (needed only for `project.build`). Both are warnings that say what they cost.
- The unit tests, plus `owlab doctor` and `owlab version`, now run on
  `windows-2025` and `macos-15` in CI. No e2e there — GitHub's runners for those
  platforms have no container engine that runs Linux images.
- `windows/arm64` release builds. The machines exist and `go install` already
  worked on them; now there is a binary for anyone without a Go toolchain.
- An install section in the [runbook](docs/runbook.md#install-owlab-on-this-machine),
  one part per platform, covering what each needs beyond the binary — the `kvm`
  group on Linux, the WSL2 filesystem rule, Windows Hypervisor Platform and the
  two optional features on Windows. The README points at it and now also mentions
  the release archives, which were previously published and never documented.
- The reference documents what `sync` copies, with which permissions and why, and
  what `project.build` and `project.post_sync` each run under.

### Changed

- `owlab/setup` on a Windows runner now explains that the runner has no engine for
  Linux images, rather than claiming owlab has no Windows build — which stopped
  being true in this release.

## [0.5.1] - 2026-07-29

No code changes. Cut so that the tag every consumer pins is an immutable release.

v0.5.0 was tagged before immutable releases were enabled on this repository, and
the setting does not apply retroactively -- so the tag that `owlab/action@v0.5.0`
resolves to could still be moved. That is the assumption the ecosystem's pinning
convention rests on: internal `owfeed/*` references are pinned by tag rather than
by commit SHA, and the argument for that is a tag under our own control plus a
binary verified against its build attestation. Immutability is what makes the
first half true rather than merely intended.

## [0.5.0] - 2026-07-29

### Changed

- **owlab lives at `github.com/owfeed/owlab`, and its module path is
  `owfeed.org/owlab`.** `go install github.com/VizzleTF/owlab/...` stops working;
  `go install owfeed.org/owlab/cmd/owlab@latest` replaces it. The path names a
  host rather than a forge so that this is the last move that breaks anyone's
  install: Go module paths have no redirect, and neither does `uses:` in Actions.
- **Release attestations now name `owfeed/owlab`.** An attestation records the
  repository that produced it, so binaries released before this one no longer
  verify against the new name and `owlab/setup` at v0.5.0 refuses them. Pin the
  action and the `version:` input to the same release, which is what the docs
  have always said and now matters.
- **Router images publish to `ghcr.io/owfeed/owlab-rootfs`.** A GHCR package
  belongs to the account that pushed it and does not travel with a repository
  transfer, so `ghcr.io/vizzletf/owlab-rootfs` still exists and still serves the
  releases up to v0.4.1 that pull from it. It must not be deleted.

## [0.4.1] - 2026-07-29

### Fixed

- `owlab/action` and `owlab/setup` used twice in one job failed the second time.
  `gh release download` refuses to overwrite a file it downloaded a minute
  earlier, and there was no reason to download it again. Two uses in a job is an
  ordinary thing to want: 25.12 installs an apk and 24.10 an ipk, so proving a
  package works on both releases is two steps. The install now returns early when
  the requested version is already there, and clobbers when a different version
  is, so a second step asking for a different `version:` still gets it.

## [0.4.0] - 2026-07-28

### Added

- `--feed` accepts `{host}` for the machine running owlab, and substitutes the
  address the router actually reaches it at. Serving a freshly built feed over
  HTTP and installing from it is how a feed's CI proves that what it publishes
  works, and until now that step had no portable way to name the server: a
  container on a Linux runner reaches the host at the bridge gateway, a container
  under Docker Desktop has no gateway to reach, and a `fidelity: vm` router sees
  neither because QEMU's user-mode stack answers somewhere else. Every pipeline
  that tried hardcoded `172.17.0.1` and broke on the first developer machine. A
  URL without the token is passed through unchanged.
- Generated containers carry `extra_hosts: host.docker.internal:host-gateway`,
  which is what the substitution above resolves through. The daemon knows where
  the host is; owlab no longer guesses.
- `owlab releases --all --json` reports the package manager each branch ships.
  owfeed answers the same question from the same server and neither tool reads
  the other; without this the two could disagree about apk against opkg and
  nothing would notice until a router on a 24.10 line got a feed it cannot read.

### Fixed

- `owlab releases` no longer needs an `owlab.yaml`, or a container engine. Asking
  a download server what it publishes requires neither, and demanding both made
  the command unusable from the one place it is most useful — a shell that is not
  a package repository. It now loads a project's config when there is one, and
  falls back to listing every distribution when there is not, because "how stale
  are the pins" is a question about a project and the honest answer without one
  is the full list.

## [0.3.0] - 2026-07-28

### Added

- `owlab install --feed` and `owlab test --feed` add a package feed to a router
  before installing, so a package can be installed BY NAME out of a signed index
  instead of from a file. That difference is the point: installing a file proves
  the package works, installing it by name proves the channel does — the index
  parses, the URL does not redirect, and the key on the router matches the one
  that signed it. A file install cannot fail in any of those ways. `--feed-key`
  carries the public half; for opkg its FILENAME must be the key id, because
  that is what opkg looks a key up by. Verified against a live feed on both
  branches, including the negative: with the key removed the router reports the
  package as not existing at all, which is what makes the positive result mean
  something. `owlab/action` takes the same three inputs.

## [0.2.0] - 2026-07-28

### Added

- `owlab test`: start the routers, install the package, assert against the
  running router, tear everything down, exit 0 or 1. It works with no
  `owlab.yaml` — `--release 25.12.5 --release 24.10.8` is the configuration —
  so a package repository can adopt it without adding a file that says nothing
  its flags do not. Teardown happens whether the run passed or failed, because
  a failed run holding 8080 and 2222 makes the *next* run fail for an unrelated
  reason.
- Assertions, one line each: `http <status> <path>`, `service`, `file`,
  `package`, `uci`, `exec`. The HTTP one logs into LuCI as root first — every
  page an app exists to serve is under `/cgi-bin/luci/admin`, and an
  unauthenticated request there is a redirect to the login form, which is why a
  hand-written `curl` reports 403 on a healthy page. It also fails a 200 whose
  body is a dispatcher error page, which a status check cannot see. Everything
  else runs over the transport `sync` uses, so assertions work the same on a
  container and on a `fidelity: vm` router.
- `VizzleTF/owlab/action`, which is all of the above as one GitHub Actions
  step, with a table in the job summary naming the router and the check that
  failed. `VizzleTF/owlab/setup` installs the binary alone.
- Release archives now carry build provenance attestations, and both actions
  verify one — with `--signer-workflow`, not merely `--repo` — before the binary
  is executed or put on `PATH`.
- `--json` on `test`, `status` and `releases`, each document carrying a `schema`
  field. `test --json` moves its progress output, and docker's, to stderr, so
  the stdout side is a clean pipe.
- `owlab build` no longer needs an `owlab.yaml`: `--release` and the package
  Makefile are everything it reads. This is what makes a build step in a package
  repository a single line.

### Fixed

- `owlab logs` was empty on a healthy container router. procd logs through
  syslogd, which writes to a ring buffer rather than to the console, so the
  container's stream holds nothing worth reading. It now asks the router for its
  own log and falls back to the container stream.

- `owlab down` and `owlab logs` no longer download anything. Both went through
  the same preparation `owlab up` does, so stopping a router or reading its log
  rebuilt the whole build context and fetched the compliance bundle — which
  made them fail outright on a machine with no network.
- `owlab doctor` warns about a project on a Windows drive under WSL even when
  Docker is absent. The check read a field that was only filled in once a
  daemon had been found, so a project whose routers are all `fidelity: vm` —
  the one that needs the warning most — never got it.
- `owlab install` no longer reports a router as failed and then installs on it
  anyway. When pushing a local `.apk`/`.ipk` failed, the package manager still
  ran against a path that was not there and reported its own confusion instead
  of the real error. A router is also listed at most once in the failure line.

### Changed

- `owlab build` writes into `dist/<arch>/` rather than a flat directory, which
  is [owfeed's artifact contract][artifact-contract] — so its output can be
  handed to a publishing tool directly, with a file format as the interface
  rather than either tool depending on the other. An architecture-independent
  package writes both spellings, `dist/noarch/` for the apk and `dist/all/` for
  the ipk, because apk rejects `all` as uninstallable and opkg has never heard
  of `noarch`. The architecture comes from `PKG_ARCH` or `LUCI_PKGARCH` in the
  Makefile, falling back to the target's. `--layout flat` restores the previous
  output for one release; a pattern that used to read `dist/*.apk` now needs the
  architecture component, `dist/*/*.apk`.
- `owlab install` re-asserts `project.theme` the way `owlab sync` does. A
  package can register a theme without selecting it, and a half-installed theme
  is the one case where LuCI crashes rather than falling back.
- The documentation is a runbook now. `docs/runbook.md` is procedures — a goal,
  the commands, and how to tell it worked; `docs/troubleshooting.md` is sorted
  by symptom; the reference, the internals and the release process each have a
  page. Every one of them, and both README files, exist in English and Russian.

## [0.1.0] - 2026-07-28

First release.

### Added

- `owlab up`, `down`, `shell`, `exec`, `logs`, `status`, `open` — dev routers
  described by an `owlab.yaml` next to your package.
- Two tiers. `basic` runs a container with procd as PID 1; `vm` boots a real
  OpenWrt kernel under QEMU on the host, where `kmod-*` packages load and two
  `mac80211_hwsim` radios come up for real.
- OpenWrt and ImmortalWrt, 24.10 and 25.12, on x86_64 and aarch64. apk and opkg
  are chosen per distribution rather than per version number.
- `owlab sync` copies your package into running routers and drops the caches
  `luci.mk`'s postinst drops. `--watch` does it on every save.
- `owlab build` produces a real `.apk` or `.ipk` through the OpenWrt SDK.
- `owlab install` for trying a package out without a rebuild, and
  `extra_packages:` for anything published outside the feeds.
- The stock package set: a router with no `packages:` gets what a shipping
  OpenWrt device has, and `+`/`-` work against it.
- Fixture profiles — invented VLANs, zones, DHCP leases, WireGuard peers — so
  LuCI renders the tables and badges an empty router never shows.
- `owlab doctor`: ports, ssh keys, line endings, QEMU and its accelerator, and
  whether the container engine forwards outbound DNS.
- Prebuilt router images on GHCR, with a GPL compliance bundle baked in.
- `owlab releases` — what the download servers publish, and how far this
  project's pins have fallen behind. `--all` lists every branch.
- `owlab version` reports the commit and build date alongside the version, and
  answers correctly for `go install ...@latest` as well as a release build.
- The published router images track upstream: the weekly rebuild resolves each
  branch to its newest point releases rather than to the pin in
  `images/owlab.yaml`, so a new OpenWrt release reaches GHCR without anyone
  editing a version number, and the previous one stays available. Images built
  at a release tag carry an immutable tag beside the moving one.

### Fixed

- The CI matrix emitted the wrong platform for a router that falls back to the
  rootfs tarball, so buildx asked for a base alpine does not publish
  (`alpine:3.22: no match for platform in manifest`). Caught by the one release
  upstream has not mirrored a container image for yet.
- `images.yml` can stamp a release tag on a manual run, so a publish that has
  to be re-run still leaves the immutable tags behind.

[artifact-contract]: https://github.com/VizzleTF/owfeed/blob/main/docs/artifact-contract.md
[Unreleased]: https://github.com/VizzleTF/owlab/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/VizzleTF/owlab/compare/v0.5.6...v0.6.0
[0.5.6]: https://github.com/VizzleTF/owlab/compare/v0.5.5...v0.5.6
[0.5.5]: https://github.com/VizzleTF/owlab/compare/v0.5.4...v0.5.5
[0.4.1]: https://github.com/VizzleTF/owlab/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/VizzleTF/owlab/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/VizzleTF/owlab/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/VizzleTF/owlab/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/VizzleTF/owlab/releases/tag/v0.1.0
