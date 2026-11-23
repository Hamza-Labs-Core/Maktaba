---
schema: 1
kind: incident
title: trail check took minutes because it ran a git show per event per snapshot
date: 2026-11-22
session: 34a3769e-2c76-42e5-aace-087f8271865a
agent: claude-code
---

## What happened
- `trail check` (agent-trail 0.2.0) took 1,205 s on the owner's Mac on main at c6628d311. On
  CI the trail step took 171 s, tag fetch included (run 45081849888). Each new snapshot and
  each new event made it slower. `trail check --no-git` took about 2.5 s.
- The cause was in agent-trail's `check_snapshot` → `load_events` → `files_at`. It ran one
  `git show` for every event file at every snapshot's commit: about 41,800 git processes for
  75 snapshots and 778 events. Each snapshot also ran a `git diff` for every merge with no
  event, plus a `git log` and two `git rev-parse`.
- agent-trail 0.2.1 (Hamza-Labs-Core/agent-trail PR 1) reads git in batches:
  - one `git cat-file --batch` per run serves every tree, blob and commit lookup;
  - each blob is read once and each event file is checked once per run;
  - one `git log` serves every snapshot's history;
  - one `git diff-tree --stdin` per snapshot covers the merges it hasn't seen yet.

  A full `trail check` on the same checkout now takes about 5 s on the Mac (three runs: 5.21,
  5.05 and 5.01 s). The estimate for CI is 5 to 10 s plus the tag fetch.
- The output was compared on the same Maktaba checkout. The good trail gave identical output
  from 0.2.0 and 0.2.1. A copy with 16 planted problems gave the same 16 messages, line for
  line, with exit 1 from both versions: 1,126 s for 0.2.0 and 4.9 s for 0.2.1.

## What we learned
- A check that reads history at every snapshot costs snapshots × files. Git objects never
  change, so one batch reader and a cache keyed by blob sha removes the repetition. Most
  events are the same blob at every snapshot.
- The slowdown stayed hidden on CI, where a git process costs far less than on a Mac. On the
  Mac it had reached 20 minutes.

## Keep doing
- Measure where a slow tool spends its time (`python3 -m cProfile -s tottime`) before
  changing it. The profile pointed at the per-file reads and the per-merge diffs.
- Prove a speed-up gives the same output: run the old and new versions on the same real
  checkout, on a good trail and on one with planted problems, and diff the outputs.

## Stop doing
- Spawning one git process per file per commit in a loop. Read in batches
  (`git cat-file --batch`, `git diff-tree --stdin`) instead.

## Open
- Tagging agent-trail v0.2.1 (which moves `@v0`) is left to the owner after the agent-trail
  PR merges. Until then, CI runs 0.2.0.
