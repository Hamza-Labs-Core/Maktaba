---
schema: 1
kind: pr
pr: 1351
title: "Roku: run T04 alone against a like-for-like reference loop (story 58.15)"
date: 2026-11-14
session: bd8c469f-122f-4be5-9cb4-f04ff55297e3
agent: claude-code
---

## What happened
- #1351 merged as fdbbbf3c7, right after #1347. #1347's CI run read T04 at 3.31 units (722 ms
  against a 218 ms addition loop), close to its 3.5 ceiling. The container read 2.5 on arm64 and
  on x64 under emulation alike.
- T04 moved to `tests/timing/t04.test.mjs`, which `npm test` runs alone after the other files.
  The reference loop now runs the shaping code's kinds of statements (a for each, string
  comparisons, array reads, pushes and additions). The ceiling is 2.2: about 1.65 now, 2.7 for
  the code before #1347.
- On the runner T04 now reads 1.70 (345 ms against 203 ms).

## What we learned
- `node --test` runs test files side by side. On a CI runner, the Roku screens tests took enough
  CPU to double T04's time (722 ms to 345 ms) and inflate a ratio by a third. The architecture
  was not the cause.
- A ratio against a reference loop of the same kinds of statements holds steadier than one
  against a plain addition loop.

## Keep doing
- Read a timing test's number from the first CI run's log before calling the ceiling settled.

## Stop doing
- Timing tests that share the CPU with other test files.

## Open
- None beyond #1347's real-device row 10.
