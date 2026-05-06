# Story 25.11 — Bandwidth metering & accounting

> Epic 25 · Cloud relay · Phase 3 (billing)

## Description

Every byte that flows through the relay (25.9) is counted. The counter
drives tier enforcement (25.12), the user-facing usage dashboard, and
the cost-per-user accounting on the admin revenue dashboard (25.21).
Bandwidth is the single biggest variable cost we have; getting it right
is a billing necessity, not a feature.

What we count:

- **Per direction.** `bytes_in` is request-body bytes; `bytes_out` is
  response-body bytes. Headers are *not* counted (they are constant
  overhead, dwarfed by media). Tunnel framing overhead is also not
  counted (HamzaLabs eats it; ~1% of payload at typical request
  sizes).
- **Per server, per day, in UTC.** Rolled up into
  `cloud_bandwidth_daily(server_id, date, bytes_in, bytes_out)`.
  The current day is held in Redis as a hash counter; flushed to
  Postgres every 60s and on graceful shutdown.
- **Per stream.** Live stream counters live in a Redis
  `cloud_streams_active` hash, used by 25.12 for concurrent-stream
  enforcement.

What we *don't* count:

- LAN traffic (the cloud doesn't see it).
- Failed-handshake bytes (TLS overhead before any payload).
- Heartbeat traffic on the tunnel (PING/PONG).
- Push notification payloads (separate accounting; under threshold).
- Admin / cloud API calls (`/api/me`, billing, etc.) — we charge for
  *relay* traffic only.

Aggregation jobs:

- **`flush_bandwidth` (every 60s).** Drains Redis hash counters into
  Postgres. Idempotent: counter values are deltas; Postgres applies
  `INSERT … ON CONFLICT DO UPDATE SET bytes_in = bytes_in + EXCLUDED.bytes_in`.
- **`rollup_monthly` (1st of each month, 00:10 UTC).** Sums daily
  rows into a `cloud_bandwidth_monthly(user_id, year_month, bytes_in,
  bytes_out, peak_concurrent_streams)` for invoices.
- **`stale_stream_reaper` (every 30s).** Closes
  `cloud_streams_active` rows whose `last_seen_at` is > 90s old
  (client crashed, network died, never sent FIN).

Surfaces:

- `GET /api/me/usage?from=...&to=...` — returns daily bytes for
  the calling user across all servers. Web dashboard chart.
- `GET /api/servers/{server_id}/usage` — same scoped to a server.
- `GET /api/admin/usage` — fleet-wide summary.

## Acceptance criteria

- **Given** a client streams a 100 MB HLS chunk through the relay,
  **when** the request completes,
  **then** within 60s, `cloud_bandwidth_daily` for that
  `(server_id, today_utc)` row has `bytes_out` increased by
  exactly `100 * 1024 * 1024 ± 0`.
- **Given** Redis fails over,
  **when** the relay is unable to write counters,
  **then** the request still completes (counters are best-effort),
  but a `bandwidth_counter_dropped_total` metric increments and an
  audit row records the drop.
- **Given** a server is suspended mid-stream,
  **when** Stripe pushes `subscription.canceled`,
  **then** the relay refuses new streams with `402
  payment_required` but already-open streams complete
  (bandwidth still counted; final invoice includes them).
- **Given** the operator runs the monthly rollup at 00:10 on the
  1st,
  **when** the job completes,
  **then** every active user has a `cloud_bandwidth_monthly` row
  for the prior month and the job is idempotent (rerunning is a
  no-op).
- **Given** a client opens a stream but never sends a FIN,
  **when** 90s pass without bytes,
  **then** the row is reaped from `cloud_streams_active` and
  the bandwidth from the last delta is flushed.
- **Given** the user opens the usage dashboard,
  **when** the page loads,
  **then** the response shows daily bytes for the last 30 days
  with p95 query latency < 200 ms.
- **Given** Redis is empty (cold start),
  **when** the first request arrives,
  **then** the counter starts at 0 and the dashboard reflects
  the new bytes within 60s.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | counter increment | feed 1k 1MB writes | total exactly 1 GiB |
| T02 | integration | Redis crash + restart | issue 10 streams during outage | dropped count metric > 0; service still healthy |
| T03 | integration | flush job runs | inspect Postgres after 60s | row sum matches Redis sum |
| T04 | integration | monthly rollup | run twice | second run is no-op |
| T05 | regression  | client RST mid-body | observe | partial bytes counted accurately |
| T06 | unit        | header bytes excluded | mock request with 8KB headers, 0 body | counter +0 |
| T07 | integration | concurrent 100 streams | flush | final total matches sum |
| T08 | integration | usage endpoint pagination (90 days) | call | pagination + cache works |
| T09 | regression  | DST boundary (`Europe/Berlin`) | run rollup over period crossing DST | UTC rows unchanged; user-facing display localizes only |
| T10 | unit        | overflow defense | 2 GiB single transfer | no integer wrap (use `int64`) |
| T11 | regression  | stale streams reaper window | hold a stream silent for 89s vs 91s | 89s survives; 91s reaped |

## Edge cases

- **Counter accuracy vs. consistency.** We pick "eventually
  exact" over "real-time exact": Redis counters can be ~60s
  stale, but losing a flush is at most 60s of writes. For
  billing we add a small safety margin (round monthly totals
  *up* to the nearest MiB so we don't undercount).
- **Negative deltas.** Should never occur; if observed,
  alarm — indicates corruption.
- **Per-IP counters for abuse.** Distinct from billing
  counters. 25.25 owns those.
- **Bandwidth reporting via tunnel.** Servers don't report
  bandwidth — we count what we relay. The server's own
  observability (Epic 21) reports its perspective; the
  cloud's accounting is authoritative for billing.
- **Refunds.** Stripe refunds are handled by the
  refunder action in 25.14 / 25.21; we never auto-refund
  on bandwidth dispute.
- **Suspended user, ongoing streams.** Final-billing
  edge: a stream open at suspension time runs to
  completion (we don't kill it mid-frame). We
  surface this in the suspension UI.
- **Cross-month stream.** A long stream that starts on the
  31st and ends on the 1st is split across months for
  rollup purposes (we count bytes in the period the byte
  was relayed; the relay updates the daily counter for
  *current UTC date* on every flush).
- **Free tier counter.** Free users have a hard cap of 0 GB
  relay; the counter still runs (we want to show "you used
  0 GB" in their dashboard) but enforcement (25.12) blocks
  any non-zero stream.
- **GDPR delete.** On user delete, monthly rollup rows
  retain `user_id` until 90 days after delete (audit
  retention) then become anonymous (`user_id = NULL`).
  Daily rows are kept 90 days then dropped.

## Files / packages

- `cloud/internal/relay/meter.go` — per-stream counters.
- `cloud/internal/jobs/flush_bandwidth.go` — Redis →
  Postgres.
- `cloud/internal/jobs/rollup_monthly.go`.
- `cloud/internal/jobs/stale_stream_reaper.go`.
- `cloud/internal/billing/usage.go` — read API.
- `cloud/migrations/00030003_bandwidth.sql`.

## Open questions

- **Burst peak in invoices.** Should we surface 95th-percentile
  peak concurrent streams on the invoice as a "soft" data
  point? Defer; v1 only shows sums.
- **Per-IP byte counts.** Useful for abuse forensics; high
  cardinality. Defer — sample on abuse incidents only.
