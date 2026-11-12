# Security

How to report a vulnerability in owlab, and what its routers and releases protect. For anyone who
runs owlab on a laptop or in CI.

- **Report privately.** Open a [private advisory](https://github.com/owfeed/owlab/security/advisories/new).
- **The routers are not hardened.** They are throwaway boxes for localhost. Never put one on a network
  you care about.
- **Releases are attested.** `owfeed/owlab/setup` and `owfeed/owlab/action` verify the attestation
  before they unpack or run anything.

| Looking for | Section |
|---|---|
| How to report a problem | [Reporting](#reporting) |
| The unsafe defaults and why they exist | [Router defaults](#router-defaults) |
| Running owlab on pull requests | [In CI](#in-ci) |
| How a release binary is checked | [Supply chain](#supply-chain) |
| Checking a download yourself | [Verifying a download by hand](#verifying-a-download-by-hand) |
| What the GHCR images carry | [Published images](#published-images) |

## Reporting

Open a [private advisory](https://github.com/owfeed/owlab/security/advisories/new).

Do not open a public issue for anything that would let someone reach a developer's machine through a
router owlab started.

## Router defaults

The routers ship what a stock OpenWrt rootfs ships, plus whatever the config asked for. They are not
meant to be hardened.

Each default below is a deliberate choice for a throwaway box on localhost. Anywhere else it is a bad
one.

| Default | Reason | What it means |
|---|---|---|
| root has no password | The upstream rootfs ships that way, and LuCI accepts the empty field. | Set `OWLAB_ROOT_PASSWORD` before `owlab up` if you need a real one. ssh key auth is unaffected either way. |
| The ssh host keys are baked into the image | They only stop ssh from prompting. | Everyone using a given image has the same keys. They authenticate nothing. |
| `extra_packages:` are installed without signature verification | Projects publishing to GitHub Releases usually sign with usign and ship a `.sig` beside the artifact. Checking it needs their public key, and trusting that key is a decision a dev container should not make on anyone's behalf. | Treat anything installed this way as trusted-by-URL. |
| Containers get NET_ADMIN and NET_RAW, not `--privileged` | netifd has to bring interfaces up, and fw4 has to load an nftables ruleset. | Neither needs the rest of what privileged grants. |
| A `fidelity: vm` router is a QEMU process on your machine | It runs a real OpenWrt kernel. | The isolation is the emulator's, not the container engine's. Everything in this table about passwords and host keys applies to it too. |

A `fidelity: vm` router is reached over ssh on a forwarded port, with a key owlab generated.

### Rules that follow from the defaults

- Never put a router owlab started on a network you care about. Reaching it only through published
  ports on localhost does not replace that rule.
- For `extra_packages:`, pin an exact release, not a `latest` link.
- Do not copy the `extra_packages:` pattern into anything that ships.

## In CI

`owlab test` runs untrusted-ish content by construction. The package under test is code from the pull
request, running as root inside a container on the runner.

- The threat model is the same as any build step in the same job, with a container boundary added.
- A container boundary is not a security boundary against a determined attacker.
- If a workflow runs owlab on pull requests from forks, treat it like any other build step there: put
  no secrets in that job.

## Supply chain

- Release binaries carry [build provenance
  attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations).
- `owfeed/owlab/setup` and `owfeed/owlab/action` verify one against this repository's release workflow
  **before the archive is unpacked or anything in it is executed**. Otherwise they refuse to install it.
- Releases before v0.2.0 carry no attestation.
- The check passes `--signer-workflow`, not only `--repo`.

<details>
<summary>Why the repository alone is not enough</summary>

Checking the repository alone would accept an attestation produced by any workflow in it holding
`attestations: write`. A single merged pull request adding a workflow would then be enough to mint a
valid attestation for arbitrary bytes.

</details>

### Pin the action to a tag

The tag decides the binary too: `setup@v0.7.1` with no `version:` installs v0.7.1.

With `latest`, a branch or a SHA, the tool a CI result depends on can change between runs without a
commit anywhere. The installer warns when you use one.

## Verifying a download by hand

```sh
gh release download v0.7.1 -R owfeed/owlab -p 'owlab_0.7.1_linux_amd64.tar.gz'
gh attestation verify owlab_0.7.1_linux_amd64.tar.gz -R owfeed/owlab \
  --signer-workflow owfeed/owlab/.github/workflows/release.yml
```

A `SHA256SUMS` file is published too. On its own it is not a check: the same host serves it, from the
same release, as the binary it describes. Whoever can replace one can replace the other.

## Published images

`ghcr.io/owfeed/owlab-rootfs` images are development images and inherit everything above.

- This repository's `images` workflow builds them from upstream rootfs tarballs.
- Each image carries its own provenance at `/usr/share/owlab/compliance/`:
  - the package manifest;
  - a CycloneDX SBOM with per-package licences;
  - the buildinfo files, which pin the tree and every feed to an exact commit.
