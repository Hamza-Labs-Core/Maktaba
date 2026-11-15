---
schema: 1
kind: pr
pr: 1357
title: "Roku: the time left, its warnings, and an earlier day's no (57.15, 58.14)"
date: 2026-11-14
session: ff9eaade-bd8f-4224-9087-4fbcea60ab40
agent: claude-code
---

## What happened
- Written after the pull request merged, from its description, commits and diff; the session
  that did the work left no event.
- #1357 merged as 47f7ea47a and closed the two follow-ups the #1350 event left open.
- **Time left.** While a restricted account's video plays, the Roku player asks
  `GET /api/me/parental/time` (with the video's `library_id`) every 30 s, every 10 s from
  6 minutes left, shows the home's `remaining_seconds` and never counts on its own; a failed
  read keeps the last answer. From 15 minutes left a note shows in the end corner (mirrored in
  Arabic, below Up next while that card shows). At 5 minutes and 1 minute a gold warning shows
  for 10 s, once each, and again only after time is given back above it. This matches Android
  TV's `TvTimeOverlay`. The logic is pure functions in `source/parental.brs`, unit-tested.
- **An earlier day's no.** `ParentalRequestState` now matches the web's `stateOf`: a denied time
  request whose `expires_at` has passed reads as `none`, so the time's-up page offers Ask for
  more time again; a no to a video stays a no; a pending request past its end reads as
  expired. Android TV, Apple TV and Samsung/LG (the web's `/tv/`) already agreed.
- Tests: `parental_test.brs` grew from 47 to over 100 checks; screen tests T06 against the mock
  home, which now answers the time endpoint. The full Roku suite passed 23/23 in
  `node:24-bookworm`.

## What we learned
- The Roku catalog takes a plural as an object of CLDR forms: English `one`/`other`, Arabic
  `zero`/`one`/`two`/`few`/`many`/`other`. `shape_strings.py` keeps the forms logical under
  `plurals`, each fitted to its label with the number at its widest; a `"value": true` flag keeps
  a string that only goes inside another template logical too. `I18n.Plural` picks the form,
  `I18n.Fill` fills without shaping, and `I18n.Format` shapes the finished line once.
  Shaping a fragment before it is placed in its line would break Arabic joining and order.
- `tools/i18n/check_tv_catalogs.py` now refuses a language with forms that are not its own, a key
  that is a plural in one language only, mismatched `other` placeholders, a form with a
  placeholder its `other` lacks, and Latin words in Arabic forms.
- `expires_at` comes from Go with fractional seconds and an offset; Roku parses it in
  `parental_Seconds`, and `ParentalNextWords` uses the same parser.

## Open
- Owner: real-device rows 38 to 41 in `docs/stores/roku.md` (the note, the warnings, Arabic with
  Up next, an earlier day's no) on the next Roku release candidate.
