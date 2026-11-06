---
schema: 1
kind: pr
pr: 362
title: A Windows node's API and copy read the passfile
date: 2026-10-27
session: f43fad60
source: claude-trail.md
---

## What happened
On the Windows node, lib/pq ignored the passfile, and an inherited `PGPASSWORD` got in the way.
#362 moved to lib/pq v1.11.2 so the node's API and copy read the passfile.

## What we learned
- Older lib/pq ignores the passfile on Windows.
- An inherited `PGPASSWORD` was a second cause; check the environment a node process inherits.
