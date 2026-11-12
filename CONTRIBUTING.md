# Contributing

What to run before you send a pull request to owlab, and the rules a reviewer holds every change to.
For anyone changing the code, the image or the docs.

- **Before you push:** `gofmt -l .`, `go vet ./...`, `go test ./...`.
- **If you touch how a router is built or reached,** the end-to-end job in CI is the one that tells you.
- **Docs come in pairs.** A change to an English page is not finished until the Russian page says the
  same thing, and the other way round.
- **Releasing** is in [docs/releasing.md](docs/releasing.md).

| I want to… | Section |
|---|---|
| run the checks | [The short version](#the-short-version) |
| know what the rules mean | [How to read the rules](#how-to-read-the-rules) |
| write code that passes review | [What this project is fussy about](#what-this-project-is-fussy-about) |
| add an assertion kind | [Adding an assertion kind](#adding-an-assertion-kind) |
| add a distribution | [Adding a distribution](#adding-a-distribution) |
| change docs, config, assets or the changelog | [Things that need care](#things-that-need-care) |
| cut a release | [Releasing](#releasing) |

## The short version

```sh
gofmt -l .
go vet ./...
go test ./...
```

CI runs these on every pull request, plus:

- a cross-compile for six platforms;
- a `dash -n` pass over the shell that ships inside the image and in `tools/`;
- `sh tools/pins.sh check` over the copies of the release pin;
- an end-to-end job that starts real routers on three release/distro combinations.

### Run real routers locally

Running the real thing locally takes one `owlab test` and needs only Docker:

```sh
go build -o /tmp/owlab ./cmd/owlab
mkdir -p /tmp/lab && cd /tmp/lab
/tmp/owlab test --release 25.12.5 \
  --assert 'http 200 /cgi-bin/luci/admin/status/overview' \
  --assert 'service uhttpd'
```

Documentation lives in [docs/](docs/) and exists in English and Russian.

## How to read the rules

Each rule carries one word from this scale. Cite a rule by its number, such as "see F-2".

| Word | Meaning |
|---|---|
| MUST, MUST NOT | Required. A reviewer blocks the change. |
| SHOULD, SHOULD NOT | Recommended. An exception needs a stated reason. |
| MAY | Allowed. Either choice is fine. |

## What this project is fussy about

**F-1. Comments MUST explain why, not what.**

- Why: the code says what. The reason a line exists, usually a bug it prevents and often one that
  shipped somewhere, cannot be recovered by reading the line. Nearly every unusual line here has one.
  Those sentences are the value of the file.
- Good: `--platform` is passed explicitly because most OpenWrt architecture names are not valid OCI
  platforms.
- Good: procd runs as PID 1 rather than Docker's init, because everything reading board data over ubus
  comes back empty otherwise.

**F-2. Code MUST NOT address a router by its bridge IP.**

- Why: published ports on localhost are the only way in that works identically on native Linux, WSL2,
  Docker Desktop for Mac and Windows, OrbStack and Colima.
- Bad: code that reaches a container by its subnet address. It works on the machine it was written on
  and nowhere else.
- Good: a published port on localhost.

**F-3. A fixture MUST NOT touch `lan`.**

- Why: `lan` is eth0, with the container engine's own address on it. It is the only way in, and there
  is no console to recover from.
- Good: invented networks sit on dummy devices or on VLANs that carry no traffic.

**F-4. A tier that cannot be delivered MUST be refused, not silently downgraded.**

- Why: quietly starting something less than what was asked for produces bug reports nobody can
  reproduce.
- Good: `fidelity: vm` on a machine with no QEMU is an error naming what is missing.

**F-5. Every path that installs, queries or removes a package MUST handle both package managers.**

- Why: apk runs from 25.12, opkg on 24.10, and neither understands the other's command.
- Where: `internal/pkgmgr` builds the shell in one place for this reason, and the end-to-end matrix
  covers both.

**F-6. Code SHOULD work on both tiers, wherever the seam allows it.**

- Why: `internal/sync`'s `Exec` is that seam. Sync, exec, install and every assertion are written once
  against it. Only `transport.go` knows whether running a script means `docker exec` or ssh.
- Exception: the batch operations (build, up, down, logs) are deliberately not on the seam. Compose
  acts on a whole project at once, and a VM is one host process per router.

## Adding an assertion kind

`internal/check` is small on purpose. A kind is a line of syntax, one shell command or one HTTP request,
and a sentence saying what its failure means.

**A-1. A failing assertion MUST say what failed in the router's own words.**

- Why: an assertion reporting only "failed" sends the reader to `owlab logs`, and in CI the container
  is gone by then.
- Where: for this reason the detail carries the first line of what the router printed, and a failed
  router's log is dumped while it still exists.

**A-2. Every assertion kind MUST be tested in both directions.**

- Why: a check that fires on a healthy router is worse than no check. It teaches people to ignore the
  output.
- How: `internal/check` has an httptest server modelling LuCI's session cookie, so you can test an
  HTTP kind without Docker.

## Adding a distribution

**D-1. A new distribution MUST be one entry in the `distros` table in `internal/config/target.go`, and
nothing else.**

**D-2. The distribution's project MUST publish a rootfs tarball (or a container image) for a supported
target.**

- Why: a disk image cannot be turned into a container. This is the bar for inclusion.

**D-3. A distribution entry MUST set `ForcePackageManager` when the fork's package manager does not
follow from its version number.**

- Why: "25.12 and later means apk" is a fact about OpenWrt, not about the family. A fork can track
  25.12 and still build with opkg. Deriving the manager from the release number alone hands such a
  router apk commands, and every install fails.

## Things that need care

**C-1. Documentation MUST describe routers and packaging, not what packages are for.**

- Why: owlab starts test routers and runs assertions. It has no opinion about the software under test,
  and the documentation keeps it that way. Naming a third-party package reads as endorsement.
- Bad: naming the third-party package an issue was first observed with.
- Good: a generic package (`luci-app-example`, `example-daemon`) and the mechanism, such as "procd
  jails a service", "a glob installs in alphabetical order", "an engine drops outbound UDP/53". The
  symptom and the cause are what make an entry useful.

**C-2. A change to an English page MUST come with the same change to its Russian pair.**

- Why: a change to one is not finished until the other says the same thing.

**C-3. A change that would break a `version: 1` `owlab.yaml` MUST wait for a major release.**

- Why: `owlab.yaml` is the compatibility surface. Unknown keys are an error by design, so a renamed
  field breaks every existing config at load time.

**C-4. You MUST NOT change what a released asset is named without checking who reads it.**

- Why: `setup/install.sh` looks assets up by name, and a workflow already pinned to a tag cannot be
  fixed remotely.

**C-5. The two actions MUST keep sharing `setup/install.sh`.**

- Why: it holds the attestation check, and two copies of a security check are one copy and one
  liability.

**C-6. Every release MUST have a section in `CHANGELOG.md`.**

- Why: the changelog is a release gate. CI extracts the notes for the newest version from
  `CHANGELOG.md`. A version with no section fails the build, so no empty release page is published.

## Releasing

See [docs/releasing.md](docs/releasing.md).
