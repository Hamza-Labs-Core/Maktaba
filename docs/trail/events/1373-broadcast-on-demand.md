---
schema: 1
kind: pr
pr: 1373
title: "On-demand broadcast channels: the edge sends wake, sleep and limits"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1373 merged as d89b9bf8f. An on-demand channel now uploads only while someone watches: the
  edge's channel Durable Object sends `wake` to the home at the first viewer (guests see
  "tuning" until segments arrive), `sleep` after 150 s with no heartbeat (the home stops 5 min
  later, and a viewer in that window keeps it running), and `limits` for the plan's quality
  count or the lowest rung alone. Always-on channels never get wake or sleep. On reconnect the
  edge resends wake (when watched) and the current limits. The home's always-on fallback is gone.
- The edge learns a channel's mode from an `X-Maktaba-Mode` header on the home's existing
  control socket, already authenticated by its ingest token; an old home that sends none is
  treated as always on.

## What we learned
- Read the existing transport before adding one: the home's control socket already carried the
  commands, so one connect header was enough and no new signed format was needed.

## Open
- Owner action: deploy the edge before any home release containing #1373. A new home on an old
  edge is never woken, so its on-demand channels would stay dark.
