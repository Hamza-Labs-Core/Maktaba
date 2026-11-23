---
schema: 1
kind: decision
title: "Story 49.11 on the big screen, /tv/ and Roku: search help reused from the web, Roku on POST /api/search"
date: 2026-11-22
session: 8ad4cab4-1867-49bd-aedc-d95c6349a3e2
agent: claude-code
prs: [1519, 1511, 1497]
---

## What happened
- #1519 builds the big-screen, TV-web and Roku part of Epic 49 story 49.11 on story 49.8's API
  (#1497), matching the web page of 49.10 (#1511). The Apple and Android parts run in parallel
  PRs; 49.11 stays Partial in `docs/wiki/epic-status.md`.
- **Big screen** (`web/src/bigscreen/screens/Search.tsx`, `searchAsk.ts`): suggestions under
  the screen keyboard with their kinds, did-you-mean, order chips, a Filters panel (a focus
  layer) of facet chips with counts, the filters in use as removable chips, all in the URL. It
  imports the web's `searchHelp.tsx` and `searchShared.tsx`; `facetGroups`, `activeChips`,
  `facetsOf` and `didYouMeanOf` were taken out of them for it. `/tv/` (Samsung, LG) is the same
  code; `apps/tv/web` has no search screen of its own.
- **Roku**: `SearchScreen` moved from `GET /api/videos?q=` to `POST /api/search`, with the
  logic in `source/search.brs` (brs-engine tests, also in the device-test package) and a new
  screen test, T07, in both languages.
- One review round by GLM-5.3 found one major (Roku re-sent the old filters after the box was
  emptied and the same words typed again) and three minors (the big screen's panel lost focus
  when its last chip went; Roku's sheet jumped back to the pressed row when an answer came;
  test gaps). All fixed in f2a9498ac.
- #1519 was stacked on #1517, which is stacked on #1515. With all of 49.11 in, this branch
  marks the story Done. Its first CI attempt (run 45086064605) failed two Apple UI tests this
  branch does not touch, and both passed on the re-run:
  - `TVSearchUITests.testSuggestionsShowTheirKindsAndATitleOpensItsPage`: the focus had not
    reached the suggestions' row;
  - `PlayerParentalUITests.testAChannelOpenedBeforeTheLimitIsKnownStillShowsTheSlate`:
    "Timed out while evaluating UI query".

## What we learned
- brs-engine's SceneGraph differs from a Roku in ways a screen test meets:
  - `SetFocus(true)` on a screen whose child MiniKeyboard has the focus leaves the keyboard
    taking OK (it deleted a letter). A dedicated focusable `Group` holding the focus works, as
    the player's `keys` group does.
  - A LabelList's first Down may only settle its focus; drive lists by a logged
    `itemFocused` (`[focus] … item N`), pressing until it shows, never by a count of presses.
  - Its MiniKeyboard is taller than the old fixed layout assumed (it ends near y 867 of
    1080), so the screen now places the typed text and suggestions from the keyboard's
    `BoundingRect()`.
  - Text set on a MiniKeyboard keeps the cursor where it was: a letter typed after a
    programmatic `text` lands mid-word.
- ECP key presses in brs-engine can be lost under load; `pressUntil` (press, read the log,
  repeat) made T07 steady where fixed key counts were flaky.
- Back to Home restores focus to Home's menu at the item left, so a test that walks the menu
  reads the index first (`toMenu`).
- axe flags `aria-allowed-role` on every big-screen video tile (a `button` with
  `role="listitem"`, `ui.tsx` `VideoTile`); the new axe check sets that rule aside for tiles.
  `Panel` was an `aside` with `role="dialog"`, also refused; it is a `div` now.

## Keep doing
- Pull shared pieces out of the web page instead of copying them into the big screen; the
  desk page's DOM stays the same through default props.
- Keep a client's search logic apart from its screen (`searchAsk.ts`, `source/search.brs`) so
  request bodies and tolerance of an older home are unit-tested.

## Open
- The Apple apps (iPhone, iPad, Mac, Apple TV) and the Android, Android TV and desktop Player
  parts of 49.11.
- The big screen's video tiles' `role="listitem"` on a `button` (axe `aria-allowed-role`).
- Locally on macOS the Roku screen test's T05 and T06 (parental player) failed in a full run,
  and the main T03 once; they were not touched by #1519, and CI's Linux job is the reference.
- Roku's ✓ mark is drawn from IBM Plex Sans Arabic (which has U+2713); brs-engine draws a
  look-alike. Check it on a device.
