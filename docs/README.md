# owlab documentation

The [README](../README.md) is the guide: what owlab is, how to write an
`owlab.yaml`, what every key does. These pages are for the questions it does
not answer — how the thing is built, and why it is built that way.

| | |
|---|---|
| [01 — Architecture](01-architecture.md) | What the pieces are and how a command flows through them |
| [02 — Tiers](02-tiers.md) | `basic` and `vm`: what each can do, and the third tier that was removed |
| [03 — Networking](03-networking.md) | br-lan, published ports, DNS, and what the engine will not forward |
| [04 — Packages](04-packages.md) | The stock set, feed pinning, out-of-feed packages, the two package managers |
| [05 — The VM tier](05-vm-tier.md) | QEMU, accelerators, disk images, extroot, real radios |
| [06 — Findings](06-findings.md) | Every measured surprise, with its symptom and its cause |

## How to read these

Nearly everything here is written the same way: a symptom that was observed, a
cause that was measured, and the decision that followed. Where a page says a
thing does not work, it was tried. Where it names a number, that number came
off a running router.

[06 — Findings](06-findings.md) is the index of those, and probably the most
useful page if something is behaving strangely: most of what has gone wrong
here presents as something other than its cause.
