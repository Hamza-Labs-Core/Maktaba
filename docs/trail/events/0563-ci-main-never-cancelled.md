---
schema: 1
kind: pr
pr: 563
title: Never cancel main's running CI or rehearsal run
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 294aab707. #547's cancel-in-progress on main starved tagging: with a merge every few minutes and runs of about 1 h, every main CI and rehearsal run since bdad891d4 was cancelled, so the tag guard could tag nothing (AirPlay waited).
- Main pushes keep the single `ci-main` / `update-rehearsal-main` group, but cancel-in-progress is now PR-only (`github.event_name == 'pull_request'`). The running main run always finishes and GitHub keeps at most one pending run, a newer commit's replacing the older pending one. release/**, re-runs, nightly and manual runs keep their own never-cancelled group; the trail paths-ignore (#553) stays.
- test_ci_paths.py asserts main is not cancel-in-progress and PRs are.

## What we learned
- With one concurrency group, GitHub keeps one running and one pending run; cancel-in-progress on a busy branch means no run ever finishes.

## Keep doing
- Tag the newest main commit whose own CI and rehearsal finished green.
