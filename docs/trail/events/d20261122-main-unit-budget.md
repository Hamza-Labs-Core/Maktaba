---
schema: 1
kind: incident
title: Main red on the unit tier's per-test cap (TestMachineRoutesAdminOnly)
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
agent: claude-code
prs: [1493]
---

## What happened
- Main's CI runs from 2026-11-19 22:38 UTC to 2026-11-20 02:43 UTC all failed, each in a
  different job: Player UI tests (iphone 4/6) twice, broadcast-edge, `unit / desktop-shell`
  (a crash-report test), the Fire TV build on the Android TV emulator, and last `unit / unit`
  at 00d588d0e (run 44662804878, the merge of #1482). The later commits on main were trail-only,
  so that run stayed main's latest result, and no release could be tagged.
- `unit / unit` failed on a test-budget hard-cap breach that was not load:
  `api/internal/handlers/components::TestMachineRoutesAdminOnly` took 1.23s, and 1.24s re-run
  alone, against a 1.2s hard cap (3x the 400ms soft cap). The test built three machine
  fixtures and sent 49 requests; the first fixture also paid for the one-time build of the
  package's SQLite fixture template (about half the test's time locally).
- #1493 splits the admin and manage-link passes into tests of their own, with the same
  assertions, and builds the throwaway template without a journal or sync. Locally the slowest
  of the three takes 0.28s alone (the old test took 0.44s).

## What we learned
- "Main is red since X" can hide several different failures. List the failed jobs of each run
  (`gh run view ID --json jobs`) before choosing what to fix.
- A test-budget breach whose re-run alone is still over the cap is the test's cost, not the
  runner's. CI ran about 2.8x slower than a local `-race` run here.
- A package's fixture template is paid for by whichever test asks for it first, so that test
  carries the build in its time.

## Keep doing
- Fix a standing breach by splitting the work into independent tests. Keep every assertion.

## Open
- The other jobs that failed on main in that window (Player UI tests, broadcast-edge,
  desktop-shell, the Fire TV emulator) passed on main's latest run. If they fail again, look
  at them as flakes.
