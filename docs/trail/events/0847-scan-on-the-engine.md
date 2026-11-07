---
schema: 1
kind: pr
pr: 847
title: A folder scan is a queue stage like the others (Epic 54 wave 9, step 1, 54.36 §2)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [844]
---

## What happened
- `scan` became an engine stage. Its new videos are its changed subjects, and a declared edge (map `videos_changed`, new) queues their probes. The scanner no longer queues `probe` for a scan.
- A parity proof against the legacy handler, with explicit allowances: the probes' queue reason, and the scan job's output hash and summary.

## What we learned
- `videos_of` falls back to every unlinked video when a job names none. That's right for a YouTube listing and wrong for a scan, which needs a map that names only its own new videos.
- Measure each allowance before keeping it. The probes' priority and the scan job's outcome, version and propagation turned out equal on both paths, because the legacy bookkeeping writes them, so those allowances were dropped.
- An old-versus-new proof can use the same drain on both sides. The old graph is the new one with the stage set back to `legacy` and its hand-chained edge.

## Open
- `probe`, `categorize`, `thumbnail` and `extract`, then the move into `tv.maktaba.core-media`. The removal waits one release after the move's flip, and only if nothing was flipped back.
