# Story 32.11 — Observability

## Description

*As the admin, I want to see at a glance how TV is doing across machines and outside the house —
hosts up, channels on air, failovers, remote viewers, minutes and cost — and find out quickly why
something failed.*

Design: [design.md](design.md) §10.

## Acceptance criteria

- **AC1** The metrics of design §10 are exported on each service's existing `/metrics`
  (Prometheus text), with the listed labels; no user names or IP addresses in labels.
- **AC2** The home writes `health_samples` rows (existing table: at, host, metric, value) every
  minute for `tv_hosts_up`, `tv_channels_on_air`, `tv_remote_viewers`, `tv_remote_pushes_live` and
  the month's cost estimate, so the control centre's health view charts them.
- **AC3** The structured log events of design §10 are emitted with `event=` and the channel,
  host, push and code fields; Cloudflare stream keys and tokens are never logged.
- **AC4** Admin → Live TV gets a **Remote** panel: integration status, inputs (state, channel,
  connected since, Cloudflare status), pushes (channel, host, state, since, viewers from sessions
  and from Cloudflare `views`), sessions (user, device, channel, since), this month's minutes and
  cost, the last 20 remote events.
- **AC5** The System Health page shows a TV line: hosts online / total, channels on air, failovers
  in the last 24 h, remote status.
- **AC6** A failed push or a host down shows on the Live TV page within 10 s.

## Test cases

- **TC1** `TestMetricsRegistered` — names and labels.
- **TC2** `TestHealthSamplesWritten`.
- **TC3** `TestNoSecretsInLogs` — run a push with a fake key; grep the captured logs.
- **TC4** Web: Remote panel renders each state; Arabic.

## Edge cases

- **EC1** Cloudflare `views` unavailable (hidden viewer count): the column shows "—".
- **EC2** Many hosts and channels: the on-air table paginates at 50 rows.

## Dependencies

32.1–32.10 (each story adds its metrics; this story wires the panels and the samples).
