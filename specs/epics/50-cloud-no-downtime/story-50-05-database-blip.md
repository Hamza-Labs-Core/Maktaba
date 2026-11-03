# Story 50.05 — The relay rides out a database blip

> Epic 50 · Phase 1 · Depends on: — · Needed by: 50.07

## Why

For **every** relayed request the relay asks Postgres twice: `Servers.BySlug` (which home this
subdomain is, and its plan) and `Meter.Allow` (this month's bytes against the cap)
(`cloud/internal/handlers/relay/proxy.go`, `cloud/internal/billing/meter.go`). A Patroni
failover makes Postgres unavailable for up to ~30–60 s (50.07). During that time every request
fails, even for homes whose tunnel is perfectly fine. Video players request a segment every few
seconds, so a 30-second blip stops every remote stream.

The relay already has everything it needs in memory to keep serving homes it has seen. This
story makes it use that, and writes down what Redis is for (very little).

## Description

### 1. Caches on the relay

- **Home cache.** `BySlug` results in an LRU of **50,000 entries, 5-minute life**. The relay
  serves from the cache and refreshes in the background when an entry is over 60 s old (so a
  normal plan change shows within a minute). On a database error it keeps serving the cached
  entry, however old, for homes it has served in the last hour.
- **Month total cache.** `Meter.Allow` reads the persisted month total once per home per
  **60 s** and adds the in-memory delta (the meter already keeps the delta). During a database
  outage it uses the last total it read plus the delta: a home near its cap can go slightly
  over, never more than the bytes of the outage. The Pro cap is 100 GB a month; a few minutes of
  one stream is a few hundred MB.
- **Invalidation.** The billing webhook handler (story 25.14; payment provider TBD) already plans `NOTIFY
  tier_changed`; nothing listens yet. The relay `LISTEN`s on `tier_changed` and `server_changed`
  (suspension, revocation, slug change) and drops the affected entries at once. Suspension and
  revocation therefore apply within a second while the database is up.
- **Unknown homes** (not in the cache) during an outage get `503 server_offline`, as today: the
  relay can't tell who they are.
- **Revoked while the database is down.** A revocation made just before the outage may be missed
  for at most the outage's length. Acceptable: revocation is also pushed down the tunnel
  (`0x20 REVOKE`, architecture §13.4) by the API.

### 2. Metering buffered in memory

The meter flushes every 30 s. When a flush fails it keeps the counts and retries every 5 s,
summing new bytes into the same buckets. The buffer is bounded at **1 million (server, bucket)
entries** (~100 MB); past that it merges the oldest buckets into one per server, so bytes are
never dropped, only coarsened. After the database is back the first flush writes everything.
On `SIGTERM` the meter's final flush (already up to 10 s) waits up to 30 s if the database is
down, then logs the unflushed total as `relay_meter_lost_bytes`.

### 3. Readiness during an outage

`/readyz` today answers `503 db_unreachable` when Postgres doesn't answer
(`cloud/internal/server/router.go`). For the relay role that would take the relay out of
rotation exactly when it can still serve. New behaviour, relay role only:

| Database | `/readyz` | `/healthz` |
|---|---|---|
| up, migrations at head | `200 ready` | `200` |
| unreachable for < 5 min, cache warm | `200 degraded` with `"db":"unreachable"` | `200` |
| unreachable for ≥ 5 min | `503 db_unreachable` | `200` |
| migrations behind | `503 migrations_behind` | `200` |

The `api` and `worker` roles keep today's strict check. Patroni's own checks decide which node
leads (50.07); this readiness only tells Caddy and the probes whether the relay serves.

### 4. Redis holds nothing we can't lose

An audit of `cloud/` on 2026-11-03:

| Where | What |
|---|---|
| `cloud/internal/config/config.go` | `RedisConfig`, `MAKTABA_CLOUD_REDIS_URL`: read, **never used** by any code. |
| `cloud/internal/ratelimit/ratelimit.go` | In memory per process; says "We do not back this with Redis in v1". |
| `cloud/migrations/00050001_bandwidth.sql` | Comment: current-month totals "hit Redis". **Not true**: the meter is in memory + Postgres. |
| `cloud/README.md`, `cloud/docs/accounts-billing-admin.md`, `cloud/configs/cloud.example.toml` | Docs and the example URL. |
| `deploy/cloud-relay/docker-compose.yml` | Runs Redis with AOF; nothing connects. |

The rule from now on, written into `cloud/README.md`:

> **Redis is a cache.** Anything in it can vanish at any time without losing a user's data or a
> byte of metering. Durable state lives in Postgres. Each node runs its own Redis; there is no
> Redis replication or Sentinel.

Because nothing uses Redis yet, Phase 1 keeps it only for the rate limiter's later move (25.24
"Redis sliding window") and drops AOF (`--appendonly no`, `--save ""`). If Phase 2 needs shared
rate limits across relays, they go in each node's Redis with per-node limits, or in Postgres;
never as a source of truth. The stale comment in `00050001_bandwidth.sql` is fixed.

## Acceptance criteria

- **Given** home H was served a minute ago,
  **when** Postgres becomes unreachable and a request for H arrives,
  **then** the relay serves it from its caches, with no database call on the request's path.
- **Given** Postgres is unreachable for 60 s while H streams,
  **when** it comes back,
  **then** every byte H used during the outage is in `bandwidth` within 35 s of recovery.
- **Given** a home never seen by this relay process,
  **when** a request arrives during the outage,
  **then** the relay answers `503 server_offline`.
- **Given** the billing webhook changes H's plan,
  **when** `NOTIFY tier_changed` fires,
  **then** the relay applies H's new plan to the next request (within 1 s).
- **Given** Postgres has been unreachable for 2 minutes,
  **when** the relay's `/readyz` is checked,
  **then** it answers `200` with `"status":"degraded","db":"unreachable"`; after 5 minutes,
  `503 db_unreachable`.
- **Given** a flush buffer at its bound,
  **when** more bytes arrive,
  **then** older buckets are merged and the total per server stays exact.
- **Given** Redis is stopped,
  **when** the relay and API run,
  **then** nothing fails and no metric or row is lost.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | home cache, fake clock | fetch, advance 61 s | served from cache; one background refresh |
| T02 | unit | home cache, lookup erroring | fetch an entry 10 min old | served (seen within the hour) |
| T03 | unit | meter, flush erroring for 60 s | record bytes, then let flush succeed | totals equal the sum recorded |
| T04 | unit | meter buffer bound 100 (test value) | 150 buckets | merged; per-server totals exact |
| T05 | integration | relay + scratch Postgres | `pg_ctl stop -m immediate` for 60 s while a simulated home streams | 0 failed requests for the known home; totals exact after restart |
| T06 | integration | `NOTIFY tier_changed` | change plan in DB | next request uses the new cap |
| T07 | integration | readyz | stop DB 2 min, then 6 min | `200 degraded`, then `503` |
| T08 | integration | Redis container stopped | run the relay tests | all pass |
| T09 | rehearsal (CI) | 50.06 `db-blip` | Patroni switchover under load | 0 failed requests for known homes; metering exact |

## Edge cases

- **A plan downgrade during an outage** is applied when the database is back (at most minutes
  late).
- **Cache poisoning by slug reuse.** A slug released and re-claimed (25.22's 30-day grace) sends
  `server_changed`; the cache entry is keyed by slug and checked against `server_id` on the
  tunnel, so a mismatch drops it.
- **Memory.** 50,000 cached homes × ~1 KB = ~50 MB, well inside a 4 GB node.
- **Clock jumps.** Cache ages use the monotonic clock (`cloud/internal/clock`).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/handlers/relay/cache.go` (new) | LRU home cache with refresh-ahead and stale-on-error. |
| `cloud/internal/handlers/relay/proxy.go` | Use the cache for `lookup()`. |
| `cloud/internal/billing/meter.go` | Month-total cache in `Allow`; buffered, bounded, merging flush; longer final flush. |
| `cloud/internal/stores/notify.go` (new) | `LISTEN tier_changed, server_changed` with reconnect. |
| `cloud/internal/handlers/billing/` (the webhook handler) and `cloud/internal/handlers/admin/` | `NOTIFY` on plan, suspension, revocation and slug changes (check each already does; add where missing). |
| `cloud/internal/server/router.go` | Relay-role readiness with `degraded`. |
| `cloud/migrations/00050001_bandwidth.sql` | Fix the Redis comment. |
| `cloud/README.md` | The Redis rule. |
| `deploy/cloud-relay/docker-compose.yml` | Redis without persistence. |
| `cloud/internal/metrics/` | `relay_cache_hits_total{result}`, `relay_db_unreachable_seconds`, `meter_buffered_entries`, `relay_meter_lost_bytes`. |

### Tests

- T01–T08 in the cloud module (`relay.yml` test job; T05–T07 use the Postgres service
  container).
- **CI only:** rehearsal scenario **`db-blip`** (50.06), and the database part of
  `failover-kill-leader`.

### Rollout

Ships on its own; useful even on the single VPS (a Postgres restart no longer stops streams).

### Rollback

`relay.cache.enabled = false` and `relay.readyz_strict = true` restore today's behaviour.

### Dependencies

None.
