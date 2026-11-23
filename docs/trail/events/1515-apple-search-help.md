---
schema: 1
kind: pr
pr: 1515
title: "Search help on the Apple Player and Apple TV (49.11, Apple part)"
date: 2026-11-22
session: b6bacc17-4700-40a7-b0f0-e6a9a38a96f9
agent: claude-code
prs: [1497, 1511]
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
- CI found three more problems, each fixed in #1515:
  - Tapping a suggestion in `.searchSuggestions` also submits the search field. For a title,
    that submit replaced the held "open the video" with a search of what was typed
    (`LetGoFirst` keeps the latest change). Both apps now skip the submit that comes with a
    title. On the TV, the submit waits 300 ms, because tvOS can send it just before the
    suggestion's own action.
  - `ViewThatFits` in the refine bar went back and forth between its two layouts on an iPhone
    in Arabic. The app never went idle: every XCUITest tap waited 60 s. It is one row now,
    whose labels wrap.
  - The facets' sheet opens at half height on a phone, and its List draws only the rows on
    screen, so the test scrolls a bucket into sight before tapping it.
- One iPhone shard then failed with "Audit failed to complete in time" in the accessibility
  audit. The same test passed on the run before, with the same app code, and a re-run of the
  shard passed.

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

- What tvOS shows XCUITest:
  - `.searchSuggestions` is a row of cells under a one-row keyboard. The cells keep the label
    ("seerah  ·  Word") and drop the identifier;
  - a `Menu` is a button that never reports `hasFocus`, and its items are "Other" elements,
    not buttons;
  - after the button that ran a search goes away, the focus returns to the keyboard.

  The TV test now steers by frames: up or down to the target's row, then along it, and the
  focus counts as reached when the focused frame overlaps the target. That needs no
  identifier and follows the language's direction.
- A screen recording from the `.xcresult` (`xcresulttool export attachments`, then `ffmpeg` to
  a tiled grid of frames) showed where the focus went. Guessing had not found it.

## Keep doing
- Delegating a read-only review to another model before the PR. It caught the TV focus bug
  without a simulator.

## Open
- The other parts of 49.11 were built in #1517 (Android, Android TV, desktop) and #1519 (the
  web big screen, Roku), stacked on this branch.
- `TVSearchUITests.testSuggestionsShowTheirKindsAndATitleOpensItsPage` failed once, on #1519's
  run 45086064605 ("the focus on the title's suggestion"). #1519 changes nothing under
  `apps/tv/tvos` or `apps/apple`, and the test passed on this branch, so it flaked. The test
  presses down at most 4 times, then right at most 8, with 400 ms between presses. On a slow
  simulator the focus had not reached the suggestions' row in time. If it fails again, wait
  for the focus to land after each press instead of sleeping a fixed time.
- The TV's transcript results open the video's page, not the player at a passage.
- Whether tvOS sends `onSubmit(of: .search)` from its search keyboard. The TV does not rely
  on it.
