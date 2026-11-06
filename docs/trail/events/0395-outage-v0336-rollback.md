---
schema: 1
kind: incident
pr: 395
title: Outage - the v0.3.36 rollback
date: 2026-10-27
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [397, 398]
---

## What happened
The v0.3.36 update rolled back. The TV staging slot's timeout counted from the stage, so the
rollback ran a 14-minute dump, then hit a launcher timeout, then a restore that failed on a
publication conflict: the PC's copy grant rebuilt the publication. Recovered by hand with the
owner's OK; no data lost.

Fixed in #395 (a TV staging slot's timeout runs from its start; a rollback keeps the launcher's
guard, v0.3.37), #397 (never block on a backup; restores skip publications, v0.3.37) and #398 (an
app installed after a rollback runs instead of the kept copy, v0.3.38).

## What we learned
- A restore on the home races the PC's node-copy grant, which rebuilds the publication mid-restore.
