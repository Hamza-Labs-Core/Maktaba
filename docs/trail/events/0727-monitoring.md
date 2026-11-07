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
- A workflow step's `name:` with ": " in it is invalid YAML. actionlint passed it; the lint job's Python tests (which load every workflow with PyYAML) failed. Load the workflows with PyYAML (or run `tools/ci/tests`) after editing one.

## Open
- Epic 53.17 (the cloud's log forwarding and log store) emits the `maktaba_*` series two of these alerts use; 50.11 to 50.35, 53.16's service-admin view and 53.18's `reports.share` follow.
