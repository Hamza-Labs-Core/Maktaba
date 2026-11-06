---
schema: 1
kind: migration
title: What waited on the owner on 2026-10-31
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [399, 458]
---

## What happened
The first trail listed the decisions and actions waiting on the owner at the end of
2026-10-31. Carried over as a snapshot; later events may have settled some of them.

## Open
- Cut the next release (after v0.3.44): not approved then.
- Retry TV change #58 (held with `release_mismatch`): offered, unanswered.
- Delete Actions run 41226231934: its log lists the team's bundle IDs and certificate types (no secrets). Offered, unanswered.
- Android debug APKs for sideloading: offered, unanswered.
- Versioning: 0.4.0 or 1.0.0. Unanswered; releases stayed on 0.3.x.
- TestFlight internal testers: the owner adds themselves to an Internal Testing group in App Store Connect for each app.
- Epic 45 duplicates: three duplicate libraries waited for the owner's Merge in Manage libraries (as of 2026-10-27).
- Shelved: Epic 45 stories 45.22 and 45.23 (#399).
- Epic 48 spec (#458): review it and answer its five open questions (downloads, hiding Live TV, Chromecast, the Windows/Linux player, keeping Light).
