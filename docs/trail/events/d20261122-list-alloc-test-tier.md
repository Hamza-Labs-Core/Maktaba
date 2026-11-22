---
schema: 1
kind: incident
title: A memory test at the unit tier's time cap (TestListAllocatesLessThanTheList)
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
prs: [1497, 1499]
---

## What happened
- #1497's `unit / unit` failed on a test-budget hard-cap breach in a package it does not
  touch: `api/internal/updates::TestListAllocatesLessThanTheList` took 1.24 s re-run alone,
  against the 1.2 s cap. On main the same test ran 0.93 s alone. It is at the cap and moves
  with the runner's load.
- The test decodes a 7 MiB release list, the size of the real one, to prove the catalog
  reads it one release at a time. Under `-race` that is about a second. What it proves is
  memory, not speed.
- #1499 skips it under `-short`, as `TestPinsForPlatform` in the same package does. It runs
  unchanged in the integration tier, which has no `-short`.
- The day's second test-budget breach on main's tests, after #1493.

## What we learned
- A test whose work is fixed by what it proves (a real-size input) belongs in the
  integration tier. The unit tier's per-test cap is for tests whose cost can shrink.

## Keep doing
- Read "re-run alone" in a breach. Over the cap, it is the test's cost, and it fails again.
