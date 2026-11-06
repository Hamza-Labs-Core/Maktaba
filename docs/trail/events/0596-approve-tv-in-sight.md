---
schema: 1
kind: pr
pr: 596
title: Approve a TV in sight: button, waiting banner, notification
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 6c8cadc2b. A TV's sign-in code was hard to approve: the approval was only in the account menu.
- The Android Remote has an "Approve a TV or device" button on every top bar (the account menu item stays), and the Player has it as the first Profile row. The Apple Remote has a toolbar button.
- New signed-in `GET /api/pairing/pending` lists waiting devices without their code or secret, rate-limited per IP; cloud sign-ins get an empty list. The Android Remote and Player poll it every 12 s while open, show a banner with "Enter code", and post a notification on a "Sign-in requests" channel only if the permission is already granted. The Apple Remote shows the banner only. The device poll's 202 now carries `looked_up`.
- The Android TV and tvOS code screens name the phones' exact button, plus the older "Approve a sign-in", built from the phones' own string and tested so it cannot drift. They also show a countdown, "Get a new code", and "Your phone found this TV". English and Arabic everywhere.
