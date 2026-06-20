# Epic 30 — Cloud Relay Anonymous Analytics

> **Status:** spec + implementation. **Source:** `specs/epics/30-relay-analytics/`.
> **Anchors:** Epic 25 (cloud relay — `cloud/`, `relay.Registry`, the
> proxy edge, `billing.Meter`), Epic 29 (the on-server watch analytics,
> for contrast: *that* is per-user on the home box; *this* is
> aggregate-only on the shared cloud relay).

## Goal

The cloud relay (`cloud/`, Epic 25) brokers traffic between home servers
and their clients. As an operator of that shared infrastructure, Hamza
Labs needs to answer fleet-level questions — *how many servers are
connected, how much bandwidth is flowing, how are pushes landing, where
are the users* — to run capacity, support, and billing. It must do so
**without ever collecting individual user data**, because the relay sees
traffic for households it does not own. This epic builds aggregate-only
observability with a GDPR posture baked in from the first write, not
bolted on.

The design rule that drives everything: **the relay metrics tables hold
no user id, no server id, and no IP address.** The only dimension is
*country*, derived once at the edge and immediately discarded with the
IP it came from. That is what makes the data "anonymous analytics"
rather than "surveillance of other people's media servers".

1. **Metrics collection** (30.1) — an in-process collector samples the
   connection gauges and accumulates traffic counters, flushing per-
   minute aggregate rows to Postgres; an hourly rollup compacts them and
   raw rows are purged after 24 h.
2. **GDPR compliance layer** (30.2) — server-id hashing, country-from-
   header (no IP storage), 90-day auto-purge, deletion-on-account-delete,
   a public `GET /privacy` policy endpoint, Article 30 processing
   records, and a DPA template.
3. **Relay admin dashboard** (30.3) — operator-only `GET
   /v1/admin/metrics/*` endpoints: counts, tunnel stats, bandwidth
   graphs, push stats, subscription breakdown, country heatmap.
4. **Metrics export** (30.4) — CSV/JSON export for operators and a
   Prometheus/OpenMetrics `/metrics` endpoint for Grafana.

## Design decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | **Aggregate-only schema: no `user_id`, no `server_id`, no IP in `relay_metrics_*`.** | The relay is shared infrastructure carrying other households' traffic. Storing per-entity rows would make it a PII honeypot. Counts and per-country sums answer every operator question without it. This is the structural GDPR guarantee — there is nothing to leak because nothing identifying is written. |
| D2 | **Country is derived from the edge header (`CF-IPCountry`) at request time and the IP is never persisted.** | Behind Cloudflare/an ALB the country is already resolved; we read the 2-letter code and drop the IP in the same breath. No GeoIP database, no IP column, no IP in logs. Unknown/anonymised codes (`XX`, `T1`) collapse to `''`. |
| D3 | **Two-tier storage: per-minute `relay_metrics_raw` (24 h TTL) → hourly `relay_metrics_hourly` (90 day TTL).** | Raw gives short-term resolution for live graphs; the hourly rollup is the durable, bounded series the dashboard reads. Raw purges at 24 h, hourly at 90 days (D5), so the table never grows without bound. |
| D4 | **Counters and gauges share one additive row shape `(bucket, metric, country, value, samples)`.** | A counter stores its summed delta with `samples` ignored; a gauge stores `sum(value)` over `samples` observations so the hourly average is `sum_value / samples`. One table, one upsert path, two read interpretations — no separate gauge table. |
| D5 | **Retention is enforced by an auto-purge goroutine, not a cron.** | The relay role already runs background loops (the meter flush); a 90-day purge tick co-located with the rollup keeps retention a property of the running binary, observable in tests, with no external scheduler to forget. |
| D6 | **Push-delivery and subscription metrics are *read* from the existing `push_dispatch_log` and `users` tables, not re-collected.** | Those facts already live in the cloud DB (Epic 16/push). The dashboard aggregates them on read; only the relay-traffic metrics (connections, bandwidth, requests) need fresh collection. Less duplication, one source of truth per fact. |
| D7 | **The collector keeps process-cumulative atomics *and* a reset-on-flush delta accumulator.** | Prometheus wants monotonic counters since process start (`/metrics`); the DB wants per-minute deltas. Maintaining both in the same `Record*` call keeps the two views consistent without deriving one from the other. |
| D8 | **Dashboard/export endpoints live on the *api* role; collection + `/metrics` + `/privacy` live on the *relay* role.** | The relay router has no user-auth stack (servers connect over WS; HTTP is proxied). The api role already has `RequireUser` + the email-domain admin gate. Both roles share the same Postgres, so the api role reads the rows the relay role writes. `/metrics` (scraped internally) and `/privacy` (public) need no auth and sit on the relay before its catch-all proxy. |

## Stories

| Story | Title | Surface |
|---|---|---|
| [30.1](story-30-01-relay-metrics-collection.md) | Relay metrics collection | `metrics.Collector` + `metrics.Runner`; `relay_metrics_*` (migration 00110001) |
| [30.2](story-30-02-gdpr-compliance.md) | GDPR compliance layer | `privacy` pkg; `GET /privacy`; Article 30 records; DPA template; purge + deletion |
| [30.3](story-30-03-relay-admin-dashboard.md) | Relay admin dashboard | `GET /v1/admin/metrics/{overview,bandwidth,push,subscriptions,geo}` |
| [30.4](story-30-04-metrics-export.md) | Metrics export | `GET /v1/admin/metrics/export`; `GET /metrics` (Prometheus) |

## Data model (migration 00110001)

```
relay_metrics_raw          -- per-minute aggregate, purged after 24 h
  id          bigserial pk
  bucket      timestamptz not null     -- minute bucket (UTC)
  metric      text        not null     -- metrics.Metric* name
  country     text        not null ''  -- ISO-3166 alpha-2 or '' (unknown)
  value       bigint      not null 0   -- counter sum, or gauge sample sum
  samples     integer     not null 0   -- gauge observation count (0 for counters)
  created_at  timestamptz not null now()
  unique (bucket, metric, country)

relay_metrics_hourly       -- hourly rollup, purged after 90 days
  hour        timestamptz not null
  metric      text        not null
  country     text        not null ''
  sum_value   bigint      not null 0
  max_value   bigint      not null 0
  samples     integer     not null 0
  primary key (hour, metric, country)
```

Metric names (`metrics/names.go`): `connected_servers` and
`active_tunnels` (gauges), `bandwidth_in_bytes`, `bandwidth_out_bytes`,
`requests`, `push_sent`, `push_failed` (counters). `requests` is the only
metric carrying a non-empty `country`.

## Out of scope (this batch)

- No per-request log retention of any kind — only minute aggregates.
- No client-side OpenTelemetry traces; `/metrics` is a pull endpoint only.
- The DPA is a **template** (`cloud/docs/dpa-template.md`), not an executed
  agreement.
- Real GeoIP resolution is delegated to the edge (`CF-IPCountry`); we ship
  no MaxMind database.
