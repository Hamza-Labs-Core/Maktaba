---
schema: 1
kind: pr
pr: 539
title: Apple integration PR: faster Player UI tests and folded fixes
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [566, 567, 568, 575]
---

## What happened
- Merge 99e51114c, the single Apple integration PR. Player UI tests build once per device and run 6 duration-balanced shards per device (`tools/apple/ui-durations.json`), with the three text sizes checked in one launch through a Debug-only, flag-gated `TextSizeHook`.
- Shards boot while the app builds and wait until the simulator's own processes are quiet (`tools/apple/sim-settle.py`, at most 8 min), then fetch the build through the API (`actions: read` in `ci.yml`). The longest shard went from 53.6 to 29.8 min.
- Folded in: #566 (warm-up and Libraries audit), #567 (whole app log), #568 (Arabic names), #575 (close the app after each test), the RouteStub redirect race and the permissions-gate `@State` fix. Each has its own event.
- #566's rail swipes were removed: after #569 they did nothing, and a swipe back left a row under the iPad sidebar, failing contrast. testShimmer reads each text size only after the app took it.
- Process: small root-fix PRs (#566-#575) each paid the full Apple CI, and each merge replaced main's pending run, holding back the tag. Apple fixes now fold into one integration PR; fix agents push branches, not PRs.

## What we learned
- Waiting for the whole runner to go idle cost 12 min per shard; waiting only for the simulator's own processes is enough.

## Keep doing
- Fold small Apple fixes into one integration PR; fix agents push branches, not PRs.
