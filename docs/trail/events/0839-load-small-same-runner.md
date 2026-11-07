---
schema: 1
kind: pr
pr: 839
title: load-small compares the relay with its base on the same runner
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [808, 832]
---

## What happened
- After #808, main's `load-small` went red, and #832 failed it at +34.6 %. #808's cgroup median was steady within a run (±1 %), but it compared against main's last run. The same relay code then measured 0.000451 to 0.000875 CPU-seconds per Mbit across runners (main at `2917a4967`, `ec0dfe5ce`, `6be172b2d`, `d97ee875e`).
- The `load-small` job now builds the base commit's relay and runs `load-small` with it, then with this commit's, on the same runner (3 one-minute rounds each), and compares the two. A base that can't run, or a missing baseline, fails. Main's artifact baseline is gone.

## What we learned
- A performance gate compares on one machine or not at all. #808 fixed the sampling and then compared across machines, which is the noise its own local evidence couldn't show: that evidence was one machine, A/B alternating. When a figure depends on hardware, both sides must run in the same job.
- #808's event says the gate was fixed. It was half fixed: this is the other half.
- Before merging a gate change, look at what it does on main (the push runs right after the merge), not only on the PR.

## Open
- None.
