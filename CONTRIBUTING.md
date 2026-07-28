# Contributing

## The short version

```sh
gofmt -l .
go vet ./...
go test ./...
```

Those three, plus a cross-compile for five platforms, a `dash -n` pass over the
shell that ships inside the image, and an end-to-end job that starts real
routers on three release/distro combinations, run in CI on every pull request.
If a change touches how a router is built or reached, the end-to-end job is the
one that will tell you.

Running the real thing locally is one command and needs only Docker:

```sh
go build -o /tmp/owlab ./cmd/owlab
mkdir -p /tmp/lab && cd /tmp/lab
/tmp/owlab test --release 25.12.5 \
  --assert 'http 200 /cgi-bin/luci/admin/status/overview' \
  --assert 'service uhttpd'
```

Documentation lives in [docs/](docs/) and exists in English and Russian. A
change to one is not finished until the other says the same thing.

## What this project is fussy about

**Comments explain why, not what.** The code says what. The reason a line
exists — usually a bug it prevents, often one that shipped somewhere — is the
part that cannot be recovered by reading it. Nearly every unusual line here has
one: `--platform` is passed explicitly because most OpenWrt architecture names
are not valid OCI platforms; procd runs as PID 1 rather than Docker's init
because everything reading board data over ubus comes back empty otherwise.
Those sentences are the value of the file.

**Nothing addresses a router by its bridge IP.** Published ports on localhost
are the only way in that works identically on native Linux, WSL2, Docker Desktop
for Mac and Windows, OrbStack and Colima. Code that reaches a container by its
subnet address works on the machine it was written on and nowhere else.

**Nothing in a fixture may touch `lan`.** That is eth0, with the container
engine's own address on it, and it is the only way in — there is no console to
recover from. Invented networks sit on dummy devices or on VLANs that carry no
traffic.

**A tier that cannot be delivered is refused, not silently downgraded.**
`fidelity: vm` on a machine with no QEMU is an error naming what is missing.
Quietly starting something less than what was asked for produces bug reports
nobody can reproduce.

**Both package managers, every time.** apk from 25.12, opkg on 24.10, and
neither understands the other's command. Any path that installs, queries or
removes a package needs both, which is why `internal/pkgmgr` builds the shell in
one place and why the end-to-end matrix covers both.

**Both tiers, wherever the seam allows it.** `internal/sync`'s `Exec` is that
seam: sync, exec, install and every assertion are written once against it, and
only `transport.go` knows whether running a script means `docker exec` or ssh.
The batch operations — build, up, down, logs — are deliberately not on it,
because compose acts on a whole project at once and a VM is one host process per
router.

## Adding an assertion kind

`internal/check` is small on purpose: a kind is a line of syntax, one shell
command or one HTTP request, and a sentence saying what its failure means.

Say what failed in the router's own words. An assertion reporting only "failed"
sends the reader to `owlab logs`, and in CI the container is gone by then —
which is why the detail carries the first line of what the router printed, and
why a failed router's log is dumped while it still exists.

Test both directions. A check that fires on a healthy router is worse than no
check: it teaches people to ignore the output. `internal/check` has an httptest
server modelling LuCI's session cookie, so an HTTP kind can be tested without
Docker.

## Adding a distribution

One entry in the `distros` table in `internal/config/target.go`, and nothing
else. The bar for inclusion is that the project publishes a rootfs **tarball**
(or a container image) for a supported target — a disk image cannot be turned
into a container.

Set `ForcePackageManager` when the fork's package manager does not follow from
its version number. "25.12 and later means apk" is a fact about OpenWrt, not
about the family: a fork can track 25.12 and still build with opkg, and deriving
the manager from the release number alone hands such a router apk commands and
fails every install.

## Things that need care

**`owlab.yaml` is the compatibility surface.** Unknown keys are an error by
design, so a renamed field breaks every existing config at load time. A change
that would break a `version: 1` file waits for a major release.

**Do not change what a released asset is named** without checking who reads it.
`setup/install.sh` looks assets up by name, and a workflow already pinned to a
tag cannot be fixed remotely.

**The two actions share `setup/install.sh` on purpose.** It holds the
attestation check, and two copies of a security check are one copy and one
liability.

**The changelog is a release gate.** CI extracts the notes for the newest
version from `CHANGELOG.md`, and a version with no section fails the build
rather than publishing an empty release page.

## Releasing

See [docs/releasing.md](docs/releasing.md).
