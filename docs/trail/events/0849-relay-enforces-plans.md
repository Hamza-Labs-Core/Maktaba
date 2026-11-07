---
schema: 1
kind: pr
pr: 849
title: The relay enforces each plan (story 50.19, part 2)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [832, 839]
---

## What happened
- Story 50.19, part 2 of 4:
  - Free is refused on the relay (`402 relay_needs_plan`, pages in en/ar), with the tunnel kept;
  - with `relay.plans.enforce` on, capped plans carry `X-Maktaba-Relay-Cap-Kbps` and their media is paced at the cap + 20 %;
  - `relay_streams` (00500008) counts streams across both relay colours, under a per-home advisory lock, with `429 stream_limit` past the limit and the worker's sweep after 2 minutes.
- #832's same-runner `load-small` (the first after #839): base 0.000816, this 0.000826 CPU-seconds per Mbit (+1.2 %), memory +5.4 %. Counting bytes by mode costs next to nothing.

## What we learned
- A stream limit needs a definition of "a stream" that a relay can see. HLS has no close, so a stream is its session id in the path (or channel/clip + client), written at most once a minute and ended by silence. Never decide it from one request.
- Two relays admitting the last slot is a race. A Postgres advisory lock per home around count + insert closes it; ten concurrent opens admitted none past the limit.
- Pace per stream, not per response: a player fetches segments one after another, and a per-response bucket would refill between them.
- A client could send the cap header itself. Strip it before setting it, so only the relay decides a home's quality.

## Open
- Part 3: the payment provider's items 2 and 3, usage reports, pay-as-you-go, the Billing page. Part 4: the home's side and the `relay-plans` rehearsal.
