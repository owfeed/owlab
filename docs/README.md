# owlab documentation

The [README](../README.md) tells you what to type. These pages say how the
thing works and why it was built this way.

- [00 — Overview](00-overview.md) — every config key, in full
- [01 — Architecture](01-architecture.md) — the packages, and how a command flows through them
- [02 — Tiers](02-tiers.md) — `basic` and `vm`, and the third tier that was removed
- [03 — Networking](03-networking.md) — br-lan, ports, DNS, what the engine will not forward
- [04 — Packages](04-packages.md) — the stock set, feed pinning, the two package managers
- [05 — The VM tier](05-vm-tier.md) — QEMU, accelerators, disk images, extroot, radios
- [06 — Findings](06-findings.md) — every measured surprise, with its symptom and its cause

## How to read these

Nearly everything here is written the same way: a symptom that was observed, a
cause that was measured, and the decision that followed. Where a page says a
thing does not work, it was tried. Where it names a number, that number came
off a running router.

[06 — Findings](06-findings.md) is the index of those, and probably the most
useful page if something is behaving strangely: most of what has gone wrong
here presents as something other than its cause.
