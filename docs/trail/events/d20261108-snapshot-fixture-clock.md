---
schema: 1
kind: incident
title: The Mac page snapshots took their fixture dates from the clock
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
---

## What happened
- From midnight UTC on 2026-11-08, `apple-apps / Player UI tests (mac)` failed on every pull request that ran it, and on main. `PageSnapshots` compares each page's text with a reference, and the video page draws its date. The fixture (`video-v10.json`) sets it to `{{now-1728000}}`, 20 days before a `{{now}}` that `MockHome` filled from the clock. The references were made on 2026-11-07, so each run after midnight drew Oct 19 against a reference of Oct 18, in English and Arabic.
- #936 (merged at a30257aad) fixed it: `MockHome.clock` is the time the fixtures are filled from, and `PageSnapshots` pins it to 2026-11-07 12:00 UTC, the references' day, at noon so the date is the same in every time zone.
- The same failure hit #918 independently. Its fix was split into #940, then closed unmerged as superseded once #936 landed: the two pinned the same instant. #918 then merged main and took main's versions of `MockHome.swift` and `PageSnapshotTests.swift`. #940's run also hit one unrelated snapshot flake (`library-open-ar-ax3`: one extra row drawn past the reference's end).

## What we learned
- A reference test (snapshot, text dump, screenshot) that draws anything read off the clock fails the day after its references are made. Before recording, check whether the pages it covers draw a date, a time or a relative age from `{{now…}}`.
- A pinned past clock is safe only if the pages shown do not use the fixtures' future times (expiries, guide slots), which would already have passed. The snapshotted pages (library, series, video) use none.
- Before splitting out a fix for a failure that breaks every PR, check main's latest merges: another session may already have fixed it.
- A gate that requires a check to have run (not just "no failure") must match the check's full rollup name (`apple-apps / Player UI tests (mac)`). One that matched a shortened name saw the check as absent the whole time.

## Open
- The iPhone/iPad Player and Remote UI tests still fill `{{now…}}` from the clock. That is fine while they assert on structure and not on drawn dates. A new screenshot reference there needs the same pin.
