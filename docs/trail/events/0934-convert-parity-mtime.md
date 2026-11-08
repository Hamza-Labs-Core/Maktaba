---
schema: 1
kind: pr
pr: 934
title: The convert parity compared fingerprints that hash when each side ran
date: 2026-11-07
session: 848393e1-ea22-4a25-a90f-b930df232381
prs: [919]
---

## What happened
- Since probe runs on the engine (#919), a conversion's probe queues `thumb_hash` and `thumb_small`, and `clip_groups` and `promo_links` run again. Their input fingerprints hash the new MP4's mtime, which differs between the in-process and plugin sides of the convert parity test.
- CI passed it (both sides in the same second); every local run failed. `parity.toml` now masks those four `input_hash`es per stage, as it already masked `videos.mtime`.

## What we learned
- A stage that starts firing edges (a legacy stage turned engine stage) changes other plugins' parity worlds. Run every first-party parity test after a wave, not only the moved stage's.
- A parity that passes in CI and fails locally on a time field is a race on the clock, not a local quirk.

## Open
- Nothing.
