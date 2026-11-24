---
schema: 1
kind: pr
pr: 1578
title: "plugind: a compile is not the job's time, with filler that grows until the compile is the longer"
date: 2026-11-24
session: e1c12b82-6431-40f4-b0ae-96e43fdfa26e
agent: claude-code
---

## What happened
- `plugins / plugins-sandbox (macos-15)` failed on #1566 (run 45324039388) in
  a_compile_is_not_the_jobs_time: "the compile took 2455 ms, not more than the job's 3392 ms".
- The job's limit is four times the guest's own timed run. On a loaded runner the guest took
  about 850 ms, so the limit outgrew one filler's compile. The test failed its precondition,
  not the property it checks.
- The filler now doubles (80,000 functions, up to 320,000), each time in a fresh host, until
  the compile outlasts the limit. #1566's failed jobs were re-run on the same run and passed.

## What we learned
- A margin as a multiple of a measured time moves with the runner's load. The other side of
  the comparison must be able to grow too, or the test flakes on exactly the busy runners it
  was given the margin for.

## Keep doing
- Fix a time-dependent test the day it is seen, in its own pull request, and re-run the red
  job of the change it blocked.
