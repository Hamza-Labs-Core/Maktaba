---
schema: 1
kind: pr
pr: 1724
title: "The permissions browse timed out with the answer behind a tick: a race fixed twice in part"
date: 2026-11-25
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- `allowingThenClosingIsNotShownAgain` failed on #1713 with `.unknown` (issue #1678 item 9, job
  139001674296).
- `PermissionsModel.browse` read signals and ticks from one queue, and a task of its own passed the
  probe's signals on. Held back, it let a tick be queued first; with the main actor then held past
  the limit, the tick was read first and the answer behind it never was. c9812127c and be01ebb36
  moved the clock and left the task.
- #1724 has the probe report straight into the queue. With more busy threads than cores and the
  main actor held in turns of 1.1 s, main ends 71 of 220 requests "unknown" and the fix 0 of 420.

## What we learned
- In a main-actor task, `for await` over an `AsyncStream` leaves the main actor for its next
  element, even one already buffered: with the pool full it waits for a thread.
- A busy main actor, or a busy pool, or both, did not show the race: their queues keep order. It
  needed threads the system holds back, as a 3-core runner's are.
- "Signals already queued are heard first" needs them queued. Ask who queues, and what can hold
  that up.

## Open
- No committed test fails if such a task comes back: the load that shows it takes every core for
  20 s.
