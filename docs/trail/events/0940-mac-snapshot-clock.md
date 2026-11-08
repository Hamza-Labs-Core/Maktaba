---
schema: 1
kind: pr
pr: 940
title: Mac page snapshots took their fixture times from the clock
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [918]
---

## What happened
- From midnight UTC on 2026-11-08, `apple-apps / Player UI tests (mac)` failed on every pull request that ran it, and on main. `PageSnapshots` compares each page's text with a reference, and the video page draws its date. The fixture (`video-v10.json`) sets it to `{{now-1728000}}`, 20 days before a `{{now}}` that `MockHome` filled from the clock. The references were made on 2026-11-07, so each run after midnight drew Oct 19 against a reference of Oct 18, in English and Arabic.
- `MockHome(now:)` takes the time to fill from (nil, the clock, for every other test). `PageSnapshots` pins it to 2026-11-07 12:00 UTC: the references' day, at noon so the date is the same in any time zone. No reference changed. It first showed on #918 and was split out of it to unblock everyone.

## What we learned
- A reference test (snapshot, text dump, screenshot) that draws anything read off the clock fails the day after its references are made. Before recording, check whether the pages it covers draw a date, a time or a relative age from `{{now…}}`.
- Pinning the clock is safe only if the pages shown do not use the fixtures' future times (expiries, guide slots), which a past day would already have passed. The snapshotted pages (library, series, video) use none.

## Keep doing
- Fix a failure that breaks every PR in its own small PR from fresh main, merged first, and not inside a feature PR.

## Open
- The iPhone/iPad Player and Remote UI tests still fill `{{now…}}` from the clock. That is fine while they assert on structure and not on drawn dates; a new screenshot reference there needs the same pin.
