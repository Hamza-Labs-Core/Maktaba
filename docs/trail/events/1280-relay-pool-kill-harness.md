---
schema: 1
kind: pr
pr: 1280
title: "A killed pool relay drops every connection (T04 flake)"
date: 2026-11-14
session: a3c5c564-0c16-4bb7-a99c-9b198a02664e
agent: claude-code
---

## What happened
- #1280 merged as 070501d16. `TestPoolRelayLossStandbyTakesOver` (story 50.16 T04, #1241)
  failed in the integration job on #1269: a minute after r3 was killed, every home whose primary
  had been there still showed `online/r3`.
- The cause was the harness, not the product. `kill` closed the public and peer servers first,
  and their `Close` waited 250–500 ms for requests r3 was holding, with r3's tunnel listener
  still open. The homes failed over to their standbys correctly, then r1/r2 (cached pool view
  still listing r3) drained them back to r3, which accepted. Those tunnels registered after the
  registry sweep, and httptest never closes a hijacked connection (every websocket), so they
  lived on, answering PINGs, with r3's rebalancer stopped.
- `kill` now drops each listener and every connection it accepted (`trackedListener`) before
  anything waits. New `TestPoolKillDropsAHandshakeInFlight` fails on the old `kill` (a dead
  relay answered AUTH_OK) and passes now.
- Evidence (`-race -cpu 1,2`, 6 processes in parallel): old harness 1/72 failed; fixed 72/72
  passed; fixed harness with a deliberately broken home (redials only the relay that dropped)
  6/6 failed as `reconnecting/r3`.

## What we learned
- `httptest.Server.CloseClientConnections` and `Close` skip hijacked connections: a test that
  powers off a websocket server has to track its own accepted connections and close the
  listener before anything that blocks.
- Per-home state sampled every 20 ms after the fault showed the whole sequence in one failing
  run (drop, standby, drained back, stuck); the failure message alone pointed at the product.
- More than about 6 parallel race-built pool tests on one Mac exhaust ephemeral ports ("can't
  assign requested address") and fail in a different way: not this bug.

## Keep doing
- Before blaming failover, prove the dead side is really dead (no listener, no socket), then
  prove the test fails on a deliberately broken failover.

## Open
- A relay that loses its pool heartbeat but keeps running (not a power-off) keeps its tunnels
  and its rebalancer; whether it then drains its homes away is not covered by a test.
