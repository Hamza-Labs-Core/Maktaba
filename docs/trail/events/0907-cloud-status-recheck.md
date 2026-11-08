---
schema: 1
kind: pr
pr: 907
title: The apps read the cloud's status at once when the relay fails
date: 2026-11-07
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [752, 791]
---

## What happened
- Story 50.11's leftovers. On Android, Android TV and Apple, a request through the relay that gets no answer, 502, 503 or 504 now reads `summary.json` at once, at most once every 10 s. Before, the read came at the next 5-minute poll. The relay's `home_reconnecting` is excluded: it is 50.13's wait, not an outage.
- Android raises it from `ApiClient`'s single send point, and `HomeStatusHost`'s loop waits on the 5 minutes or the recheck. Apple posts a notification from `APIClient.exchange`, and `CloudStatusWatcher.run` races it against `Task.sleep`.
- The "Android TV strip" leftover was already built: the TV's screens have used the shared `HomeStatusHost` since a225243d5. Event 0752 was written on a branch from before that. What was missing was a test, and `CloudStripTvTest` now drives the strip on the TV.

## What we learned
- The TV test's first run showed a strip before any failure was triggered. The home's own `/status.json` had failed through the relay, which correctly asked for the feed. The test now gives the home an answer, so "all well" is real.
- Running the test once with the old `delay` put back (it fails) is the cheap proof that it checks the wiring.
- The affected-tests gate maps a source file to its test by name across modules (`client`'s `CloudStatus.kt` to `core`'s `CloudStatusTest`). Pure rule tests belong there.

## Keep doing
- Before building a leftover, check whether a later merge already built it; trail events can be written on a branch that predates it.

## Open
- None for 50.11 in the apps.
