---
schema: 1
kind: pr
title: "Search help on the Apple Player and Apple TV (49.11, Apple part)"
date: 2026-11-22
session: b6bacc17-4700-40a7-b0f0-e6a9a38a96f9
prs: [1515]
---

## What happened
- #1515 brings story 49.8's search help to the Apple Player (iPhone, iPad, Mac) and the Apple
  TV. Both apps use one model in MaktabaKit: `SearchModel`, with `SearchModel.Ask` (query,
  sort, filters), and `SearchHelp.swift`.
- The Player shows each suggestion's kind (a title opens its video), a did-you-mean row, a
  Filters sheet with counts, the filters in use as removable chips with Clear all, and a sort
  menu.
- The Apple TV had searched titles with `GET /api/videos?q=` as you typed. It now uses
  `POST /api/search` with the same help. The home keeps every POST as a recent search, so
  the TV runs a search from **Search for “…”** under the keyboard, not from each keystroke.
- An independent review (GLM-5.3) found a TV UI test that could never reach a row's second
  bucket with Down alone. tvOS moves the focus between rows with Up and Down and along a row
  with Left and Right.

## What we learned
- `String(localized:)` wraps each interpolated argument in bidi isolates (U+2068, U+2069).
  Tests that compare localized sentences must strip them, or expect them. A sentence built by
  hand ("Facet: value") needs them added, so a Latin name keeps its place in an Arabic line.
- `Locale(identifier: "ar")` formats Latin digits on this ICU; `ar_SA` gives Arabic-Indic
  digits. The apps run with the person's region, so a test of digits names the region.
- "Speaker" is «متحدث» as a suggestion kind and «المتحدث» as a facet. One English key cannot
  hold both, so the kinds are keyed (`search.kind.<kind>`, extractionState manual).
- The UI-test fixture's `q` does not echo what the tests type. Keying "the results on screen"
  on the echoed query swapped the list for a spinner on every refinement in tests.
  `Results.sent` (what the app sent) is the key now.

## Keep doing
- Delegating a read-only review to another model before the PR. It caught the TV focus bug
  without a simulator.

## Open
- The Android Player, Android TV, the desktop player, the web big screen and Roku parts of
  49.11.
- The TV's transcript results open the video's page, not the player at a passage.
- Whether tvOS sends `onSubmit(of: .search)` from its search keyboard. The TV does not rely
  on it.
