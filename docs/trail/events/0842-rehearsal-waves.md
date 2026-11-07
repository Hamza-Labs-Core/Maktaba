---
schema: 1
kind: pr
pr: 842
title: The update rehearsal runs Epic 54's waves across an update (scenarios 16.1-16.3)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [833]
---

## What happened
- Scenarios 16.x run each release's real pipeline wheel, without the ML extras, one environment per release (`tools/rehearsal/venvs.sh`).
  - The home's Python is a script that runs the environment of the release whose server started it.
  - The wave stages' work is an 8 s sleep (`tools/rehearsal/sleepy`, a `.pth` hook on `registry.get_impl` and the runner's `load_impl`). The claims, the drain, the flip and the runners are the releases' own.
- Each wave queues jobs on the previous release, takes a failing update (a rollback), then a passing one, then queues jobs after the flip. Every queued job must end done, once, with no attempt spent, and must run where its release runs the stage.
- CI builds the environments once in the kit job and hands them to the 16.x jobs as an artifact. The previous release's is cached by its tag, saved by main's run only. Rows 16.1–16.3 are in the matrix.

## What we learned
- The first local run found that every first-party runner had crashed at start since 0.3.93 (#833).
  - The worker's fallback hid it: jobs ended done, in-process, and `job_stages.impl` read `plugin`.
  - Only a check of where each job actually ran showed it.
- Behind that, on macOS, a runner whose socket falls back to `/tmp` (a long data path) cannot bind it: Seatbelt sees `/private/tmp`.
- A rehearsal home's data path is long, so the fallback is the rule there. On a real home it is the exception.
- The jobs in flight at a switch finished on the old release before it stopped (sleep 8 s, drain 15 s). None started twice in either direction.
- A wave's own runs enqueue more jobs of the wave (`promo_links` enqueues `clip_groups`), so the scenario judges the jobs it queued by id, not a count.
- The update's backup refuses when the dump and the disk's reserve do not fit. On a 95% full machine the local rehearsal failed there until old kits were removed.

## Keep doing
- Judge where each job ran (its own record), not the stage's setting.
- Run a release as it is installed (its wheel in its own environment) when the question is what a home runs.

## Open
- Waves 4–9 (16.4–16.9, with 16.7's Epic 33 swap across the flip): their rows skip until their plugins merge. Each wave's workflow row and wave-specific checks come with it: files kept (5), downloads not duplicated (6), the TV swap and rollback (7), Plex settings kept (8).
- `rate_style` has no subject in the small seed (no seasons).
- The macOS `/tmp` socket path in `sandbox.policy_for`: a small follow-up.
