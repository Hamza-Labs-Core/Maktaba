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
- Slot 0210 seeds `scan` as an engine stage (`job_stages.legacy` false), SchemaRev 210. The full pipeline suite on a fresh database failed `test_seed_matches_graph` without it. CI's unit job runs only the affected tests and had passed.

## What we learned
- `videos_of` falls back to every unlinked video when a job names none. That's right for a YouTube listing and wrong for a scan, which needs a map that names only its own new videos.
- Measure each allowance before keeping it. The probes' priority and the scan job's outcome, version and propagation turned out equal on both paths, because the legacy bookkeeping writes them, so those allowances were dropped.
- An old-versus-new proof can use the same drain on both sides. The old graph is the new one with the stage set back to `legacy` and its hand-chained edge.

- Wave 9 is the first wave to take a stage off `legacy`. Each conversion needs a seed migration, as slot 0100 did for transcribe. The graph sync rewrites the row only when a worker starts, and the API's queue list reads the column before then.
- Run the full pipeline suite on a fresh Postgres before opening a wave PR. A reused cluster keeps roles from earlier runs and hides test-order bugs (#902).

## Open
- `probe`, `categorize`, `thumbnail` and `extract`, then the move into `tv.maktaba.core-media`. The removal waits one release after the move's flip, and only if nothing was flipped back.
