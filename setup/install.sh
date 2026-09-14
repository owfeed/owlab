#!/usr/bin/env bash
# Download an owlab release, verify it, and put the binary on PATH.
#
# Shared by both actions in this repository rather than copied into each: the
# verification below is the part that must not drift, and two copies of a
# security check are one copy and one liability.
#
# Inputs, all from the environment:
#   OWLAB_VERSION     a tag such as v0.2.0, "latest", or empty for OWLAB_ACTION_REF
#   OWLAB_ACTION_REF  the ref the calling action was pinned at (github.action_ref)
#   OWLAB_VERIFY   "true" to check the build attestation before running anything
#   GH_TOKEN       token for the release download and the attestation API
set -euo pipefail

# owlab's own CI tests the action against the working tree rather than against a
# published release, which is the only way a change to the action can be tested
# before it ships. Nothing else should set this.
if [ -n "${OWLAB_SKIP_INSTALL:-}" ] && command -v owlab >/dev/null 2>&1; then
  echo "using the owlab already on PATH: $(command -v owlab)"
  owlab version
  exit 0
fi

# Windows is deliberately not here, and not because the binary is missing —
# releases carry windows/amd64 and windows/arm64, and owlab runs on a
# developer's Windows machine. A GitHub Windows runner is the one place it
# cannot: Docker Desktop is not installed there and cannot be, so a runner has
# no engine that speaks Linux images and every owlab command that needs a
# router would fail after this step rather than during it. Say so here, where
# the cause is still visible.
case "$RUNNER_OS" in
  Linux)  os=linux ;;
  macOS)  os=darwin ;;
  Windows)
    echo "::error::owlab needs a container engine that runs Linux images, and GitHub's Windows runners have none. Use a Linux runner."
    exit 1 ;;
  *) echo "::error::owlab has no release build for $RUNNER_OS"; exit 1 ;;
esac
case "$RUNNER_ARCH" in
  X64)   arch=amd64 ;;
  ARM64) arch=arm64 ;;
  *) echo "::error::owlab has no release build for $RUNNER_ARCH"; exit 1 ;;
esac

dir="${RUNNER_TEMP}/owlab"
mkdir -p "$dir"

# No version asked for: install the release the action itself is pinned at, so
# `setup@v0.6.4` means owlab 0.6.4 the way it reads. The default used to be
# "latest", which made every `uses: ...@vX.Y.Z` without a `version:` float --
# and the ref is the only pin dependabot moves.
#
# OWLAB_ACTION_REF comes from `${{ github.action_ref }}` in action.yml, not from
# the GITHUB_ACTION_REF environment variable: measured on 2026-09-14, inside a
# composite action's run step the variable is empty while the expression holds
# the ref the action was called at (a branch or a tag, as written in `uses:`).
# Only a release-shaped tag is taken. A branch, a SHA or a local `./action` has
# no release of that name, so those keep the old answer, with the warning below.
if [ -z "${OWLAB_VERSION:-}" ]; then
  case "${OWLAB_ACTION_REF:-}" in
    v[0-9]*.[0-9]*.[0-9]*)
      OWLAB_VERSION="$OWLAB_ACTION_REF"
      echo "installing owlab $OWLAB_VERSION, the release this action is pinned at"
      ;;
    *)
      OWLAB_VERSION=latest
      echo "::warning::this action was called at \"${OWLAB_ACTION_REF:-a local path}\", which is not a release tag; installing the latest release"
      ;;
  esac
fi

# gh_outage <gh output>
#
# gh exits 1 for every failure, so the text decides whether GitHub was failing or
# answering. Measured with gh 2.99.0: a 5xx prints `HTTP 503: <message> (<url>)` or
# `(HTTP 503)`, an unreachable host `error connecting to <host> check your internet
# connection`, an unreachable proxy `proxyconnect tcp: ... connection refused`, and
# `gh attestation verify` with no Sigstore trust root reachable `error creating
# Sigstore verifier: no valid Sigstore verifiers could be initialized`. An answer
# prints `release not found`, `no assets match the file pattern` or `HTTP 404`.
gh_outage() {
  grep -Eqi 'HTTP (408|429|5[0-9][0-9])|status code: (408|429|5[0-9][0-9])|error connecting to|connection (refused|reset)|i/o timeout|TLS handshake timeout|timeout awaiting|no such host|unexpected EOF|server misbehaving|proxyconnect|rate limit|error creating Sigstore verifier' <<<"$1"
}

# gh_read <gh arguments>
#
# A read from GitHub, asked again while GitHub is failing: 4 attempts, 5, 10 and 20 s
# apart. Exit 8 when the outage outlasts them -- owfeed's code for "upstream outage,
# safe to rerun" -- and 7 at once for an answer. Before this a single 502 from the
# release API failed the step with gh's exit 1, the same as a tag that does not exist.
gh_read() {
  local attempt=1 delay=5 err
  err="$(mktemp)"
  while :; do
    if gh "$@" 2>"$err"; then
      rm -f "$err"
      return 0
    fi
    if ! gh_outage "$(cat "$err")"; then
      cat "$err" >&2
      rm -f "$err"
      return 7
    fi
    if [ "$attempt" -ge 4 ]; then
      cat "$err" >&2
      rm -f "$err"
      echo "::error title=Upstream outage (safe to rerun)::gh $1 $2 could not reach GitHub after $attempt attempts. Nothing was installed and nothing failed verification; rerun the job."
      return 8
    fi
    echo "gh $1 $2: GitHub did not answer (attempt $attempt of 4), retrying in ${delay}s: $(tr '\n' ' ' <"$err")" >&2
    sleep "$delay"
    attempt=$((attempt + 1))
    delay=$((delay * 2))
  done
}

# The version is needed before the download, not after: the asset name carries
# it, so "latest" has to be resolved to a tag rather than guessed at.
if [ "$OWLAB_VERSION" = "latest" ]; then
  echo "::warning::owlab pinned to \"latest\"; pin a tag so a CI result cannot change without a commit"
  tag="$(gh_read release view --repo owfeed/owlab --json tagName --jq .tagName)"
else
  tag="$OWLAB_VERSION"
fi
ver="${tag#v}"
asset="owlab_${ver}_${os}_${arch}.tar.gz"

# Already installed at this exact version, in this job. Using the action twice in
# one job is an ordinary thing to want -- 25.12 takes an apk and 24.10 takes an
# ipk, so proving a package works on both releases is two steps -- and without
# this the second one dies in `gh release download`, which refuses to overwrite a
# file it downloaded a minute earlier.
#
# Matched on the version rather than on the file's existence: two steps asking for
# different versions have to get different binaries, and silently reusing the
# first would make the second step's `version:` a lie.
if [ -x "$dir/owlab" ] && [ "$("$dir/owlab" version 2>/dev/null | head -1 | awk '{print $2}')" = "$ver" ]; then
  echo "owlab $ver is already installed in this job"
  echo "$dir" >> "$GITHUB_PATH"
  export PATH="$dir:$PATH"
  exit 0
fi

# --clobber, because a job may have downloaded a DIFFERENT version into the same
# directory: the short-circuit above returns only on an exact match, so reaching
# here with the file present means it is the wrong one and has to be replaced.
gh_read release download "$tag" --repo owfeed/owlab --pattern "$asset" --dir "$dir" --clobber

# Verify BEFORE the archive is unpacked or anything in it is executed. A check
# that runs after the thing it checks is not a check.
if [ "${OWLAB_VERIFY:-true}" = "true" ]; then
  # --signer-workflow, not only --repo. Checking the repository alone accepts an
  # attestation produced by ANY workflow in it holding attestations: write, so a
  # single merged pull request adding a workflow would be enough to mint a valid
  # attestation for arbitrary bytes.
  #
  # gh writes its result to stderr, which a composite step swallows, so it is
  # captured and echoed either way: a check nobody can see ran is one nobody
  # believes ran.
  #
  # Asked again when GitHub's attestation API or Sigstore is what failed: reporting
  # that as "does not verify" told people the binary was suspect when the check had
  # never run, and pushed them towards verify: false.
  attempt=1
  delay=5
  while :; do
    if out=$(gh attestation verify "$dir/$asset" \
        --repo owfeed/owlab \
        --signer-workflow owfeed/owlab/.github/workflows/release.yml 2>&1); then
      echo "$out"
      echo "verified $asset as built by owfeed/owlab .github/workflows/release.yml"
      break
    fi
    echo "$out"
    if gh_outage "$out"; then
      if [ "$attempt" -lt 4 ]; then
        echo "the attestation check could not reach GitHub or Sigstore (attempt $attempt of 4); retrying in ${delay}s"
        sleep "$delay"
        attempt=$((attempt + 1))
        delay=$((delay * 2))
        continue
      fi
      echo "::error title=Upstream outage (safe to rerun)::the attestation check could not reach GitHub or Sigstore after $attempt attempts. The binary was not found to be wrong; it was not checked. Rerun the job."
      rm -f "$dir/$asset"
      exit 8
    fi
    echo "::error::$asset does not verify as built by owfeed/owlab's release workflow — refusing to install it"
    echo "::error::releases before v0.2.0 carry no attestation; pin a later tag, or set verify: false to accept an unverified download"
    rm -f "$dir/$asset"
    exit 1
  done
else
  echo "::warning::owlab installed without verifying its build attestation"
fi

tar -C "$dir" -xzf "$dir/$asset"
bin="$dir/owlab_${ver}_${os}_${arch}/owlab"
[ -x "$bin" ] || { echo "::error::$asset does not hold owlab where the release layout says it does"; exit 1; }
mv "$bin" "$dir/owlab"
echo "$dir" >> "$GITHUB_PATH"
export PATH="$dir:$PATH"

if [ -n "${GITHUB_OUTPUT:-}" ]; then
  echo "version=${ver}" >> "$GITHUB_OUTPUT"
fi
"$dir/owlab" version
