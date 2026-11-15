---
schema: 1
kind: pr
pr: 1381
title: "The six remaining broadcast rehearsal scenarios (story 50.29 follow-up)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1381 merged as 904f900cd. Six scenarios that were `Built: false` now run on the real edge in
  Miniflare and the real home: `broadcast-plans` (full, lowest, wait, back to full; nobody
  playing is cut), `broadcast-access` (invite device caps, short-code try limit, Turnstile once,
  per-network and daily limits refuse only new sessions, revocation at the next beat),
  `broadcast-takedown` (slate within 60 s, counter-notice restore, third strike off air),
  `broadcast-publishable` (guide holds only publishable items; a withdrawal slates within 15 s),
  `broadcast-hetzner-down` (nightly; a real cloud outage, then every usage window reaches the
  cloud exactly once) and `broadcast-observe` (expected failure: the alerts, status page and
  canary probe are not in this stack). The spec's file table matches the files.
- CodeQL's umbrella check failed on `go/allocation-size-overflow` for `make(map, len(x)+4)` in
  the rehearsal tool; dropping the size hint cleared it.

## What we learned
- The edge refuses a home clock more than 60 s off its own, so a fast clock cannot run while the
  home is live; outage scenarios run in real time.
- The `broadcast-observe` work found that glass-to-glass read 0 (the home sent ffmpeg's late
  PROGRAM-DATE-TIME); fixed in #1382.
- Remove capacity hints built from `len(x)+k` on small maps rather than dismissing the alert.
