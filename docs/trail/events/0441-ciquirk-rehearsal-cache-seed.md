---
schema: 1
kind: pr
pr: 441
title: Rehearsal scenarios take their seed from the run
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
The update rehearsal failed with "Failed to restore cache entry" when the cache was evicted.
Scenarios now take their seed from the run, not the cache (#441). Shipped in v0.3.44.
