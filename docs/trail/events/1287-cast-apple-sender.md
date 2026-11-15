---
schema: 1
kind: pr
pr: 1287
title: "The Apple Player casts to Maktaba's receiver (story 58.06)"
date: 2026-11-15
session: ff9eaade-bd8f-4224-9087-4fbcea60ab40
agent: claude-code
---

## What happened
- Written after the pull request merged, from its description, commits, comments and diff;
  the sessions that did the work left no event.
- #1287 merged as d05ef913f (2026-11-15 UTC), stacked on #1286 and after the receiver (#1267).
  On iPhone and iPad the **Cast** button opens a list with AirPlay first and Google Cast TVs
  beside it. Before the first cast an interstitial explains iOS's local network prompt. The
  phone mints a cast grant, loads it on Maktaba's receiver, stops posting its own progress
  while casting (Continue Watching follows the TV, also when the phone is locked) and renews
  the grant on `urn:x-cast:tv.maktaba.cast` 30 minutes before it ends. An older home gets the
  plain signed URL and a line saying so.
- The pure parts live in MaktabaKit `Cast.swift` (custom data from `shared/cast/load-vectors.json`,
  config, the grant mint and its refusals, the namespace messages). The Player's `Cast/` uses a
  `CastContexting` protocol with a fake context for UI tests and `GoogleCastContext` behind
  `#if canImport(GoogleCast) && os(iOS)`. `google-cast-ios-sdk` 4.8.6 links only into the iPhone
  and iPad targets.
- `tools/apple/cast-app-id.py` writes `apps/apple/Config/Cast.xcconfig` from `shared/cast/app.json`;
  CI runs it with `--check`. With an empty id, `NSBonjourServices` expands to the existing
  `_maktaba._tcp` only and no Cast button shows.
- The pull request was taken over from a stopped session, which had left five Player UI
  shards failing on 541557fe1.

## What we learned
- SwiftUI folded the cast panel (a `.contain` stack holding one image and the mini controller's
  container) into the watch screen's container, so `cast.panel` never reached the
  accessibility tree, though the panel was drawn. A drawn backing (`.frame` + `.background`)
  did not bring it back. The fix (a3c339acd) put the identifier on the TV image, made the
  panel's VoiceOver headline ("Playing on <TV>").
- The Arabic label wraps the TV's name in bidi isolates (U+2068 … U+2069); a UI test compares
  it with them stripped, as `PlayerLibrariesUITests` does.
- On CI iPads one accessibility snapshot of the TV list over a playing video took 14 s, and in
  a later run a query made during the list's 5 s search timed out at 35 s; locally each takes
  0.2 s. The cause on the runners is unproven. The test now waits 40 s and queries only once
  the search is over (97a3614c8, 04cbc0166).
- A CHANGELOG entry at the end of its section conflicts on every merge of main, because each
  pull request adds there. Moving it to the top of the section stopped that (687875b7b).
- One failing shard was the known Ask-for-more-time race in
  `PlayerParentalUITests.testTimeUpAtTheStartInArabic`, fixed on main by #1309; merging main
  cleared it.

## Keep doing
- Read the failing xcresult's element hierarchy before changing a UI test's waits: it showed
  the panel drawn with no identifier, which pointed at the container, not at timing.

## Stop doing
- Putting a long-lived branch's CHANGELOG entry where every other pull request appends.

## Open
- Owner: register the receiver in the Google Cast SDK Developer Console, write its id to
  `shared/cast/app.json` `receiver_app_id` and run `tools/apple/cast-app-id.py`. Until then no
  Apple build shows Cast.
- Real-device rows in `docs/stores/cast.md`.
