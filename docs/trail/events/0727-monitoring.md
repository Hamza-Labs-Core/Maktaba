---
schema: 1
kind: pr
pr: 727
title: Monitoring: the canary home, the witness's Prometheus, alerts with unit tests, the outside probe
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [724]
---

## What happened
- Story 50.10: a real-cloudlink canary home, Prometheus/Alertmanager/Grafana/blackbox/Pushgateway on the witness as low-weight guests, alert rules with promtool tests and runbooks, a Floating IP check, and GitHub Actions as the probe from outside Hetzner (off until `CLOUD_PROBE` is set).

## What we learned
- promtool's unit tests compare annotations too: an expected alert with no `exp_annotations` never matches one that has them.
- In a promtool test a missing sample (`_`) is not a scrape failure: the last value lives on for the 5-minute lookback. `stale` is what a failed scrape looks like.
- An SLO counted per probe double-counts: a moment is down only when every probing network fails it, so the recording rule takes the best probe (`max(probe_success)`) before the burn rate.
- The relay's processes listen on loopback; the witness reaches their `/metrics` through a Caddy site on the node's private address, which follows the same readiness as the public edge (so it scrapes whichever colour leads).
