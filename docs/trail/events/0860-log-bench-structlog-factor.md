---
schema: 1
kind: pr
pr: 860
title: The Python log bench's factor follows structlog's own p99
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- #806, which does not touch logging, failed `unit / log-bench` with a Python p99 of 55.7 µs against a 55.3 µs budget, at runner factor 2.77. Its run predates #838's tail term.
- A temporary 24-job matrix, run twice on this branch, sampled the bench on 48 runners and recorded each one's CPU. The logger's p99 sat at 0.55–0.68 of its budget on the AMD EPYC 9V45, the 9V74 and the Xeon 8573C, and at 0.75–0.79 on the AMD EPYC 7763.
- A call's p99 through structlog's stock JSON pipeline now joins the factor, against the reference's 8.7 µs. The 7763 moves to 0.73–0.75, and the other runner types are unchanged.

## What we learned
- A run's rounds agree within 2–3%, and a CPU type's runs within 4%. The spread is between CPU types, so more samples or a median of repeats would not change a verdict.
- C-code loops (hashing, JSON, regex) do not follow interpreter-bound work across CPUs, and neither did a small pure-Python loop: per unit of it, Python's p99 was 400 on the 9V45 and 590 on the 7763. The library itself, without our processors, did follow.
- A matrix job that records `/proc/cpuinfo`'s model name beside the numbers answers in about 10 minutes what single CI runs could not.

## Keep doing
- Before changing a bench's gate, sample it across runners and group the results by CPU model.
- Calibrate against the same library the code under test uses, never against the code under test.

## Open
- Nothing.
