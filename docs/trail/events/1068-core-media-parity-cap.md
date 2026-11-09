---
schema: 1
kind: pr
pr: 1068
title: core-media's parity test fits its soft cap; every parity test starts faster
date: 2026-11-09
session: aa4f6da6-177c-478d-81fa-7ab103e7cac3
---

## What happened
- #1068 merged at 2dd0d18b2. `test_core_media_wave_9_is_identical` took 7.2 s on CI against the 5 s integration soft cap. The N16 bench (9 stages × 11 runs × 2 sides) was 1.4 s of it and can't shrink without weakening N16, so the test was split into three, each building its own world and timing one N16 group; a fourth test checks the groups together cover exactly every stage once. Each now runs in ~2 s locally (~3.5 s on CI).
- The fake ffmpeg now answers `thumb_small`, so its jobs write a thumbnail that is compared across sides instead of failing three times each.
- Harness, for every parity test: the dev server starts while the databases are copied and seeded; template copies and drops run concurrently; a setup that fails before the dev server is awaited still stops it and drops both databases (new selftest). The full first-party command went from 93–98 s to 79 s.
- One update-rehearsal job, 16.7 (wave 7's TV change across an update), failed on this PR: `job 2 ran plugin on "0.3.111", want inproc on 0.3.111`, right after the log said tv_stage switched to inproc. The PR touches only Python tests, and 16.7 passed on every other recent run; a rerun of the failed job passed and the PR merged.

## What we learned
- Splitting a slow proof by what it times, with a test that the parts still cover the whole, keeps the proof's strength while meeting a time cap.
- A fake that refuses a call hides that call's outputs from the comparison; make fakes answer, so the parity covers them.

## Open
- 16.7's failure may be a real race: a job dispatched just after the stage's switch to inproc still ran as a plugin. Worth checking whether the switch is visible to the dispatcher before the rehearsal stages the next job.
