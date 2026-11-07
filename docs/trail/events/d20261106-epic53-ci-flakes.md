---
schema: 1
kind: incident
title: Epic 53's merge series — the CI failures that were not the change
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- Merging #673 to #691 in order, several CI failures had nothing to do with the code. Each was re-run once and passed, or a new push gave it a fresh run:
  - the Apple Player iPad and iPhone UI shards, each time on a different test, failed with a dropped simulator tap (flagged by the harness as #639), "Lost connection to the application", an accessibility audit that did not finish in time, and wait timeouts;
  - `desktop-shell-linux` timed out installing WebKitGTK;
  - the rehearsal's kit job got HTTP 500 downloading a release asset;
  - a darwin build upload got ENOTFOUND;
  - Epic 52's `Review.test.tsx` failed once on timing.
- #686's iPad shard failed twice, on different tests; a second re-run was made on that evidence and passed.

## What we learned
- Whether a UI failure belongs to a change is told by whether the same code passes the shard elsewhere (here, every stacked branch above it passed all six iPad shards), and by whether the failing test moves from run to run.

## Keep doing
- Read the failing job's log before re-running. In this series, as many failures were real (mypy annotations, a test-time cap, CodeQL, a Windows read race, a port clash) as were flakes.
