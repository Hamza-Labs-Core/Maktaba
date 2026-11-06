---
schema: 1
kind: pr
pr: 614
title: Player Home on HomeShelves; tvOS buffering banner; stalled players reopen
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge bdc25c10d. The Player's Home builds its rows with MaktabaKit's `HomeShelves`, as the Apple TV does; a row that fails keeps what it last showed or shows a Try again tile.
- On tvOS a stall over 1 s shows the banner and "Buffering...", and a bottom-corner logo moves the banner and mini-guide to the top, using the `watermark_position` the guide API now sends.
- On TV and Player a player still waiting after 15 s reopens the stream: on loaded CI runners AVPlayer gave up on a late segment and waited for good, including after an audio switch on the iPhone.
- The Player's accessibility audits load each text size's posters before they run, and a restored `@SceneStorage live.mode` no longer overrides the launch's view.
- The secrets store test used to look for the two bytes "v2" in random AES-GCM ciphertext, which matched about once in a couple of thousand runs; it now checks a long, distinctive value.

## What we learned
- AVPlayer can give up on a late segment and wait forever; a watchdog that reopens the stream is needed.
- Searching ciphertext for a short byte string is a random-chance flake; use a long, distinctive value.
