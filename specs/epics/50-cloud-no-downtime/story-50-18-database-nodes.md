# Story 50.18 — The database on its own nodes

> Epic 50 · Phase 2 · Depends on: 50.09, 50.17 · Needed by: —

## Why

In Phase 1 Postgres shares nodes A and B with Caddy, the relay and the cloud processes. That is
fine at 1,000 homes. In Phase 2 the relays and the API have their own pools (50.16, 50.17), and
a busy relay shouldn't slow down the database, nor should a database failover move the
relay's public address. Three database nodes also let us keep a synchronous replica while
one node is down for maintenance, so RPO stays 0 through planned work.

## Description

### 1. Layout

| Node | Plan (README §5) | Role |
|---|---|---|
| db-1, db-2, db-3 | CCX13 (dedicated vCPU) in FSN1, NBG1, HEL1 | Patroni: one primary, one **synchronous** replica, one async replica |
| etcd | on the three DB nodes (3 members) | the witness W keeps monitoring only |

Patroni settings carry over from 50.07, with `synchronous_node_count: 1` (one of the two
replicas confirms each commit; Patroni picks the healthy one).

### 2. How apps find the primary

A **Floating IP on the primary**, moved by the same `on-role-change` callback as 50.07 (now
without starting cloud processes). Apps connect to `db.internal` = the Floating IP's private
alias, through PgBouncer.

Alternative weighed: HAProxy on each app node that asks Patroni's REST API (`GET /primary`)
which node is primary. Rejected for now: one more moving part on every app node; the Floating
IP gives the same result with what we already run. Kept as the fallback if the Hetzner API is
unreliable.

### 3. PgBouncer

PgBouncer runs on each DB node next to Postgres (the Floating IP lands on the primary's
PgBouncer).

- **Pool mode `transaction`** for normal traffic. The cloud uses `database/sql` with
  `github.com/lib/pq` (`cloud/go.mod`) and no explicit `Prepare` calls (checked 2026-11-03);
  lib/pq's placeholder queries use unnamed statements, which work in transaction mode.
  PgBouncer ≥ 1.21 with `max_prepared_statements = 200` covers any named statements added later.
- **Session pool** (a second PgBouncer database entry, `cloud_session`) for the connections that
  need a session: the worker's **advisory lock** (50.17), the **LISTEN** connection for cache
  invalidation (50.17), and `migrate up` (the migrator's `pg_advisory_lock(8472612)`).
  `cloud/internal/config` gets `db.session_url` for these.
- `default_pool_size` 20 per database/user, `max_client_conn` 2,000.

### 4. Moving from Phase 1 with no downtime

1. Create db-1..3 (the owner); `setup.sh --role db`.
2. Add db-1 and db-2 to the **existing** Patroni cluster as replicas (they stream from A).
3. `switchover` to db-1 (≤ 15 s pause in writes; the relay rides it out, 50.05). The Floating
   IP for the database moves with it.
4. Point the cloud's `db.url` at PgBouncer on `db.internal`, by a deploy (50.08).
5. Add db-3. Move etcd to db-1..3 one member at a time (add a new member, remove an old one).
   Then stop Patroni and Postgres on A and B; they drop out of the cluster.
6. pgBackRest (50.09) runs on the new leader; take a new full backup at once.

## User-facing strings

None.

## Acceptance criteria

- **Given** three DB nodes,
  **when** a commit returns,
  **then** it is on at least one replica.
- **Given** one replica is down for maintenance,
  **when** writes continue,
  **then** they still wait for the other replica (RPO stays 0).
- **Given** the primary dies,
  **when** 45 s pass,
  **then** the synchronous replica is primary, the database Floating IP points at it, and the
  API and relays write again without a config change.
- **Given** the worker and the LISTEN connection,
  **when** they connect,
  **then** they use the session pool, and the advisory lock and notifications work through
  PgBouncer.
- **Given** the Phase 1 pair is serving,
  **when** the migration steps in §4 run,
  **then** no request fails apart from writes held ≤ 15 s at the switchover, and no commit is lost.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration | cloud tests against PgBouncer in transaction mode | full `relay.yml` integration suite | passes |
| T02 | integration | worker + LISTEN through session pool | lock, notify | works; transaction pool refuses LISTEN as expected |
| T03 | rehearsal (CI) | 50.06 `failover-kill-leader`, 3 DB nodes | kill primary | sync replica promoted ≤ 45 s; 0 lost commits |
| T04 | rehearsal (CI) | `switchover` | Phase 1 → DB nodes per §4 | 0 failed requests; writes held ≤ 15 s |
| T05 | rehearsal (CI) | `restore-pitr` on the new cluster | as 50.09 | passes |

## Edge cases

- **A replica takes too long to catch up** after a rebuild: it joins as async until caught up;
  Patroni only makes caught-up replicas synchronous.
- **PgBouncer down on the primary**: its health check fails; Patroni doesn't know about it, so a
  `pgbouncer_down` alert pages (50.10). Runbook: restart it, or switchover.
- **Long transactions** pin a transaction-mode server connection; `idle_in_transaction_session_timeout = 60s`
  on Postgres.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `deploy/cloud-ha/setup.sh` | `--role db`. |
| `deploy/cloud-ha/pgbouncer/pgbouncer.ini` (new) | §3. |
| `deploy/cloud-ha/patroni/patroni.yml` | `synchronous_node_count`, three members. |
| `deploy/cloud-ha/bin/on-role-change` | DB-only mode: move the DB Floating IP, start nothing else. |
| `deploy/cloud-ha/bin/migrate-db-nodes` (new) | §4 as guarded steps. |
| `cloud/internal/config/config.go`, `cloud/internal/db/pool.go` | `db.session_url` and a session pool for locks, LISTEN and migrations. |
| `deploy/cloud-ha/monitoring/alerts.yml` | `pgbouncer_down`, sync replica missing. |

### Tests

- Integration suite through PgBouncer in `relay.yml` (a PgBouncer service container).
- **CI only:** `failover-kill-leader`, `switchover` (the §4 migration), `restore-pitr` with
  three DB nodes.

### Rollout

1. Ship the session-URL code (it defaults to `db.url`, so nothing changes on Phase 1).
2. The owner creates the three nodes; run §4, announced on the status page as maintenance.

### Rollback

Until A and B are removed (§4 step 5): switchover back to A and point `db.url` back. After
that: the cluster is the rollback; restore from backup (50.09) is the last resort.

### Dependencies

50.09 (backups follow the leader), 50.17 (session-pool users: the worker lock and LISTEN).
