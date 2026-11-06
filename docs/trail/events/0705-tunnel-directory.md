---
schema: 1
kind: pr
pr: 705
title: Any relay process answers for any home: the tunnel directory, peer forwarding, holding
date: 2026-11-06
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [701]
---

## What happened
- Story 50.04: `relay_tunnels` / `relay_nodes` record which relay process holds each home's tunnel; a process without it forwards (signed, one hop) to the peer that has it, and holds a request for a recently seen home up to 15 s. The relay's pages got Arabic.
- The cloud rehearsal (50.06's harness, built on a stacked branch before this merged) ran drain-1k in Docker and found three bugs here and in #701's drain, fixed in this PR.

## What we learned
- Tunnel seqs from a counter started at process start order tunnels wrongly across processes: green started before blue, so green's newer tunnels lost to blue's older rows. A seq must be the tunnel's creation time.
- A home that closes its drained tunnel the instant its streams end races the relay, which may route one more request to it before the new tunnel registers (1 in 1,289 GETs got a 502). The home waits for a quiet second; the relay retries an idempotent request whose tunnel closed before any answer.
- 1,000 homes drained at 100/s with a fixed 5 s reconnect window take 10 s + 5 s: the 15 s budget is the arithmetic itself. The window must shrink as the drain proceeds.
- The shared logger redacts IP addresses in log lines (`10.50.0.11:8444` logged as `10.50.0.0:8444`): not a config bug when reading container logs.

- CodeQL's go/reflected-xss flags a request header's value that reaches a page even when only two values can match: return constants, not the matched text.

## Keep doing
- Run the containerised rehearsal before opening the story's pull request: the unit and in-process tests all passed while each of these bugs was live.

## Stop doing
- Chaining `go test … | tail && git commit`: the pipeline's status is tail's, so a failing test still committed.
