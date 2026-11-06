---
schema: 1
kind: pr
pr: 547
title: CI concurrency: one group for main pushes
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Main pushes ran a full CI and rehearsal per sha, never cancelled, so about 12 merges an hour queued 12 full runs and the newest (tagged) commit waited hours.
- #547 (bdad891d4): `ci.yml` and `update-rehearsal.yml` put main pushes in one group (`ci-main` / `update-rehearsal-main`) with cancel-in-progress. PRs stay per-PR; `release/**`, re-runs (`run_attempt > 1`), nightly and manual rehearsals keep per-sha groups. `coverage-map.yml` skips a cancelled CI run in its own group. The tag guard checks only the tagged commit's own runs, so tag main's tip. Tests in `tools/ci/tests/test_ci_paths.py`; runbook in `deploy/packaging/README.md` "Release flow".
- Cancelled 18 stale main runs, kept 41500155980 on bdad891d4 (contains #512 AirPlay).

## What we learned
- cancel-in-progress on main starved tagging when merges came faster than runs finished; #563 made it PR-only.
