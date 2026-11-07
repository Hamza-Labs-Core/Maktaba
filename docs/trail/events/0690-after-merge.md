---
schema: 1
kind: pr
pr: 690
title: After the merge of #690 — the log bench's calibration on an x86 runner
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- On CI's x86 runner the capture bench took 23 µs of wall time a line, over its 20 µs budget, with a runner factor of 1.00. The calibration only hashed (SHA-256), which that runner does as fast as the reference Mac, while the keeper's JSON decode and scrub ran slower there.
- Capture is now budgeted by the process's CPU time per line, since its target is a share of a core. The factor is the larger of the hashing loop and a decode-scrub-encode loop over 5,000 corpus lines (44 ms on the reference).
- The corpus unit test had called `Calibrate()`, which with the new loop took 1.75 s under `-race`, over the per-test cap; the unit test no longer calibrates.

## What we learned
- Calibrate with work like the work being budgeted: a CPU with SHA instructions says nothing about JSON and regex.
- A budget phrased as "a share of a core" is a CPU-time measure; wall time includes the runner's scheduling.
