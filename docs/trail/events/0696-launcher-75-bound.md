---
schema: 1
kind: pr
pr: 696
title: The launcher's restart-on-75 test bounds by the backoff it must skip
date: 2026-11-06
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
---

## What happened
- `TestLauncherRestartsOn75` asserted two restarts within a fixed 1.5 s while the backoff it checks for is 2 s; a slow Windows runner took 1.54 s with no backoff (#669's run). The bound is now `cfg.BackoffInitial`.

## What we learned
- A test that asserts "X did not happen" should bound by X's own size (here one backoff), not by a guess of how fast the runner is. Same shape as #694.
