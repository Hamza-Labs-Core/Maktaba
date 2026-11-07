---
schema: 1
kind: pr
pr: 881
title: A flake in TestTheAgentCarriesTheDirectFrames (from #845)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [845, 861]
---

## What happened
- #861's integration job (128486295045) failed in `cloud/internal/cloudlink/direct_test.go` TestTheAgentCarriesTheDirectFrames, at line 151: `endpoints [] from invalid IP`.
- The test, added in #845, waited for the name and then read the fake cloud's endpoints once. The name's answer can arrive before the endpoints frame is handled, so the read raced.
- The test now waits for the endpoints too, read under the fake's lock. No production code changed.

## What we learned
- A test that waits for one asynchronous effect must not read a different one once. Each thing it asserts on needs its own wait, even when the frames are sent in order: here the agent sends the endpoints before the name, but the cloud side doesn't handle them in that order.
- The race didn't reproduce locally: 300 runs with `-race`, with and without `-cpu 1`, on the old code. A loaded CI runner shows orderings a laptop doesn't. Fix by reading the code, and don't take local passes as proof there is no race.

## Keep doing
- Check a flake's fix with `-race -count=50` or more, and say in the PR whether the old code failed locally.

## Open
- Nothing.
