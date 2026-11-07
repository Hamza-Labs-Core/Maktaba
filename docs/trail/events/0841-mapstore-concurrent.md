---
schema: 1
kind: pr
pr: 841
title: The Android tests' in-memory store is concurrent
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- A `CloudTest` failed on #824's Windows desktop-player job with a `ConcurrentModificationException`. Its `AppSession`, on an unconfined scope, writes `MapStore` from HTTP threads while the test iterates it.
- `MapStore` is now a `ConcurrentHashMap`.

## What we learned
- A test fixture that a session under test writes from its own threads must be safe to read while it is written. `Dispatchers.Unconfined` resumes on whatever thread finishes the call.

## Open
- Nothing.
