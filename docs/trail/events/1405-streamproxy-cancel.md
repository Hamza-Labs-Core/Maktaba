---
schema: 1
kind: pr
pr: 1405
title: "StreamProxy: a cancelled request ends without waiting for its session"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1405 merged as 72bf104a1. `StreamProxyCopyTests aBodyCutShortIsAnErrorForThePlayer` failed
  on several Apple PRs with `proxy.serving` never reaching 0. The CI log showed the chunked
  request's URLSession never heard the home's reset; the proxy's 60 s `homePatience` did fail
  it and free the slot, but the test had stopped waiting at 35 s. Why URLSession missed the
  reset on the runner is still unknown (not reproduced locally under heavy load).
- A first rewrite replaced `NWConnection.forceCancel()` with a plain-socket `DroppingHome`
  (SO_LINGER 0) and still failed in CI. Auditing every slot path then found a real hold:
  `UpstreamBody.cancel()` left a waiting `response()` or `next()` to be resumed only by
  URLSession's completion callback and stopped the silence timer at once, so a late or lost
  callback kept the slot among the 16 for good. `cancel()` now resumes the waiters itself, as
  `fail()` did; `cancellingEndsTheWaitWithoutTheSession` hangs without it. The test's proxy now
  has 5 s of patience, assertions unchanged.

## What we learned
- When a test says "eventually released" fails, read how long the bound is before calling it a
  flake or a leak: here the bound worked and the test was shorter than it.
- Every continuation a cancel path leaves to a framework callback is a potential permanent
  hold: resume it on cancel too.

## Open
- Why URLSession misses a reset on the CI runner for a chunked response.
