---
schema: 1
kind: pr
pr: 1041
title: Rehearsal 16.9 takes wave 9's core stages through a rollback and an update
date: 2026-11-08
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
prs: [998, 1045, 1046]
---

## What happened
- Scenario 16.9 (story 54.36 T06, after #998 moved the core stages into `tv.maktaba.core-media`) queues `scan`, `probe`, `link` and `fp_gate`: an update mid-scan. On the previous release all 15 queued jobs ran in-process through the rollback (41.7 s down, budget 4 min). Through the update, 10 ran in-process on the previous release and 5 in the light runner on this commit (11.3 s down, budget 2 min). After the flip every job ran in the runner. Every 16.x row passed in the same run.
- Core stages are not switchable, and nothing needed it: the flip at update is the graph sync (`engine/sync.py`), which does not read `switchable`. Only the admin route and the CLI refuse them.
- Before queueing, a scenario now waits out every open job of its stages, not only the catalog's: an enqueue on a subject with an open job joins it, and a home queues scans of its libraries itself.
- `fp_gate` claims seasons, and the small seed has none, so in that first green run it never queued: 16.9 passed covering three of its four stages. The warm-up also waited for every listed stage, so it ran its full four minutes (16 rounds).
- #1045: the warm-up waits only for the stages that got a job (one round after it).
- #1046: 16.9's setup gives the seed a series with two seasons, written as the detect stage writes them; a detect run only retires such rows, and the queue takes a retired season's job. A wave marked `must` fails when a round queued no job of one of its stages. In its run all four stages ran in every round: 17 jobs in-process through the rollback (42 s down), 9 in-process and 8 in the runner through the update (11.3 s down), and after the flip 8 of 8 in the runner, two of each stage.

## What we learned
- A scenario that skips a stage the seed has no subject for passes without it. Make the coverage an assertion (16.9's `must`), and give the seed the subjects in the scenario's setup rather than changing the shared seed every row uses.
- A runner that exits after a minute idle (#998) does not break a scenario's jobs after the flip: a job that finds it gone waits for it to start (#953) and still runs there.
