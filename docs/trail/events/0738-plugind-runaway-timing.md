---
schema: 1
kind: pr
pr: 738
title: A runaway plugin job is timed from its start
date: 2026-11-07
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
prs: [683]
---

## What happened
- The plugin host's runaway-job test started its clock before preloading the guest, so compile time counted against the deadline + 250 ms bound and a slow macOS runner failed it at 1.28 s. The clock now starts at the job.

## What we learned
- A timing assertion must time only the thing it is about. The third such test fixed today (after #694 and #696), and each looked like a flake until read.
