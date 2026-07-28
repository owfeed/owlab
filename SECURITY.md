# Security

## Reporting

Open a [private advisory](https://github.com/VizzleTF/owlab/security/advisories/new).
Please do not open a public issue for anything that would let someone reach a
developer's machine through a router owlab started.

## What owlab is, in security terms

**These routers are not hardened and are not meant to be.** They ship what a
stock OpenWrt rootfs ships, plus whatever the config asked for. Three of the
defaults are worth stating plainly, because each is a deliberate choice for a
throwaway box on localhost and a bad one anywhere else.

**root has no password by default.** That is what the upstream rootfs ships
with, and LuCI accepts the empty field. Set `OWLAB_ROOT_PASSWORD` before
`owlab up` if you need a real one. ssh key auth is unaffected either way.

**The ssh host keys are baked into the image**, so everyone using a given image
has the same ones. They authenticate nothing; they only stop ssh from prompting.
A router owlab started must not be put on a network you care about, and nothing
about the way it is reached — published ports on localhost — is a substitute for
that.

**`extra_packages:` are installed without signature verification.** Projects
publishing to GitHub Releases usually sign with usign and ship a `.sig` beside
the artifact, but checking it needs their public key, and that is a trust
decision a dev container should not make on anyone's behalf. Pin an exact
release rather than a `latest` link, treat anything installed this way as
trusted-by-URL, and do not copy the pattern into anything that ships.

**Containers get NET_ADMIN and NET_RAW, not `--privileged`.** netifd has to
bring interfaces up and fw4 has to load an nftables ruleset; neither needs the
rest of what privileged grants.

**A `fidelity: vm` router is a QEMU process on your machine**, reached over ssh
on a forwarded port with a key owlab generated. It runs a real OpenWrt kernel,
so the isolation is the emulator's rather than the container engine's, and
everything above about passwords and host keys applies to it too.

## In CI

`owlab test` runs untrusted-ish content by construction: the package under test
is code from the pull request, running as root inside a container on the runner.
That is the same threat model as any build step in the same job, with a
container boundary added — and a container boundary is not a security boundary
against a determined attacker. A workflow that runs owlab on pull requests from
forks should take the same care it would with any other build step there: no
secrets in that job.

## Supply chain

Release binaries carry [build provenance
attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations).
`VizzleTF/owlab/setup` and `VizzleTF/owlab/action` verify one against this
repository's release workflow **before the archive is unpacked or anything in it
is executed**, and refuse to install it otherwise. Releases before v0.2.0 carry
no attestation.

The check passes `--signer-workflow`, not only `--repo`. Checking the repository
alone would accept an attestation produced by any workflow in it holding
`attestations: write`, so a single merged pull request adding a workflow would
be enough to mint a valid attestation for arbitrary bytes.

Pin the action to a tag. `latest` means the tool a CI result depends on can
change between runs without a commit anywhere; the installer warns when it is
used.

## Verifying a download by hand

```sh
gh release download v0.2.0 -R VizzleTF/owlab -p 'owlab_0.2.0_linux_amd64.tar.gz'
gh attestation verify owlab_0.2.0_linux_amd64.tar.gz -R VizzleTF/owlab \
  --signer-workflow VizzleTF/owlab/.github/workflows/release.yml
```

A `SHA256SUMS` file is published too, and on its own it is not a check: it is
served by the same host, from the same release, as the binary it describes.
Whoever can replace one can replace the other.

## Published images

`ghcr.io/vizzletf/owlab-rootfs` images are built by this repository's `images`
workflow from upstream rootfs tarballs, and each carries its own provenance at
`/usr/share/owlab/compliance/`: the package manifest, a CycloneDX SBOM with
per-package licences, and the buildinfo files pinning the tree and every feed to
an exact commit. They are development images and inherit everything above.
