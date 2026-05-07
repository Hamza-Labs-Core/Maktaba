# Epic 19 — Scalability

> **Status:** spec + plans complete. **Source:** `specs/epics/19-scalability/`.
> **Anchors:** [`architecture.md` §10](../../../specs/architecture.md) (capacity), §11 (scale axes), §11.3 (concurrency defaults).

## Goal

Maktaba serves the 30 TB / single-household target on one box without falling over, **and the same code paths scale horizontally** to multi-host deployments without architectural rewrites. Each service has an explicit scale axis; bottlenecks are detected by load test, not production incident. Capacity floors are asserted in CI. This epic does not cover speed of any single request (that's [Epic 18](epic-18-performance.md)) — it covers *capacity*: how many videos, segments, sessions, and concurrent users a deployment can hold.

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 19.1 | [Single-host capacity floor](../../../specs/epics/19-scalability/story-19-01-single-host-capacity.md) | [plan-19-01](../../../specs/epics/19-scalability/plan-19-01-single-host-capacity.md) | Mac mini M2 holds 50 k videos / 1 M segments; 8 direct-play or 4 transcoded sessions; mixed workload. `make capacity`. |
| 19.2 | [API scale-out](../../../specs/epics/19-scalability/story-19-02-api-scale-out.md) | [plan-19-02](../../../specs/epics/19-scalability/plan-19-02-api-scale-out.md) | Stateless API N replicas; WS fan-out via Postgres NOTIFY + `events` table; rolling restart via drain. |
| 19.3 | [Streaming scale-out](../../../specs/epics/19-scalability/story-19-03-streaming-scale-out.md) | [plan-19-03](../../../specs/epics/19-scalability/plan-19-03-streaming-scale-out.md) | Sticky-session by `session_id` hash; failover via clean reopen from server-side watch state; `EvictHashCache` gRPC fan-out. |
| 19.4 | [Pipeline scale-out](../../../specs/epics/19-scalability/story-19-04-pipeline-scale-out.md) | [plan-19-04](../../../specs/epics/19-scalability/plan-19-04-pipeline-scale-out.md) | N workers via `SELECT … FOR UPDATE SKIP LOCKED`; per-GPU advisory locks; ChromaDB single-writer. |
| 19.5 | [Database scaling & failover](../../../specs/epics/19-scalability/story-19-05-database-scaling.md) | [plan-19-05](../../../specs/epics/19-scalability/plan-19-05-database-scaling.md) | Streaming replica; daily backup; restore drill; migration safety lint for long DDL. |
| 19.6 | [Storage scaling](../../../specs/epics/19-scalability/story-19-06-storage-scaling.md) | [plan-19-06](../../../specs/epics/19-scalability/plan-19-06-storage-scaling.md) | 30 TB cold scan ≤30 min, RSS ≤800 MiB; BLAKE3 `content_hash` from first/last 4 MiB + size; debounced watcher. |
| 19.7 | [Concurrency caps & quotas](../../../specs/epics/19-scalability/story-19-07-concurrency-caps.md) | [plan-19-07](../../../specs/epics/19-scalability/plan-19-07-concurrency-caps.md) | Per-host CPU/GPU/transcode caps; per-library USD budget; observable via `/api/system/health`; hot-reloadable. |
| 19.8 | [Multi-tenant readiness](../../../specs/epics/19-scalability/story-19-08-multi-tenant-readiness.md) | [plan-19-08](../../../specs/epics/19-scalability/plan-19-08-multi-tenant-readiness.md) | v1 single-user; schema allows flag-flip to multi-user without migration. Sentinel UUID + `library_acl`. |

## Capacity floor (Story 19.1)

- **Hardware reference:** Mac mini M2 16 GB / 30 TB SSD.
- **Library:** 50 000 videos, 1 000 000 segments.
- **Concurrent streams:** 8 direct-play *or* 4 transcoded.
- **Mixed workload:** 8 streams + 1 transcribe + 100 search qps over 30 min, error rate ≤0.1 %.
- **Cold scan:** 30 TB tree ≤30 min, RSS ≤800 MiB.
- **SQLite:** documented at 1/4 capacity (12 k videos, 250 k segments).

## Scale axes

| Service | Axis | Coordination |
|---|---|---|
| API | Stateless replicas behind L7 LB | Session state in Postgres; in-process ring buffer for fast path; WS fan-out via NOTIFY + `events` table |
| Streaming | Sticky session by `session_id` hash | Consistent-hash LB; clean reopen on replica loss; `EvictHashCache` gRPC fan-out |
| Pipeline | N workers across hosts | `SELECT … FOR UPDATE SKIP LOCKED`; `pg_advisory_xact_lock(host, device)` for per-GPU; `pg_try_advisory_lock('chroma:writer')` for ChromaDB single-writer |
| Database | Primary + read replica | Async streaming repl; route reads to replica if lag ≤5 s, else primary; alert if lag >60 s |
| Storage | Bounded-memory cold scan | DFS iterative; debounced watcher (2 s); BLAKE3 sample hash for identity stability |

## Key technical decisions

- **Stateless API.** No in-memory session state durable across replica failure beyond a ~5 min ring buffer fast-path. Transparent rolling restarts via drain mode.
- **Sticky streaming.** Streaming replicas pinned by session_id via consistent-hash LB. Failover is clean reopen (`410 Gone session_invalidated`), not failover; watch position preserved server-side.
- **Pipeline single-flight via SKIP LOCKED.** Workers across hosts coordinate via Postgres; exactly-once. GPU jobs serialize via per-device advisory locks. ChromaDB single-writer enforced via `pg_try_advisory_lock('chroma:writer')`; second worker falls back to read-only (multi-writer is deferred).
- **Database as event bus.** Postgres LISTEN/NOTIFY is the WS-event delivery for multi-replica fan-out. Payload ≤8 KiB; larger events stored in `events` table. Clients reconnect via `last_event_id` cursor → durably replay (cross-replica safe). 7-day retention; monotonic `BIGSERIAL`.
- **Concurrency caps tunable at runtime.** `transcode.max_concurrent` defaults to `max(1, num_cores/4)`, auto-derived at boot. Hot-reload from `system_config` (poll 30 s; no restart). Library USD budget cap enforced at job-claim time; over-budget jobs requeued for next month; in-progress jobs never preempted.
- **Migration safety lint.** Pre-merge CI blocks long-running DDL (e.g., `CREATE INDEX` without CONCURRENTLY on tables >10 k rows). All migrations tested against 1 M-segment fixture (≤60 s wall-clock).
- **Multi-tenant readiness without migration.** All user-scoped rows have `user_id NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'`. Single-user mode resolves all requests to that sentinel; admin token also resolves to it. Flag flip + backfill of `library_acl` rows enables multi-user. Flip is **irreversible** in v1.

## Migrations claimed by this epic

| Slot range | Plan | Tables / changes |
|---|---|---|
| `00xx` events table | plan-19-02 | `events(id BIGSERIAL, channel, payload, user_id, library_id, created_at)`; 7-day retention pruner. |
| `00xx` streaming replicas | plan-19-03 | `streaming_replicas(id, host, grpc_port, advertise_url, drain, last_seen)`, `streaming_sessions.replica_id` FK. |
| `00xx` pipeline scale-out | plan-19-04 | `processing_jobs` adds `worker_id`, `heartbeat_at`, `attempt`, `backend`, `model_hash`, `last_segment_end_sec`; partial index on `(state, heartbeat_at)`. |
| `00xx` library budgets | plan-19-07 | `library_budgets(library_id, max_usd_per_month, used_usd, period_start)`. |
| `00xx` multi-tenant | plan-19-08 | Backfill `user_id` on user-bearing tables; `library_acl(library_id, user_id, role)` with partial unique index; CHECK forbidding sentinel collision. |

## Dependencies

- **Stories 18.1, 18.2, 18.3** — budgets must hold at scale.
- **Epic 6** (Job Queue): `processing_jobs` claim path, state machine.
- **Epic 5** (Search): FTS + Chroma single-writer rule (also documented in [Story 24.4](epic-24-data-integrity.md)).
- **Epic 9** (Library Management): libraries, collections, watch_state schemas (used in 19.8 backfill).
- **Epic 7** (API): in-process ring buffer (Story 7.16), WS hub, `/healthz` drain mode.
- **Story 21.2** (Metrics): Prometheus surface for queue depth, replica lag, concurrency.
- **Story 23.1** (Auth): admin token bypass mapping to sentinel user.

## Out of scope

- Multi-writer ChromaDB (deferred; v1 single embedded writer).
- Automated primary failover (manual; runbook documented).
- Global CDN / edge caching.
- Sharded database (single primary + optional read replica).
- Cross-region deployment.
- Multi-tenant billing & per-tenant limits (19.8 lays schema only).

## See also

- [Epic 18 — Performance](epic-18-performance.md) (per-request latency).
- [Epic 24 — Data Integrity](epic-24-data-integrity.md) (concurrency and locking).
- [Glossary](../glossary.md) — scale axis, capacity floor, stateless service, sticky session, drain mode, advisory lock, replica lag, budget cap, sentinel user.
