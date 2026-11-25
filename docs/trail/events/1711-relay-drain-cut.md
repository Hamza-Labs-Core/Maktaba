---
schema: 1
kind: pr
pr: 1711
title: "TestDrainDeadlineForcesTheClose: the relay missed the cuts the home made first"
date: 2026-11-24
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- `TestDrainDeadlineForcesTheClose` failed on #1616 with `tunnel_drain_forced_total += 0, want 1`
  (issue #1678, job 138956933154).
- The relay and the home time the same drain deadline. When the home's close came first, the
  relay's watcher found the tunnel gone and counted nothing. A real race, in the metric alone: the
  stream is cut either way.
- #1711 counts in `Close`, by the deadline set before DRAIN is sent. With the relay's timer 80 ms
  late, main fails 10 of 10 and the fix passes 20 of 20. 480 plain runs at a load average of 120
  did not show it.

## What we learned
- Two parties on one deadline have no order. A count that depends on who acts first is a race, and
  load alone may not show it: delay one side by hand.
- A counter read after `Done()` must be written before it is closed.
