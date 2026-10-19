# Story 33.15 — Observability

## Description

*As the owner, I want to see in the control centre and the logs what staging is doing, how long swaps take,
and whether anyone noticed.*

Design: [design.md](design.md) §15.

## Acceptance criteria

- **AC1** Metrics of design §15 exported by the front door, slots and API on their existing metrics
  endpoints.
- **AC2** Events of design §15 in the structured logs with `change_id`, `release_id`, `channel_id`, `slot`,
  `epoch` fields; visible in the control centre's event stream.
- **AC3** The control centre shows the slot bar (per host: live/staging/standby, release, version) and the
  in-flight change with its progress; runs of `tv_stage` and `tv_gc` appear as visible runs.
- **AC4** Per swap a summary is stored on the change: duration, per-channel switch latency, splices
  (aligned/retimed), stitches, relay switches (seamless/retimed/reconnect), skipped channels, viewers at
  the time.
- **AC5** Alerts (notifications, 33.13): swap with any relay reconnect; > 0 skipped channels; staging extra
  encodes hit the budget for > 10 min.

## Test cases

- **TC1** `TestMetricsRegistered`.
- **TC2** `TestSwapSummaryStored`.
- **TC3** Control centre component test with fixtures.

## Edge cases

- **EC1** Metrics from a stopped slot disappear (no stale gauges): the front door reports slot gauges from
  `tv_slots`, not the slot process.

## Dependencies

33.1–33.10.
