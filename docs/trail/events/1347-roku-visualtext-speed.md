---
schema: 1
kind: pr
pr: 1347
title: "Roku: shape Arabic text in less time; T04 enforced in CI (story 58.15)"
date: 2026-11-14
session: bd8c469f-122f-4be5-9cb4-f04ff55297e3
agent: claude-code
---

## What happened
- #1347 merged as de1334f84. Story 58.15's T04 was open: 20 descriptions of 229 characters took
  about 320 ms through `VisualLines` on brs-engine, against the 50 ms budget, with only a 2 s
  regression ceiling in CI.
- Profiled step by step in the Linux container: prepare 53 ms plus a separate tashkeel pass
  25 ms, wrap 46 ms (it copied every character), shape 54 ms (two passes), order 120 ms (UAX #9:
  full-line W4 and W6/W7 passes, and a reversal pass per level). No single hot spot. The tables
  were already built once.
- Changes in `source/visualtext.brs`:
  - tashkeel is dropped while preparing (a gated string is prepared again with it kept);
  - `vt_Wrap` returns word ranges, and `vt_Shape` copies each line once, in one pass;
  - W4 to W7 visit only the digits, separators and terminators;
  - L2 and L4 are a single output pass (levels are 0 to 2);
  - printable ASCII not forced right to left skips the steps.
- Best of 7: 331 ms before, 207 ms after. The output is byte-identical on the 312 vectors, and a
  new test holds the ASCII fast path equal to the full steps.
- T04 is now `tests/timing_test.brs` (#1351 then moved its CI check into
  `tests/timing/t04.test.mjs`). It prints `TIMING shape= loop= units=`, and the
  device-test package runs it too, for `docs/stores/roku.md` row 10.

## What we learned
- On brs-engine, a statement costs about 0.85 µs and a call about 5.5 µs. The cost of
  `visualtext.brs` is roughly passes × characters, so fusing passes and dropping copies is what
  pays.
- A character's Arabic joining form is final once the next character is seen, so shaping can
  write each letter one step behind, in one pass (the lam-alef ligature is decided there too).
- BrightScript names are case-insensitive. In a scratch profiler, `tS` overwrote `ts` (the
  roTimespan), and `tO` failed to parse (`to`).
- `roTimespan.TotalMilliseconds()` is whole milliseconds: time a stage over the whole workload,
  not per call, or the sum undercounts.

## Keep doing
- Profile each step before changing code, and compare old and new as a best of N in one
  container run, next to a reference loop.

## Stop doing
- Setting a timing ceiling from container numbers alone: the first CI run read 3.31 against
  #1347's 3.5 ceiling (see the #1351 event).

## Open
- Run row 10 on a Roku Express: does the device meet 50 ms? brs-engine still takes about
  210 ms in raw time.
