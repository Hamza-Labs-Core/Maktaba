---
schema: 1
kind: pr
pr: 1350
title: "Roku player: time's up and a title hidden mid-play (58.14)"
date: 2026-11-14
session: 263a473e-1c97-4037-abcf-3b8796c65f6a
agent: claude-code
---

## What happened
- #1350 merged as 39034e757. It closes the gap that #1313 and #1331 each left to the other:
  the Roku player's own answers to the home's parental refusals.
  - 403 `parental_time` on a session open or a progress post: the video stops, its session
    closes, and the time's-up page shows inside `PlayerScreen` (title by reason, when watching
    opens again, the saved place, Ask for more time with 15, 30 or 60 minutes, Check again).
    A parent's yes (followed every 15 s) or Check again opens the session again where it
    stopped; the home decides.
  - `no_heartbeat` reopens once where it was; a second lapse within 30 s stops for time.
  - 404 on a progress post, or on an open under a limit, replaces the player with
    `BlockedScreen` (a stopped line when it played); its yes plays the video again from there.
  - 503 `parental_time_unknown` on an open shows the cannot-check line, never free time.
- The logic is in `source/parental.brs` (`ParentalStop`, `ParentalTimeUp`,
  `ParentalNextWords`), with 47 unit checks. Screens test T05 runs against a mock home that
  answers with the streaming handler's problem body. Five new dark references (en, ar);
  real-device rows 32–37; the parental guide and the TV apps page on maktaba.dev, en and ar.
- The full Roku suite passed in `node:24-bookworm` (22 tests), then in CI.

## What we learned
- brs-engine's Video node never plays, so its position stays 0. A seek in a screens test
  lands at the step itself (10 s), not at the start position, and the progress timer never
  fires. Drive a progress post with a seek (its done-timer posts) rather than waiting.
- `BlockedScreen`'s reason label held one line at 100 px. Two lines (the stopped line and the
  limit) were cut with an ellipsis, which only the screenshot showed. Look at every new
  reference before committing it.
- `rm tests/screens/player-*-{en,ar}.png` also removed tracked references. Name new
  references by their exact file names.
- Avoid `stop` and `next` as BrightScript names (statements); use `answer` and `nextAt`.

## Keep doing
- Classify a server answer in one pure function (`ParentalStop`) and unit-test it against
  the server's real body, and give the mock home the same body.

## Stop doing
- Leaving a cross-story display to "the other story" without a named owner. #1313 and #1331
  each did so, and the gap waited for a separate PR.

## Open
- The player's "12 minutes left" chip and the five- and one-minute warnings (Android TV's
  `TvTimeOverlay`) are not on Roku. They need Arabic plural time words in the Roku catalog.
- A denied request for more time from an earlier day still reads as denied on Roku; web
  treats it as none once it expires (`expires_at`).
- Owner: real-device rows 32–37 on the next Roku release candidate.
