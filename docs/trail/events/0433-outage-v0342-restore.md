---
schema: 1
kind: incident
pr: 433
title: Outage - the v0.3.42 update and failed restore
date: 2026-10-28
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [429]
---

## What happened
2026-10-28, 17:58 to 18:13, v0.3.42. The update watch timed out while TV change #57 waited for
approval, so the update rolled back. The full restore then failed with "tuple concurrently updated"
because the PC's copy grant ran GRANTs mid-restore. Vectors, 630 post-data items and the ACLs were
missing and were recovered by hand; the ACLs were missed at first.

Root causes fixed in #433; TV changes stop blocking updates in #429. Both first shipped in v0.3.43.

## What we learned
- v0.3.39 to v0.3.42 shipped in one day, each fixing the last, and each rollback made things worse. This is why update paths are proven in CI first and only one release goes live at a time.
- After a hand recovery, check every restored part (vectors, post-data, ACLs), not only the tables.

## Stop doing
- Stacking fixes on the live install in one day.
