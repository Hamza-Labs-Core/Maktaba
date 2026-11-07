---
schema: 1
kind: pr
pr: 792
title: A request that waited for a slow key read uses it instead of reading again
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
issue: 681
---

## What happened
- `TestARequestWaitsForTheReadUnderWay` panicked on a slow runner. The JWKS cache stamped `reloadedAt` before reading the key file. A read slower than `reloadEvery` (100 ms) therefore looked stale to the request queued behind it, which read again.
- `reloadedAt` is now the read's end. A waiter uses any read that returned after it arrived.

## What we learned
- A "last done at" stamp taken at the start of slow work makes its waiters think it is stale. Stamp it when the work returns, and compare it with when the waiter arrived.
- Making the read slower than the window turned a rare CI panic into a test that fails every time without the fix. A test of "waits for the read under way" should make the read slow on purpose.
