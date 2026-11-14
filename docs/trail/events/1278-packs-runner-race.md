---
schema: 1
kind: pr
pr: 1278
title: "TestRunnerRules waits for the pack's try before waitIdle"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1278 merged as e8b831b2b. `TestRunnerRules` (`cmd/maktaba-server/internal/packs`) failed
  with "0 tries" in #1242's integration job, which doesn't touch packs. The test now waits (10 s
  at most) for the first checksums request with a new fixture helper, `waitReqs`, before
  `waitIdle`.

## What we learned
- `Runner.Desired` starts a missing pack's install on its own goroutine. `waitIdle` sleeps 20 ms
  and then polls `Busy`; on a loaded runner the goroutine hadn't marked the pack busy yet, so it
  returned at once and the test counted before the first try.
- "Wait until not busy" is only safe once something has been seen busy, or seen done. Wait for
  the effect itself (a request, a row) instead of a fixed head start.

## Keep doing
- A flake in code the PR doesn't touch gets its own small fix PR off main; don't just re-run it.

## Open
- `waitIdle`'s other callers in packs use the same 20 ms head start; they haven't failed yet.
