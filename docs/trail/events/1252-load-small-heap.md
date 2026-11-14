---
schema: 1
kind: pr
pr: 1252
title: "load-small gates the relay's memory on its Go live heap"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1252 merged as 1f6f7302c. The relay serves `maktaba_go_heap_live_bytes` on `/metrics` (the
  live heap after the last collection, read without forcing one). load-small reads it before
  the homes connect and every 5 s under load; the figure is the median of each round, then of
  the rounds, minus the heap before the homes, over 1,000 tunnels, as the CPU figure is kept.
  The `docker stats` peak is still reported, not gated.
- `compare` gates the heap at 20 % when both sides have it, fails when the base has it and this
  commit doesn't (a relay can't switch the gate off by not reporting), and shows it ungated
  when only this commit has it (PRs whose base predates the merge); two old files compare peaks.
- Evidence from a temporary CI step (reverted before merge), one runner: the same code five
  times read 133,767 / 150,455 / 133,677 / 150,255 / 133,8xx B per tunnel (pairwise −11.2 % to
  +12.5 %); a test-only build holding 64 KB per tunnel read +40.8 % and failed the compare.

## What we learned
- The old gate divided one `docker stats` peak by 1,000 tunnels: main alone read 254k–327k B per
  tunnel across seven runs, and a main-vs-main run showed −17 %. It failed #1238 (+28 %) and
  #1251 (+24 %) with relay code identical to main's (#1238's whole relay-side cost was +68 KB of
  init allocations, about 70 B a tunnel; its earlier run read −13.4 %).
- Before calling a memory regression, diff the relay's packages against main and compare init
  allocations (`GODEBUG=inittrace=1`): if nothing per tunnel changed, the measurement is noise.

## Keep doing
- Prove a new gate both ways in CI before it gates: the spread of identical code, and a
  deliberate leak that must fail.

## Open
- The live heap sits at one of two levels (≈134k or ≈150k B per tunnel) between runs of the
  same code, likely a size class or map growth step; it uses 12.5 % of the 20 % margin. Worth a
  look if the gate ever flakes.
