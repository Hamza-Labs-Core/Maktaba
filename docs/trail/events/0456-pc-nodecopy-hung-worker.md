---
schema: 1
kind: pr
pr: 456
title: End a copy worker that hangs on the home before a fresh copy
date: 2026-10-31
session: c17dd289
source: claude-trail.md
---

## What happened
The 2026-10-28 copy stall: a table's first copy waited 15 min on the restarting home, and every
fresh copy failed with "workers did not stop within 30s". #456 (merge be9ba4ef) makes quiesce
end workers still there after its wait, and its switch-off waits for the subscription's lock at
most that wait (a review finding). Tested against real Postgres, red before and green after.
