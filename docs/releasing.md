# Releasing

[Русская версия](releasing_ru.md)

## Cutting a release

Move the `## [Unreleased]` heading in `CHANGELOG.md` down to a version and a
date, add the comparison link at the bottom, move the `owlab/action@vX.Y.Z` and
`owlab/setup@vX.Y.Z` references in both READMEs, `docs/` and
`examples/workflow/` to the tag about to exist, then:

```console
$ git tag -a v0.2.0 -m 'owlab 0.2.0'
$ git push origin v0.2.0
```

That is the whole procedure. The tag is the only input: the version compiled
into the binary, the archive names, the release title and the release body all
come from it.

The release fails if `CHANGELOG.md` has no section for the version. Pushing a
tag nobody wrote a changelog entry for should not publish an empty release
page.

## What the tag does

`release.yml` builds five archives — linux/amd64, linux/arm64, darwin/amd64,
darwin/arm64, windows/amd64 — each carrying the binary, the README, the
changelog, the licence and `docs/`. Then it writes `SHA256SUMS`, publishes the
release as a **draft**, and flips it to published afterwards.

The draft matters. `releases/latest` moves the moment a release is published,
and the upload action creates the release first and uploads after — so a
release published up front has a window in which `latest` resolves to something
with no assets in it. A draft is not `latest`, so the switch happens once, with
everything already in place.

The last step then downloads `releases/latest/download/SHA256SUMS` and compares
it to what was just built. A 404 there means every download would have failed,
and that is the last moment it is a red build rather than a bug report.

Each archive is attested before it is uploaded. `owlab/setup` verifies that
attestation with `--signer-workflow` — not merely `--repo` — before the binary
is executed or put on `PATH`, and refuses to install one that does not verify.
`SHA256SUMS` is published too and is not a substitute: it is served by the same
host, from the same release, so whoever can replace one can replace the other.

The action references are moved *before* the tag rather than after, so the tag
contains a README and an example pointing at itself. They are exact tags rather
than a floating `v1` because the action decides whether somebody's package is
broken: a repository pinning `owlab/action@vX.Y.Z` should get the assertion
behaviour that was reviewed with it, not one that can change with no commit in
their repository at all.

`softprops/action-gh-release` is pinned by commit, not by its `v3` tag. It is
the only third-party action in the repository and it runs in the only job that
can write to a release, so whoever could move that tag could publish assets
under our name.

## Version numbers

Semver, with `owlab.yaml` as the compatibility surface. A `version: 1` config
that works today keeps working across minor and patch releases; a change that
would break one waits for a major.

The binary answers three ways, in order of preference:

1. What the release build stamped with `-ldflags -X main.version=…`.
2. What the module system recorded, which is how `go install …@v0.2.0` knows
   its own version.
3. `dev`, for a plain `go build` from a checkout.

The commit and build date come along either from the stamp or from the VCS
information the Go toolchain embeds, so `owlab version` says something true in
every case. It matters more here than in most tools: owlab carries the image
build context inside the binary, so "which owlab built this router" is a real
question.

The build date is the commit date, not the wall clock. Rebuilding the same tag
should produce the same file.

## Router images

`images.yml` publishes to `ghcr.io/owfeed/owlab-rootfs`. One tag is one
target — distro, exact release, architecture — because an OCI image index has
no field for "distro version" and most OpenWrt architecture names have no valid
GOARCH mapping. A manifest list cannot express this matrix, and upstream does
not try either.

It runs weekly, on pushes that touch the image inputs, and after a release.

### Tracking upstream

`images/owlab.yaml` pins one release per branch. That pin is what a developer
building locally gets, and it is also the floor: the published set is the
newest `keep` point releases of those same branches, resolved against the
download server at build time.

So upstream tagging 25.12.5 puts an image on GHCR the following Monday without
anyone editing a version number, and 25.12.4 stays available for whoever has
not moved yet. `keep` defaults to 2 and is an input on both the manual and the
called runs.

Two things the resolver checks that a listing alone does not tell you:

- **The release actually has artifacts for the target.** A point release
  appears in the listing when it is tagged; a given target's images land when
  that target's build finishes. Taking the listing at its word produces a job
  that 404s twenty minutes in.
- **The pinned release is always built**, even when it has fallen off the end
  of the list. Someone may be pinning it in their own `owlab.yaml`, and the
  weekly rebuild is what keeps that image current with its feed.

If the download server cannot be read, the matrix falls back to the pins. A
publish job that fails because a listing timed out would be worse than one that
publishes what the config already says.

To see where things stand:

```console
$ cd images && owlab releases
```

### Immutable tags

A release adds a second tag beside the moving one:

```
ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.5-x86_64          # moves
ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.5-x86_64-v0.2.0   # does not
```

The moving tag is rebuilt weekly and picks up feed changes within the same
pinned release, so it is the right thing to develop against and the wrong thing
to pin. Point `image:` at the versioned one when you need the same bytes twice.

## Nothing is ever deleted

Old images stay on GHCR. Somebody's `owlab.yaml` pins them, and a tag that
stops resolving is a broken build for a stranger. The resolver stops *building*
old releases; it does not remove them.
