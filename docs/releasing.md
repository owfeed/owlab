# Releasing

[Русская версия](releasing_ru.md)

How to cut an owlab release, and what happens after you push the tag. For a maintainer who can push
tags to the repository.

- **The tag is the only input.** The version compiled into the binary, the archive names, the release
  title and the release body all come from it.
- **The changelog gates the release.** The release fails if `CHANGELOG.md` has no section for the
  version.
- **Router images** are published by a separate workflow, `images.yml`, and are never deleted.

| I want to… | Section |
|---|---|
| cut a release | [Cutting a release](#cutting-a-release) |
| recover from a failed release run | [If the release workflow fails](#if-the-release-workflow-fails) |
| know what the release workflow does | [What the tag does](#what-the-tag-does) |
| know how the binary reports its version | [Version numbers](#version-numbers) |
| understand how router images are tagged | [Router images](#router-images) |
| move the OpenWrt release pin | [Tracking upstream](#tracking-upstream) |
| pin an image that never changes | [Immutable tags](#immutable-tags) |

## Cutting a release

1. In `CHANGELOG.md`, turn the `## [Unreleased]` heading into the version and the date. See
   [The changelog edit](#the-changelog-edit).
2. At the bottom of `CHANGELOG.md`, move the `[Unreleased]` link to the new tag and add the comparison
   link for the version.
3. Move every version reference in the table below to the tag about to exist.
4. Tag and push:

   ```console
   $ git tag -a v0.2.0 -m 'owlab 0.2.0'
   $ git push origin v0.2.0
   ```

That is the whole procedure.

### What moves on release

| Reference | Where |
|---|---|
| `owlab/action@vX.Y.Z` and `owlab/setup@vX.Y.Z`, including the short forms in prose (`setup@…`, `action@…`) | `SECURITY.md`, both READMEs, `docs/` and `examples/workflow/` |
| The version in prose next to them, such as "installs vX.Y.Z" | `SECURITY.md` and `docs/` |
| The `gh release download` example, and the archive name in it and in the `gh attestation verify` line after it | `SECURITY.md` |

- **The Russian pages carry the same references.** `README_ru.md`, `docs/reference_ru.md` and
  `docs/runbook_ru.md` move with their English pairs.
- **Move these *before* the tag**, not after, so the tag contains a README and an example that point
  at itself.

To list every reference that is left, run this from the repository root:

```sh
grep -rnE '(action|setup)@v[0-9]|owlab v[0-9]|installs v[0-9]|gh release download v|owlab_[0-9]' \
  SECURITY.md README.md README_ru.md docs examples/workflow
```

`tools/pins.sh` does not help here. It moves the OpenWrt release pins, not owlab's own version.

<details>
<summary>Why exact tags, not a floating <code>v1</code></summary>

The action decides whether somebody's package is broken. A repository pinning `owlab/action@vX.Y.Z`
should get the assertion behaviour that was reviewed with it. It should not get one that can change
with no commit in their repository at all.

</details>

### The changelog edit

`tools/release-notes.sh` takes the release body from the section whose heading names the version in
brackets. Before the release, the top of `CHANGELOG.md` reads:

```markdown
## [Unreleased]
```

After it, with today's date:

```markdown
## [X.Y.Z] - YYYY-MM-DD
```

At the bottom, the `[Unreleased]` link moves to the new tag and a link for the version is added below
it:

```markdown
[Unreleased]: https://github.com/owfeed/owlab/compare/vX.Y.Z...HEAD
[X.Y.Z]: https://github.com/owfeed/owlab/compare/vP.Q.R...vX.Y.Z
```

`vP.Q.R` is the previous release. The link lines are left out of the release body.

### Check that it worked

The release workflow's last step downloads `releases/latest/download/SHA256SUMS` and compares it to
what the run built. If the step passes, `releases/latest` serves the `SHA256SUMS` this run built.

A 404 there means every download would have failed. That is the last moment it is a red build rather
than a bug report.

## What the tag does

`release.yml` runs these steps:

1. Builds six archives: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 and
   windows/arm64. Each carries the binary, the README, the changelog, the licence and `docs/`.
2. Writes `SHA256SUMS`.
3. Publishes the release as a **draft**.
4. Flips the draft to published.
5. Downloads `releases/latest/download/SHA256SUMS` and compares it to what this run built.

Each archive is attested before it is uploaded.

Between steps 1 and 2, the release job builds the notes from `CHANGELOG.md`. A version with no
section stops the run there, with `no CHANGELOG section for version X.Y.Z`.

After the release, `release.yml` calls `images.yml` with the tag, which builds the router images with an
immutable tag. See [Immutable tags](#immutable-tags).

### If the release workflow fails

`release.yml` can be run again by hand for a tag that already exists:

```console
$ gh workflow run release.yml -f tag=vX.Y.Z
```

- **The rerun checks out the tag**, so it reads `CHANGELOG.md` and the code as the tag has them. It
  fixes a failure from outside the repository, such as a network error.
- **A failure in the files** needs a commit, and a tag that contains it. A missing changelog section is
  one of these.
- **Only the images failed?** `images.yml` takes the tag as its `version` input, so a manual run still
  adds the immutable tags:

  ```console
  $ gh workflow run images.yml -f version=vX.Y.Z
  ```

<details>
<summary>Why the release starts as a draft</summary>

`releases/latest` moves the moment a release is published. The upload action creates the release
first and uploads after. A release published up front therefore has a window in which `latest`
resolves to something with no assets in it.

A draft is not `latest`, so the switch happens once, with everything already in place.

</details>

### How the attestation is checked

- `owlab/setup` verifies the attestation with `--signer-workflow`, not only `--repo`.
- It does this before the binary is executed or put on `PATH`.
- It refuses to install a binary that does not verify.
- `SHA256SUMS` is published too and is not a substitute. The same host serves it, from the same
  release, so whoever can replace one can replace the other.

### Why one third-party action is pinned by commit

`softprops/action-gh-release` is pinned by commit, not by its `v3` tag. It is the only third-party
action in the repository, and it runs in the only job that can write to a release. Whoever could move
that tag could publish assets under our name.

## Version numbers

owlab uses semver, with `owlab.yaml` as the compatibility surface.

- A `version: 1` config that works today keeps working across minor and patch releases.
- A change that would break one waits for a major release.

The binary answers three ways, in order of preference:

1. What the release build stamped with `-ldflags -X main.version=…`.
2. What the module system recorded, which is how `go install …@v0.2.0` knows
   its own version.
3. `dev`, for a plain `go build` from a checkout.

The commit and build date come either from the stamp or from the VCS information the Go toolchain
embeds. So `owlab version` says something true in every case.

The build date is the commit date, not the wall clock. Rebuilding the same tag should produce the same
file.

<details>
<summary>Why the version matters more here than in most tools</summary>

owlab carries the image build context inside the binary. So "which owlab built this router" is a real
question.

</details>

## Router images

`images.yml` publishes to `ghcr.io/owfeed/owlab-rootfs`. It runs weekly, on pushes that touch the image
inputs, and after a release.

One tag is one target: distro, exact release, architecture.

<details>
<summary>Why one tag per target, not a manifest list</summary>

An OCI image index has no field for "distro version", and most OpenWrt architecture names have no valid
GOARCH mapping. A manifest list cannot express this matrix, and upstream does not try either.

</details>

### Tracking upstream

`images/owlab.yaml` pins one release per branch.

- **The pin** is what a developer building locally gets.
- **The pin is also the floor.** The published set is the newest `keep` point releases of those same
  branches, resolved against the download server at build time.
- **`keep`** defaults to 2. It is an input on both the manual and the called runs.

So when upstream tags 25.12.5, an image lands on GHCR the following Monday without anyone editing a
version number. 25.12.4 stays available for whoever has not moved yet.

To see where things stand:

```console
$ cd images && owlab releases
```

#### Move the pin

Moving the pin is not a one-line edit. The same release number is copied into `images/Dockerfile`, and
`ci.yml` fails a copy that names a release `images/owlab.yaml` does not pin.

- Run `sh tools/pins.sh bump <old> <new>`. It moves every copy at once.
- Or merge the pull request `pins.yml` opens: it proposes that bump daily.

Both are in the [runbook](runbook.md#check-the-copies-of-the-pin).

Do not write a release number into a workflow: `tools/pins.sh check` fails it. The workflows name no
release. `ci.yml` reads its e2e releases from `images/owlab.yaml` at run time.

<details>
<summary>Why the workflows read the pin at run time</summary>

`pins.yml` pushes with `GITHUB_TOKEN`, and GitHub refuses any such push that changes a file in
`.github/workflows/`.

</details>

#### What the resolver checks

The resolver checks two things a listing alone does not tell you:

- **The release has artifacts for the target.** A point release appears in the listing when it is
  tagged. A given target's images land when that target's build finishes. Taking the listing at its
  word produces a job that 404s twenty minutes in.
- **The pinned release is always built,** even when it has fallen off the end of the list. Someone may
  be pinning it in their own `owlab.yaml`, and the weekly rebuild keeps that image current with its
  feed.

If the download server cannot be read, the matrix falls back to the pins. A publish job that fails
because a listing timed out would be worse than one that publishes what the config already says.

### Immutable tags

A release adds a second tag beside the moving one:

```
ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.5-x86_64          # moves
ghcr.io/owfeed/owlab-rootfs:openwrt-25.12.5-x86_64-v0.2.0   # does not
```

- **The moving tag** is rebuilt weekly and picks up feed changes within the same pinned release.
  Develop against it; do not pin it.
- **The versioned tag** does not change. Point `image:` at it when you need the same bytes twice.

## Nothing is ever deleted

Old images stay on GHCR. The resolver stops *building* old releases; it does not remove them.

Somebody's `owlab.yaml` pins them, and a tag that stops resolving is a broken build for a stranger.
