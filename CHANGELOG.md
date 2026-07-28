# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

`owlab.yaml` is the compatibility surface. A `version: 1` config that works
today keeps working across minor and patch releases; a change that would break
one waits for a major.

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
[Unreleased]: https://github.com/VizzleTF/owlab/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/VizzleTF/owlab/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/VizzleTF/owlab/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/VizzleTF/owlab/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/VizzleTF/owlab/releases/tag/v0.1.0
