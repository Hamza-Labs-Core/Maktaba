---
schema: 1
kind: pr
pr: 1338
title: "The broadcast rehearsal and fan-out load test in CI (story 50.29)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1338 merged as b9a2fec95, last of the Epic 50 broadcast stack (50.21–50.28 were already on
  main). In docker compose, CI runs the real edge Worker in Miniflare as five simulated
  locations, a home running the real `streaming/internal/broadcast` broadcaster on an ffmpeg
  test pattern, and a Go load generator of HLS players. Scenario jobs on the PR all passed:
  `broadcast-fanout` (1 → 10 → 100 viewers, 1,000 nightly), `broadcast-fanout-planted` (a
  planted fault the check must catch), `broadcast-home`, `broadcast-kill`, `broadcast-plans`,
  `broadcast-edge-deploy`. Upload stays one copy per segment however many watch.
- No migrations of its own: 0227–0229 came from the stack.

## What we learned
- A rehearsal branch that stacks every story should be rebuilt as one integration of the
  merged-up stack plus its own files, so its diff shrinks to its own 26 files once the stack
  lands, and its migrations follow the stack's numbering.
- Since #1257 the edge refuses an ingest token whose `pub` is not its `home`; the rehearsal had
  minted `pub-rehearsal` for `home-rehearsal` and would have failed at the first upload. Any
  test fixture that mints tokens follows the publisher-is-the-home rule.
- Merging the stack took many rounds because each sibling merge re-conflicted the rest
  (CHANGELOGs, string tables, the edge Worker, test mocks). Merge each stack PR as soon as it is
  green, run every gate again on each new head, and keep one watcher per PR.

## Open
- Make `broadcast-rehearsal` a required check and give the nightly run a larger runner (owner).
- Follow-ups: a release tag gate on the nightly-green sha; the six unbuilt rehearsal scenarios;
  the spec's file table in English and Arabic.
