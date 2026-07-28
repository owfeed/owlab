# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

`owlab.yaml` is the compatibility surface. A `version: 1` config that works
today keeps working across minor and patch releases; a change that would break
one waits for a major.

## [Unreleased]

### Changed

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

[Unreleased]: https://github.com/VizzleTF/owlab/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/VizzleTF/owlab/releases/tag/v0.1.0
