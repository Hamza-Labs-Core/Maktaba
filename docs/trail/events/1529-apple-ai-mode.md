---
schema: 1
kind: pr
pr: 1529
title: "AI mode's tab in the Apple Player and on Apple TV (49.14, Apple part)"
date: 2026-11-22
session: 396db35a-4bf0-467a-846c-e6f8a87a9500
agent: claude-code
prs: [1529]
---

## What happened
- #1529 adds Search's second tab, **AI mode**, to the Apple Player (iPhone, iPad, Mac) and
  the Apple TV, against 49.12's `POST /api/search/ai` (#1525). Both apps share MaktabaKit's
  `AIMode.swift`: `SSEParser`, `AIEvent`, `APIClient.askAI` (over `URLSession.bytes(for:)`),
  `AIModeModel` (the conversation, follow-ups with the last three answered turns, Ask again,
  cancel) and `AIWords` (English and Arabic).
- The Player's tab is `AIModePane` and the TV's is `TVAIModePane`. On the TV, the question is
  typed in the search field and asked from **Ask “…”** or **Ask as a follow-up: “…”** under the
  keyboard. A TV has no second text field for follow-ups.
- `MockHome.stubStream` sends a stream's first part at once and each later part on a
  `release()`. The tests use this latch to see the place in line, then the sources before any
  sentence, without racing a timer.
- The CHANGELOG, the spec's As built and the epic status are left to the lead's closing PR for
  49.14 (four parts built in parallel).
- CI found these problems, each fixed in #1529:
  - `affected-tests` linked `AIMode.swift` to `TVParentalUITests`, which shares only the words
    "Ask again", and did not count `AIModeTests.swift`. A `No-Test-Change` trailer with that
    reason, 50aca7bd0.
  - The website job: new help anchors (`ai-mode`) must be listed in
    `web/packages/website/dist-manifest.json`, b74dfadfa.
  - The Player's accessibility audit, run 45109831118: "Potentially inaccessible text", three
    times, with no element named. Adding the library to the source card's label (4e9c62c57)
    did not fix it; run 45116460111 failed the same way. The cause was the second source card
    in a horizontal row, cut by the screen's edge: its words were drawn partly outside the
    window. The sources are now rows, the first three, then Show all n sources, db54a3bd5.
  - The TV, run 45116460111: four of five search tests, three of them older tests that passed
    on main, failed at "the field never had the keyboard". The new Results and AI mode row sat
    right under the search field, and `typeQuery` presses Down from the tab bar until the field
    has focus, so the focus could pass the field. The row now shows only once something is
    typed, AI mode is open or it holds a conversation, db54a3bd5.
  - Dynamic Type, run 45122208432: the library name in `.caption2` was 18 pt high at the
    smallest size and at the default. It is 13 pt relative to `.caption` now, as the search
    rows' details are, 2fc20affa.
- Failures in tests this PR does not touch, all "Timed out while evaluating UI query":
  `PlayerDemoShots.testPlayer` (iPad shots, run 45116460111),
  `PlayerLibrariesUITests.testLibrariesInArabic` (iPhone 4/6, run 45122208432) and
  `PlayerHomeUITests.testAFailedRowIsARetryTileInArabic` (iPad 3/6, run 45128192106, re-run
  once). On run 45109831118, `PlayerSearchUITests.testATitleOpensItsPage` waited 62 s for idle
  on an iPhone and `TVSearchUITests.testSuggestionsShowTheirKindsAndATitleOpensItsPage` failed
  to give the field the keyboard; both passed on the next runs.
- A GLM-5.3 review found no major issue. Fixed by the lead in
  f342969997de8702c74b51433dc4aff2fee180f6:
  - `AIWords.clock` trapped on a valid-JSON but absurd `start_sec` (1e308): `Duration`
    cannot hold it. It now clamps, and a number that is not finite reads as absent.
  - On Apple TV, the documented follow-up meant climbing back from the answer to the
    search field, which the UI test could not do reliably. `.searchFocused` is unavailable
    on tvOS, so the follow-up is now a field and Ask under the answer, as on the Player.
    The UI test that asked from it failed twice in CI (the field never had the keyboard,
    after Select whether or not focus was reported), so `TVSearchUITests` checks only that
    the field is there. Asking a follow-up on the TV has no UI test.
  - New question disappeared after a refusal.
  - The 401 renew-and-retry path and the timeout line had no tests; both do now.
- The last head's CI (run 45156256805) needed three attempts. Each failed shard was a UI
  timeout and passed on a re-run:
  - `PlayerDemoShots.testFilm` and a `PlayerParentalUITests` test: "Timed out while
    evaluating UI query".
  - `PlayerSearchUITests.testFacetsNarrowCombineAndAreTakenOff` (iPad), then
    `testNewWordsDropTheFiltersAndKeepTheOrder` (iPhone): "the results after Return never
    went idle", each in a different attempt. The PR adds nothing that animates in the Results
    tab, and both passed on their re-runs. If this check fails again on main, look for a
    screen that keeps redrawing (Trail #1418).

## What we learned
- `URLSession.AsyncBytes.lines` drops empty lines. Server-Sent Events end each event with an
  empty line, so the stream is read byte by byte (`SSEParser`). A line is decoded as UTF-8
  only once it is whole. The unit test cuts the stream at every byte, inside Arabic letters
  too.
- A refusal (409, 429, 404) arrives on the same `bytes(for:)` call as a stream. The client reads
  up to 4 KB of the body and throws `APIError.status`, so the problem's `code` and `help` are
  read the way the other routes read them.
- A 404 on `/api/search/ai` means a home from before 49.12 (MockHome's default 404 shows it in
  `testAIModeOnAnOlderHomeSaysItHasNoneYet`). 405 is read the same way, in case a catch-all
  answers a POST.
- The audit's "Potentially inaccessible text" with no element can mean words drawn outside
  the window: a card cut by the edge of a horizontal row. A row scrolled along has no place
  in an audited Player screen. Use rows, or a capped list with Show all.
- On tvOS, a focusable view added right under the search field changes where Down from the
  tab bar lands. Every TV search test opens the field that way, so a control there must
  wait until it is needed.
- From an answer far down the page, Up on the remote stops at the keyboard's row, not the
  search field scrolled off the top. Typing again on the TV starts from the tab bar (Menu),
  and even that stayed unreliable, so the TV test leaves the follow-up to the Player's test.
- `.caption2` does not shrink below its default size: a text in it fails `assertTextScales`.
- A Player view with a fixed width and `lineLimit` risks the accessibility audit's clipped-text
  check at large text sizes. The source cards wrap their text instead.

## Keep doing
- On tvOS, put the input where the focus already is. A field off the top of a long page is
  hard for the remote to reach, and focusing it from code is not available.
- Keep every word in MaktabaKit's catalog when the Player and the TV share a feature. One
  test checks both languages, and neither app's catalog changes.

## Open
- The TV's follow-up field under the answer: no UI test reaches it with the remote.
- The TV's follow-up (**Ask as a follow-up**) has no UI test. The model's follow-up is unit
  tested, and the Player's UI test covers it there.
- No screenshots of AI mode on maktaba.dev yet. The UI tests attach them
  (`player-*-search-ai-mode*`, `tv-search-ai-mode-*`); the Apps pages can take them from a
  green run.
- AI mode sends no `filters`. The Results tab's filters do not narrow the passages.
- On the Player, a citation's push leaves Search. If the answer is still being written, it
  stops; back on the tab, **Ask again** asks it again.
