---
schema: 1
kind: pr
pr: 1408
title: "Watch a broadcast on Android, Android TV and Fire TV (50.27 T04, T06)"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1408 merged as 02448f6e8. A guest can watch a broadcast on Android phones and tablets,
  Android TV, Google TV and Fire TV, as on Apple (#1400): handle plus `MK-` code, QR pairing
  with a check code, or a strictly parsed invite link; channels with now and next; a player
  that refreshes the viewer token per request. Passes are Keystore-encrypted and kept out of
  backups. English and Arabic throughout.
- Parental limits fail closed (the same gap #1409 closes on Apple): one `GuestGate` and
  `GuestDoor` (`core/guest/GuestAccess.kt`) open the entry only once the home has answered and
  the account is not limited; with no one signed in, the device's saved flags decide and an
  unreadable flag counts as limited; an open guest screen and its player close when the state
  turns limited; links are held while unknown and refused while limited, with messages; a
  guest screen a new sign-in shut is not reopened, so an invite is never redeemed twice.
- The review found two real bugs: the Fire TV CI job ran only the first class of a
  comma-separated list, so `TvGuestJourneyTest` never ran though the job was green
  (`tools/ci/firetv-journey.sh` now runs each class and fails on a missing one); and once it
  ran, it showed the guest player never requested the stream (it took a play as loaded when
  its address arrived). Also: the 188-byte TS packet rule would have rejected every encrypted
  (AES-128) segment without a length; it now applies to clear segments only.

## What we learned
- A green test job proves only the tests in its report: check the report lists the new
  classes, especially when a runner takes a list.
- Screen tests without a player can't catch a player that never loads; keep one journey test
  per platform that plays a stream end to end.

## Open
- The desktop Player's guest screens, a Report button in the apps, verified https app links
  and a remote switch to turn guest watching off.
