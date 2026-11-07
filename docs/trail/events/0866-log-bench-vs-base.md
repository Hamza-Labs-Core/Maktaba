---
schema: 1
kind: pr
pr: 866
title: The Python log bench is judged beside its base on the same runner
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- #806 changed nothing in the logger and failed the strict gate twice at factor 2.7–2.8, while #823, which holds #806's code, passed. Both of #806's runs predated #838, but even after #838 and #860 the 7763 runners sit at 0.73–0.77 of the budget, against 0.55–0.68 elsewhere.
- The `log-bench` job now also measures the logger of the commit a change goes onto, on the same runner. Rounds run in fresh processes, base and head alternating. Over the budget fails a change only when it is also more than 15% slower than that base. Without a base, the budget alone decides.

## What we learned
- No calibration loop follows the logger to better than about 15% across CI's CPUs: not C loops, not a pure-Python loop, not structlog's stock pipeline alone or in any combination. A separate Python factor only moves the tight spot to a different CPU.
- A reference that is the same code, measured in the same job, removes the runner from the question "did this change make the logger slower?". The absolute budget still says whether the cost is acceptable.

## Keep doing
- For a bench gate on shared runners, compare with the base commit measured beside the change, alternating rounds. Report the runner factor's verdict, but do not let it alone fail a change.

## Open
- The Go logger bench (`logbench`) has the same kind of gate. Its factor followed Go more closely (about 1.33× against Python's 1.65× on the slow runners), so it is left as is until it flakes.
