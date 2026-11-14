---
schema: 1
kind: pr
pr: 1341
title: "load-small's live heap no longer counts goose's start-up buffers"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1341 merged as 763f75051. `releaseStartupMemory()` (cloud/cmd/maktaba-cloud) runs two
  collections right after the migrations, emptying the `sync.Pool` that held goose's SQL scanner
  buffers. `TestReleaseStartupMemoryEmptiesPools` fails with only one collection.
- Five runs of identical code now agree within 0.23% (B/tunnel 145,491–145,824; before the homes
  1.96 MB each); a 64 KB/tunnel leak reads +45.3% and fails. The 20% gate is unchanged.

## What we learned
- The two levels from #1252's Open item came from the relay that applied migrations holding
  ~16 MB of pooled 4 MB parser buffers. A pool frees only over two GCs, and an idle relay goes
  minutes without one, so the baseline read 0, 13.6 or 22 MB by GC timing. It failed #1266
  (+20.2%) with no relay change.
- Heap profiles from a temporary CI step found it; guessing at size classes would not have.
- The true level is ≈145.7k B/tunnel; PR bases from before the fix can still read low (+16%).

## Keep doing
- Prove a gate's spread on identical code and a planted leak before trusting it.

## Open
- Nothing.
