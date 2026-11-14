---
schema: 1
kind: pr
pr: 1234
title: "Epic 58 story 58.08, part 1: the Epic 56 screens on the big screen"
date: 2026-11-14
session: c5bd0950-9d71-4da4-acdd-7062c20f3fd6
agent: claude-code
---

## What happened
- #1234 merged as abcdf9bd2. A library on the big screen (`/big`) opens on the TV apps' tabs:
  Recommended, Library (videos or series, Sort, Filter, a letter rail A…Z and ا…ي), Collections,
  Playlists and Categories, with Play and Shuffle on the server queue (a `library` source with
  the view's filters and order). Collections and playlists have their own pages; a series gains
  Play, Mark watched and season actions; Up next shows in the last 30 s, Shuffle again after a
  shuffled queue.
- The remote rules of Epic 56 §6.5: Back steps to the tab's top, then its tabs, then out; Play/Pause
  on a focused card plays it; a long OK (500 ms) or the menu key opens the card's actions; each
  tab keeps its focus and scroll. Times, episode codes and resolutions sit in `<bdi dir="ltr">`.
- A security review found the library screen's kept answers (a module-level cache) could show one
  user's rows to the next on a shared screen; they are now keyed by the user they were read for.

## What we learned
- The big screen's focus engine is geometric, so RTL mirrors Left and Right by itself: nothing to
  swap in code, only logical CSS and `dir` on the root.
- Arabic letters sort by the alphabet, not code points (أ, إ, آ belong with ا): rank by a fixed
  alphabet string.
- A module-level cache in a screen outlives a sign-out; key it by user or drop it on a user change.

## Keep doing
- Keeping the whole view in the URL (tab, show, sort, filters): Back, reload and Play agree.

## Stop doing
- Caching API answers at module level without an owner.

## Open
- Part 2, the TV build at `/tv/` with its serving and the `_tv-web.yml` gate, is #1247.
