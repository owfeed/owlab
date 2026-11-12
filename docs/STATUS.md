# What is built in owlab, and what is not

How much of owlab's side of the three-tool stack exists, as of 2026-11-12. For anyone deciding whether
to rely on an owlab feature. [ECOSYSTEM.md](https://github.com/owfeed/owfeed/blob/main/docs/ECOSYSTEM.md)
in owfeed says where the boundaries of the stack run and why; owlab is its bottom.

- **Verified:** both tiers, `fidelity: vm`, `owlab build`, installs from a signed feed, and
  `owlab releases` without a project.
- **Also verified:** per-format `--install`, reports of packages an image failed to install, starting
  from a saved image, the release-pin check, and unit tests on Linux, macOS and Windows.
- **Verified on Linux only:** everything with a router in it.
- **Built, not yet exercised:** `{host}` in a `--feed` URL.
- **Not built, by design:** signature verification by owlab itself, and any knowledge of the feeds
  owlab publishes to.

| Section | What it lists |
|---|---|
| [Working, and verified rather than assumed](#working-and-verified-rather-than-assumed) | Features with evidence |
| [Working, and verified on one platform](#working-and-verified-on-one-platform) | The Linux-only gap |
| [Built but not yet exercised in anger](#built-but-not-yet-exercised-in-anger) | Code that tests cover but no real run has used |
| [Not built, and why](#not-built-and-why) | What owlab will not do |
| [Where to look](#where-to-look) | The other docs |

<details>
<summary>Why this file lives in owlab, not in the shared document</summary>

The shared version went stale twice, both times on an owlab fact, because nothing in owfeed's CI ever
touches this repository. Beside the code, a claim that stops being true is a line somebody has to edit
in the same pull request that made it untrue.

</details>

## Working, and verified rather than assumed

| | Evidence |
|---|---|
| Both tiers install identically | Container over `docker exec`, VM over ssh, one `pkgmgr` producing the shell for each |
| `fidelity: vm` | `internal/qemu` boots a real image under QEMU with mac80211_hwsim radios and an extroot disk; assertions run against it unchanged from the container tier |
| `owlab build` writes `dist/<arch>/` | The layout `owfeed` and a feed's ingest both read. Built luci-theme-footstrap through the SDK and released it from that tree without rearranging anything |
| Install from a signed feed by name | `owlab test --feed` on 25.12: the package installs out of a signed index and its LuCI page renders. With the key removed the router reports it as not existing at all |
| `owlab releases` without a project | Asking a download server what it publishes needs neither an `owlab.yaml` nor a container engine, and no longer demands either |
| `--install` gives each router only its own format | `pkgmgr.FilterFiles` splits by extension before the push, so one glob over `dist/` covers both release lines; what a router did not get is named on its install line, and a router left with nothing is reported as a skip rather than a green install |
| A router built without an `extra_packages` file says so | The image records the failures in `/etc/owlab/extras-failed`; `owlab up` prints them after its table and exits non-zero, `owlab test` fails that router on an `extra_packages` step |
| A router built without a package from `packages:` says so | The image records the failures in `/etc/owlab/packages-failed`, after asking a failed download 4 times; `owlab up` prints them after its table and exits non-zero, `owlab test` fails that router on a `packages` step. A name the feeds do not carry stays a skip, and so, on opkg, does a name an installed package provides (ImmortalWrt's `dnsmasq-full` for `dnsmasq`) |
| A router starts from a saved image | `ci.yml`'s `no-build` job runs `owlab up`, saves the image with `docker save`, removes it, loads it back and starts it with `owlab up --no-build`; LuCI must answer. After a `packages:` change in `owlab.yaml`, the same image must be refused |
| The release pins cannot go stale unnoticed | `sh tools/pins.sh check` in `ci.yml` fails any release literal in a workflow (they read `images/owlab.yaml` at run time) and one in `images/Dockerfile` that `images/owlab.yaml` does not pin; `pins.yml` proposes the bump as a pull request |
| The unit tests pass on Linux, macOS and Windows | `ci.yml` runs `go test` on `ubuntu-24.04`, `macos-15` and `windows-2025`, and `owlab doctor` and `owlab version` on the bare macOS and Windows runners. Until this was added, "runs everywhere" rested on a cross-compile, and two Windows bugs were living in the gap — permissions derived from a filesystem that has none, and `project.build` run by a shell that is not there |

## Working, and verified on one platform

Everything with a router in it is verified on Linux only.

| Platform | Covered by |
|---|---|
| Linux, in CI | `ci.yml`'s `e2e` job runs `owlab test` (start, assert, tear down) on OpenWrt 25.12, OpenWrt 24.10 and ImmortalWrt 25.12, x86_64. Its `no-build` job runs `owlab up`, `down` and `up --no-build`. Its `action` job runs the composite action on one release |
| Linux, by daily use | `owlab sync`, `install` and the VM tier. No CI job runs them |
| macOS, Windows | The unit tests, the platform-specific code paths those tests reach, and `doctor` starting on a bare host. No router has booted there. |

The gap comes from the runners, not from a decision. GitHub's macOS and Windows runners have no
container engine that runs Linux images, and Docker Desktop cannot be installed on one. Closing the gap
needs hardware this project does not have.

What the [runbook](runbook.md#install-owlab-on-this-machine) says about those platforms comes from
reading the code and from what each platform documents about itself. This file marks it as such; it is
not presented as measurement.

## Built but not yet exercised in anger

**`{host}` in a `--feed` URL.**

- Tests cover the substitution and the `extra_hosts` entry it resolves through.
- The container tier's alias is Docker's own `host-gateway`.
- It shipped in 0.4.0.
- No CI run has yet served a feed off the runner and installed from it, and that is the whole reason
  the token exists. On 2026-11-12, no workflow in owfeed or owfeed-packages wrote `{host}`.
- The first such run is expected in a feed's pull-request pipeline.

## Not built, and why

**owlab does not verify signatures, and will not.**

- A package file is installed with apk's `--allow-untrusted`, because trust is not what it is
  checking. That covers `--install` and `owlab install` with a file, and `extra_packages`.
- A name installed through `--feed` gets no such flag. The router's own package manager checks the
  index signature against the key `--feed-key` put there. owlab itself checks nothing.
- That is the T0 column of the trust matrix, and the reason owlab holds no key anywhere.
- The moment owlab verifies a key, the column collapses, and every invariant written against it has an
  exception.
- `owfeed smoke` is the tool that asserts about a channel.

**owlab does not know about feeds it publishes to.**

- It neither reads nor writes an `owfeed.lock`.
- `owlab.yaml` has no `owfeed:` section.
- Why: a developer's machine ergonomics and a feed's release policy are edited by different people,
  with different amounts of review.

## Where to look

- [reference.md](reference.md) — every `owlab.yaml` key and every flag *([по-русски](reference_ru.md))*
- [runbook.md](runbook.md) — how to do a particular thing *([по-русски](runbook_ru.md))*
- [internals.md](internals.md) — why the defaults are what they are *([по-русски](internals_ru.md))*
- [ECOSYSTEM.md](https://github.com/owfeed/owfeed/blob/main/docs/ECOSYSTEM.md) — the boundary with owfeed
