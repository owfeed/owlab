# What is built in owlab, and what is not

*owlab is the bottom of a three-tool stack —
[ECOSYSTEM.md](https://github.com/VizzleTF/owfeed/blob/main/docs/ECOSYSTEM.md) in
owfeed says where the boundaries run and why. This file says how much of owlab's
side of that exists, as of 2026-07-28.*

It lives here rather than in the shared document on purpose. The shared version
went stale twice, both times on an owlab fact, because nothing in owfeed's CI ever
touches this repository. Beside the code, a claim that stops being true is a line
somebody has to edit in the same pull request that made it untrue.

## Working, and verified rather than assumed

| | Evidence |
|---|---|
| Both tiers install identically | Container over `docker exec`, VM over ssh, one `pkgmgr` producing the shell for each |
| `fidelity: vm` | `internal/qemu` boots a real image under QEMU with mac80211_hwsim radios and an extroot disk; assertions run against it unchanged from the container tier |
| `owlab build` writes `dist/<arch>/` | The layout `owfeed` and a feed's ingest both read. Built luci-theme-footstrap through the SDK and released it from that tree without rearranging anything |
| Install from a signed feed by name | `owlab test --feed` on 25.12: the package installs out of a signed index and its LuCI page renders. With the key removed the router reports it as not existing at all |
| `owlab releases` without a project | Asking a download server what it publishes needs neither an `owlab.yaml` nor a container engine, and no longer demands either |

## Built but not yet exercised in anger

- **`{host}` in a `--feed` URL.** The substitution and the `extra_hosts` entry it
  resolves through are covered by tests, and the container tier's alias is
  Docker's own `host-gateway`. What has not happened is a CI run that serves a
  feed off the runner and installs from it — which is the whole reason the token
  exists. The first one will be in a feed's pull-request pipeline, after this
  ships in a release.

## Not built, and why

**owlab does not verify signatures, and will not.** It installs with
`--allow-untrusted` because trust is not what it is checking. That is the T0
column of the trust matrix and the reason owlab holds no key anywhere: the moment
it verifies one, the column collapses and every invariant written against it has
an exception. `owfeed smoke` is the tool that asserts about a channel.

**owlab does not know about feeds it publishes to.** It reads an `owfeed.lock`
lying beside it if there is one; it never writes one, and there is no `owfeed:`
section in `owlab.yaml`. A developer's machine ergonomics and a feed's release
policy are edited by different people with different amounts of review.

## Where to look

- [reference.md](reference.md) — every `owlab.yaml` key and every flag *([по-русски](reference_ru.md))*
- [runbook.md](runbook.md) — how to do a particular thing *([по-русски](runbook_ru.md))*
- [internals.md](internals.md) — why the defaults are what they are *([по-русски](internals_ru.md))*
- [ECOSYSTEM.md](https://github.com/VizzleTF/owfeed/blob/main/docs/ECOSYSTEM.md) — the boundary with owfeed
