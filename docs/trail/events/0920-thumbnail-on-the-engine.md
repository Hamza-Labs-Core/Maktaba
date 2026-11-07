---
schema: 1
kind: pr
pr: 920
title: Making a video's poster and sprite is a queue stage like the others (Epic 54 wave 9, step 4, 54.36 §2)
date: 2026-11-07
session: 848393e1-ea22-4a25-a90f-b930df232381
prs: [847, 919]
---

## What happened
- `thumbnail` became an engine stage, on the home only, as before. Its output hash is the images' content-addressed paths, where the legacy bookkeeping hashed the time, so a run that makes the same images counts as unchanged.
- A parity proof: videos, the queue and the job log identical, and the two allowances (output hash, summary) each checked.
- Slot 0212 seeds `thumbnail` as an engine stage, SchemaRev 212.

## What we learned
- The parity test reused the name `old` for a list and then a row. mypy strict caught it only in the full run, because the commit had gone out without one. Run mypy over the whole pipeline before pushing a wave branch.

## Open
- `extract` (stacked on this one), then the move.
