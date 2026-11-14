---
schema: 1
kind: pr
pr: 1331
title: "Parental screens on the new TVs: blocked page, Ask a parent, managed line (58.14)"
date: 2026-11-14
session: 13f121e8-f4e3-4dc2-8e8c-81d703aad903
agent: claude-code
---

## What happened
- #1331 merged as 930f7aabf. It finishes story 58.14; the server half (bypass rows A23–A27)
  was #1321.
  - **Samsung, LG (`/tv/`) and `/big`**: a 404 on a video, series or collection, for a limited
    sign-in, shows `BigBlocked` (`web/src/bigscreen/screens/Blocked.tsx`). Ask a parent takes
    focus, and Check again takes it once asked. A line says a parent answers from their phone,
    and a yes opens the item by itself. There is no PIN.
  - Inside the big screen, the player's `BlockedOr` and `TimesUp` draw the 10-foot pages,
    through a `ParentalScreens` context (`components/parental/screens.ts`). Settings shows
    `BigManaged`. `/tv/` gained the `ParentalProvider` it lacked.
  - **Fire TV / Android TV**: Ask a parent takes focus, the phone line is added, and Account
    shows `TvManaged`. The PIN from Epic 57 stays, as on the Apple TV, and the spec table is
    corrected to say so.
  - **Roku**: the channel reads `/api/me/parental` after sign-in. A 404 for a limited sign-in
    opens `BlockedScreen`, which follows the request every 15 s and replaces itself with the
    video on a yes, so Back skips it. A deep link to a hidden id shows the not-available line
    and asks nothing. Account shows the managed lines (`source/parental.brs`).
- maktaba.dev: the parental guide gains *On a TV* and the TV rows; the TV page gains *A child's
  account on a TV*. Both are in English and Arabic.
- CI's first run failed `affected-tests`: changed screens (`Collection.tsx`, `Series.tsx`,
  `Settings.tsx`, `Video.tsx`, `AccountScreen.kt`, `SignedInApp.kt`) had no test change. Real
  tests were added (BigScreen per-screen blocked page and Settings, Android Account's managed
  line) rather than a `No-Test-Change` trailer.
- Main moved three times under the pull request:
  - `web/e2e/tv` conflicted with #1322's reading-order test; both sides were kept.
  - #1322 moved Roku's `ui_Colors` into a generated `components/theme.brs` that every screen
    now includes. The new `BlockedScreen.xml` failed bslint until it included it too.
  - #1340 (Arabic shaped inside English lines) conflicted in Roku's `AccountScreen.brs`; its
    `ui_Arg` calls and the managed lines were both kept, and `account-managed-en` was redrawn.

## What we learned
- In Chromium a `disabled` button drops the remote's focus to `body`. Use `aria-disabled` on
  the big screen, and move focus by ref when the focused control goes away.
- A brs-engine deep-link test that fires while Home is still loading its For you row leaves the
  remote's focus on the removed Home. Wait for `[home] for you` before sending the link.
- Roku's `i18n.Format` shapes Arabic once per fill. A translated word (the All ages tier) goes
  in a whole-string key, never inside another template.
- `trail check` with git checks can run for half an hour locally; `--no-git` is quick, and CI's
  lint job runs the full check on a trail-only pull request.

## Keep doing
- Run the platform's whole test suite again after each merge of main: a new screen misses what
  main added to every other screen.

## Stop doing
- Pushing a screen change without checking which tests `affected-tests` maps to the changed
  files.

## Open
- The Roku player's own time's-up and blocked display (PlayerScreen) is left to 58.13 (#1313).
