# Story 50.17 — A stateless API pool

> Epic 50 · Phase 2 · Depends on: 50.08 · Needed by: 50.18

## Why

In Phase 1 the api and worker roles run on the one active node, and the standby starts them on
failover. In Phase 2 the relays move to their own pool (50.16), and the API should be able to
run on several machines at once, so losing one is a non-event and adding one is a config change.
Three things stop that today: the worker's jobs would run on every node at once, the rate
limiter is per process, and caches have no way to hear about changes made on another node.

## Description

### 1. Layout

```
apps ── api.maktaba.tv (Cloudflare proxy) ── Hetzner LB11 (HTTP, health check /readyz)
                                                ├── api-1  (api + worker)
                                                ├── api-2  (api + worker)
                                                └── api-3  (api + worker)
                                                        │ private network
                                                   Postgres (Phase 1 pair, then 50.18)
```

- **LB11** (€7.49, README §5): HTTP service on port 443 to the nodes' :8443 over the private
  network, health check `GET /readyz` every 5 s, 2 failures to remove, least connections.
  Cloudflare's proxy sits in front (API traffic isn't video, so Cloudflare's terms allow it;
  research §3). The LB's 1 TB traffic limit is plenty for JSON.
- `app.maktaba.tv` and `admin.maktaba.tv` go through the same LB.
- Sessions are already stateless: 1 h RS256 JWT access tokens and refresh tokens in Postgres
  (architecture §13.6). Any node can serve any request.
- The real client IP: nodes trust `CF-Connecting-IP` only when the request comes from the LB's
  private address, and the LB only accepts Cloudflare's IP ranges (Hetzner firewall).
- **Updated by 50.31:** the LB takes no public traffic. `cloudflared` runs on two of the API
  nodes and sends the Tunnel's traffic to the LB's private address; the LB's public interface
  is off. The nodes' `server.trusted_proxies` is the LB's private address. The admin listener
  (50.32) sits behind a second LB service on its own port.

### 2. The worker runs once

The worker (`cloud/cmd/maktaba-cloud/role_worker.go`) runs `cleanupExpired` every 10 min and
`purgeDeletedAccounts` every hour, plus metering roll-ups. On every API node the worker
starts, but works only while it holds a **Postgres session advisory lock**,
`pg_try_advisory_lock(8472613)` (one more than the migrator's `8472612` in
`cloud/migrations/README.md`), on a dedicated connection:

- try every 15 s; whoever gets it runs the loops;
- if its connection drops, Postgres releases the lock and another node takes it within 15 s;
- every job stays idempotent (they already are: deletes by `expires_at`, purges by hold date),
  so a job interrupted half way is simply run again.

Metric `worker_leader{node}`; alert if no node holds it for 2 min.

### 3. Rate limits

`cloud/internal/ratelimit` is an in-memory token bucket per process. With round robin across
three nodes, one IP would get three times its limit. Decision:

| Limit | Where it lives | Why |
|---|---|---|
| Sign-in, password reset, mail, claim redeem (`role_api.go` lines ~201–205) | **Postgres**, table `cloud_rate_buckets(key, tokens, updated_at)` with one `UPSERT … RETURNING` per request | They guard against brute force, so the limit must be global. These paths are low volume (a few per second at most), so a Postgres write is cheap. |
| Everything else (general per-IP abuse limits) | stays in memory per node | Coarse limits; three times a generous limit is still a limit; Cloudflare's own rate limiting sits in front. |
| Account lockout (10 fails / 15 min) | already Postgres | unchanged |

Redis stays a cache only (README §6, 50.05): no limit depends on it.

### 4. Caches hear about changes

Caches added in 50.05 (home lookup, tier) and the existing per-node LRUs need to drop entries
when another node changes them. The writer sends `NOTIFY cloud_cache, '<kind>:<id>'` in the
same transaction as the change (tier change from the billing webhook, server revoke, suspend,
slug change). Each node keeps one dedicated `LISTEN cloud_cache` connection (a direct
connection, not through PgBouncer's transaction pool, see 50.18) and drops the entry. If the
LISTEN connection drops, the node clears the whole cache on reconnect. TTLs stay as the backstop
(60 s, architecture §13.10).

## User-facing strings

None new.

## Acceptance criteria

- **Given** three API nodes behind the LB,
  **when** one is stopped,
  **then** the LB removes it within 10 s and no client request fails after that (in-flight ones
  on the stopped node finish during its graceful shutdown).
- **Given** three nodes running the worker,
  **when** the 10-minute cleanup is due,
  **then** it runs on exactly one node.
- **Given** the worker leader is killed,
  **when** 15 s pass,
  **then** another node holds the lock and runs the jobs.
- **Given** an IP tries sign-in 31 times in a minute spread across the three nodes,
  **when** the 31st arrives,
  **then** it is refused `429`, as on one node.
- **Given** a plan change is applied on node 1,
  **when** node 2 serves the next request for that home,
  **then** node 2 uses the new tier (within 1 s, not 60 s).
- **Given** node 2's LISTEN connection drops,
  **when** it reconnects,
  **then** its caches are emptied.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration | 3 workers on one test Postgres | run 30 min of fake clock | each job ran once per period |
| T02 | integration | worker leader connection killed | wait | lock moves ≤ 15 s |
| T03 | integration | 3 api processes, Postgres limiter | 31 sign-ins round robin | 31st refused |
| T04 | integration | 2 api processes | change tier via webhook on one | other sees it ≤ 1 s |
| T05 | integration | LISTEN connection dropped | reconnect | cache cleared |
| T06 | rehearsal (CI) | 50.06 `deploy-blue-green` with 3 api nodes behind an HAProxy standing in for LB11 | rolling deploy | 0 failed API requests |

## Edge cases

- **LB11 itself.** A Hetzner managed component; if it fails, the runbook points
  `api.maktaba.tv`'s Cloudflare origin at one node's IP directly.
- **Cloudflare down.** Grey-cloud `api.maktaba.tv` (DNS only) to the LB as a runbook step.
- **Clock skew between nodes.** JWT checks allow 30 s leeway; chrony everywhere.
- **Lock held by a hung node.** A hung process keeps its connection; the node's health check
  fails and the LB removes it, but the lock stays. The worker loop renews a heartbeat row every
  15 s; a heartbeat older than 60 s lets another node `pg_terminate_backend` the holder.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/cmd/maktaba-cloud/role_worker.go` | Advisory-lock leadership around the loops; heartbeat. |
| `cloud/internal/db/leader.go` (new) | `TryLead(ctx, id)` on a dedicated connection. |
| `cloud/internal/ratelimit/pg.go` (new), `cloud/cmd/maktaba-cloud/role_api.go` | Postgres limiter for the auth/claim paths. |
| `cloud/migrations/<next>_rate_buckets.sql` (new) | `cloud_rate_buckets`, worker heartbeat table (expand only). |
| `cloud/internal/cache/notify.go` (new) | LISTEN/NOTIFY invalidation; used by 50.05's caches. |
| `cloud/internal/handlers/billing/`, `cloud/internal/handlers/admin/`, `cloud/internal/handlers/servers/` | `NOTIFY` on tier, suspend, revoke, slug change. |
| `cloud/internal/middleware/` | Trusted client IP from the LB/Cloudflare. |
| `deploy/cloud-ha/setup.sh` | `--role api`: Caddy (with Proxy Protocol from the LB) and the api/worker units, no Postgres. |
| `deploy/cloud-ha/README.md` | The owner's LB settings. |

TLS choice: the LB runs in **TCP mode with Proxy Protocol** to Caddy on each API node, so TLS
and certificates stay as in Phase 1 (50.02) and the LB never holds a key.

### Tests

- Integration tests above in `cloud/` (run by `relay.yml`).
- **CI only:** `deploy-blue-green` with three api nodes.

### Rollout

1. Ship the code (leadership, limiter, NOTIFY): it works the same on one node.
2. The owner creates LB11 and three API nodes; `setup.sh --role api` on each.
3. Point Cloudflare's origin for `api.maktaba.tv` at the LB; stop the api/worker roles on the
   Phase 1 pair.

### Rollback

Point Cloudflare's origin back at the Floating IP and start the api/worker roles on the Phase 1
leader again. The code changes are safe on one node, so they stay.

### Dependencies

50.08 (the deploy flow the pool is deployed with). Needed by 50.18.
