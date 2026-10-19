# Plan 33.15 — Observability — implementation

> Implementation plan for [story-33-15](story-33-15-observability.md). No migration (swap summary in
> `tv_changes.diff.swap_summary` or a `verify_report` sibling field).

## 1. Files

**Modify**
- `streaming/internal/frontdoor/metrics.go` — swap/splice/stitch/relay counters, slot gauges from `tv_slots`
- `streaming/internal/slot/metrics.go` — per-slot station gauges
- `api/internal/tvchange/metrics.go` — change states, verify results, rollbacks
- control centre (web) — slot bar and in-flight change widgets
- `shared/log` event names

## 2. Steps

1. Metrics and events with consistent field names.
2. Swap summary aggregation in the front door, stored by the API.
3. Control centre widgets.
4. Alerts via `notify`.

## 3. Tests

Registration and summary tests; widget test.

## 4. Rollout

With each phase.
