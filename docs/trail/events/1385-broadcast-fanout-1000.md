---
schema: 1
kind: pr
pr: 1385
title: "Broadcast: one PUT per segment and one R2 read per location at 1,000 viewers"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1385 merged as c9fc90e95. The first nightly broadcast rehearsal (run 43666460653) failed
  broadcast-fanout at 1,000 viewers, which blocks every release under the #1376 gate.
- Three causes, fixed in turn, each found from a branch nightly:
  - 429 duplicates: a rung catching up after a stalled edge sent its backlog back to back and
    hit ChannelLive's 4-per-second burst guard. The uploader now paces each rung's PUTs 500 ms
    apart and honours Retry-After.
  - R2 stampede: simultaneous misses on a new segment each read R2. `cached()` now coalesces
    misses per key within the isolate (the first fills, others wait up to one target duration;
    only a 200 is shared).
  - Reset duplicates (run 43669899046): 8 PUTs got "connection reset by peer" on a reused
    keep-alive connection, then a 201; the edge never stored the reset try. The uploader now
    sends an Idempotency-Key, so Go's transport replays the PUT on a fresh connection itself.
    An edge test proves a same-body re-PUT answers duplicate and writes R2 once.
- Run 43672016912 then failed only the end-of-run tally (480p: home 315, edge 316): one PUT got
  no answer at the home although the edge stored it. The tally now allows an extra edge store
  only for a segment whose every home PUT got no answer, at most 2 % of a rung, each named.
- Run 43674939952 on the final head passed every scenario; the 2 % allowance was not used.

## What we learned
- Log each PUT's latency and client error in the rehearsal home before theorising: the reset
  cause was visible only once the PUT log carried the error text.
- Go's `http.Transport` replays a request on a dead pooled connection only for idempotent
  methods or when the request carries `Idempotency-Key`; a PUT with a body needs the header.
- A stopped child that hit its usage limit had already pushed a fix; check the branch's head
  before briefing a replacement, so two agents never redo the same step.

## Open
- The nightly on main (run 43677138213, dispatched on c9fc90e95) must pass before the next tag.
- The 2 % unanswered-PUT allowance has not yet been exercised in a real run.
- golangci-lint with the rehearsal build tag flags two issues in `scenarios_cloud_test.go`.
