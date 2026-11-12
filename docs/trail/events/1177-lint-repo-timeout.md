---
schema: 1
kind: pr
pr: 1177
title: "CI: lint / repo gets 20 minutes"
date: 2026-11-11
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1177 merged as b63f862b6. `lint / repo` had a 10-minute limit. Its routing tests ("which jobs
  a change runs") took about 8 minutes, and main's runs of the whole job took 6 to 10 minutes on
  2026-11-11. It was cancelled at the limit inside the trail step on #1175 and #1176, turning
  `ci-success` red with no real failure. The limit is now 20, as `lint / lint` already has, with
  the reason in a comment.

## What we learned
- A job close to its time limit fails on whichever step runs last, here the trail step, so the
  log points at the wrong step. Compare the job's total time with its limit before blaming the
  step it stopped in.

## Keep doing
- Read a cancelled job's step timings before re-running it.

## Stop doing
- Treating the step a job was cancelled in as the cause.

## Open
- The routing tests keep growing with every filter; if `lint / repo` nears 20 minutes, split or
  speed them up rather than raising the limit again.
