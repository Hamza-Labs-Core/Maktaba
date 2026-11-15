---
schema: 1
kind: pr
pr: 1399
title: "Broadcast edge: a heartbeat writes its viewer, not the whole channel state"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1399 merged as c5236bce9. The nightly on main (run 43683183048, b6ed73832) failed
  broadcast-fanout's 1,000-viewer copy-rung fetch p95 (1675 ms over 1.5 s), blocking releases.
  Earlier passing runs ranged 176 to 1132 ms. Neither #1389 nor #1390 changed the segment path.
- Evidence: on an idle 24-CPU machine the old code failed once (2264 ms) and passed once
  (200 ms), so it was not runner CPU; in the failing run the edge froze about 9 s (the channel's
  minute alarm ran 9 s late, three ingest PUTs went unanswered for 8 s) as 900 viewers joined.
- Cause: every heartbeat rewrote a whole Durable Object state. `ChannelLive.viewer` copied the
  viewers map, stringified it twice and wrote every viewer, every rung's segment keys and the
  playlists; `PublisherControl.beat` wrote every session and seven days of usage windows.
  O(viewers) a beat, bursting each minute and on joins. On Cloudflare it holds the channel's
  ingest and bills a write unit per 4 KB; in the rehearsal, with one workerd thread, it stalls
  every fetch, which is why the p95 swung with timing.
- Fix: each viewer (`v:`), session (`x:`) and usage window (`w:`) is its own key. A watching
  viewer's heartbeat updates memory and is saved by the minute alarm (inside the 150 s session
  TTL); a new session, changed mode or lapse writes its key at once, and "s" only when the
  home's wake or limits change. The old layout is moved once. `test/beat_writes.test.ts`.
- A first version stopped saving the publisher id a beat teaches, so a restarted object keyed
  usage windows to no publisher; `broadcast-hetzner-down`'s exact-metering check caught it
  (80 of 320 viewer-minutes) and `takePub` fixed it, with a restart test.
- Branch nightlies after the fix: 410, 208 and 285 ms p95 at 1,000 viewers; the 4-CPU runner
  ran about 60 % busy with no steal. The rehearsal report now splits each fetch (first byte,
  the edge's own answer time, reload lateness, CPU of edge, home and viewers, runner busy and
  steal, CPU model).

## What we learned
- A p95 that swings 10x between runs on the same code is a hidden stall, not noise: look for
  work that grows with the audience on a hot path.
- In a Durable Object, write per-entity keys, never the whole map per event; the minute alarm
  can carry soft state that a lost write only delays.
- Keep the scenarios that check exact accounting in every nightly: they catch storage
  refactors that drop a field.

## Open
- The nightly on main (run 43697217604, on c5236bce9) must pass before the next tag.
