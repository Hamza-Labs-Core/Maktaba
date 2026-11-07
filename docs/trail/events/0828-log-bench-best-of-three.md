---
schema: 1
kind: pr
pr: 828
title: The Python logger bench is judged on the best of three rounds
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
issue: 744
---

## What happened
- The bench has been strict in CI since #802. It failed #824, which does not touch the Python logger: one round's p99 was 56.5 µs against 54 µs, on a runner the calibration measured 2.7 times slower.
- It now runs three rounds and judges the one with the lowest p99, and reports every round.

## What we learned
- A strict latency budget on a shared runner needs the same robust statistic as the calibration it is scaled by. Noise only adds time, so the best round is the cost.
- Turning a warning into a gate on the evidence of one green run was too early. Watch a gate over several runs, on several runners, before making it strict.

## Open
- Nothing.
