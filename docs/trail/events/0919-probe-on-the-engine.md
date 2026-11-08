---
schema: 1
kind: pr
pr: 919
title: Reading a video's length and audio tracks is a queue stage like the others (Epic 54 wave 9, step 2, 54.36 §2)
date: 2026-11-07
session: 848393e1-ea22-4a25-a90f-b930df232381
prs: [847]
---

## What happened
- `probe` became an engine stage. It saves what it reads as before, and the queue decides what runs next from the length and frame size it reports.
- A parity proof against the legacy handler on two copies of one drained library.
- Slot 0211 seeds `probe` as an engine stage (`job_stages.legacy` false), SchemaRev 211.

## What we learned
- The wave 9 PRs are a stack of seed migrations, one slot each, and main can take a slot between them (0209 went to plugins' `data_version`). Renumber the whole stack against main's newest migration right before each merge, never earlier.

## Open
- `thumbnail` and `extract` (stacked on this one), then the move into `tv.maktaba.core-media`. `categorize` stays declared, pending an owner decision.
