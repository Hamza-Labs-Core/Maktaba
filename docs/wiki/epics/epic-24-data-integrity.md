# Epic 24 — Data Integrity

> **Status:** spec + plans complete. **Source:** `specs/epics/24-data-integrity/`.
> **Anchors:** [`architecture.md`](../../../specs/architecture.md) §3 (video state machine), §3.1 (content_hash identity), §7.1–7.6 (job state, claim loop, segment commit), §8.1 (core schema), §10.3 (ChromaDB single-writer).

## Goal

A user's media library and the platform's derived state survive crashes, power loss, partial writes, concurrent jobs, and operator mistakes. **The library is the durable truth;** derived data (transcripts, indexes, caches) is recoverable from it. Recovery is documented and tested. This epic covers atomicity, idempotency, consistency between authoritative state and derived state, backup/restore, durability of in-flight work, and integrity verification.

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 24.1 | [Atomic writes for sidecar artifacts](../../../specs/epics/24-data-integrity/story-24-01-atomic-writes.md) | [plan-24-01](../../../specs/epics/24-data-integrity/plan-24-01-atomic-writes.md) | Subtitles, sprites, thumbnails written via temp file + fsync + atomic rename; reaper sweeps `.tmp.*` orphans. |
| 24.2 | [Idempotent & resumable jobs](../../../specs/epics/24-data-integrity/story-24-02-idempotent-jobs.md) | [plan-24-02](../../../specs/epics/24-data-integrity/plan-24-02-idempotent-jobs.md) | Idempotency key `(content_hash, stage, backend, model, config_hash)` deduplicates re-claims; jobs resume from `last_segment_end_sec`; sidecars regenerate from DB. |
| 24.3 | [Database constraints](../../../specs/epics/24-data-integrity/story-24-03-database-constraints.md) | [plan-24-03](../../../specs/epics/24-data-integrity/plan-24-03-database-constraints.md) | FK / unique / CHECK constraints; soft-delete with partial unique indexes; SQLite `PRAGMA foreign_keys`; state-machine guards via WHERE clauses. |
| 24.4 | [Concurrency & locking](../../../specs/epics/24-data-integrity/story-24-04-concurrency-locking.md) | [plan-24-04](../../../specs/epics/24-data-integrity/plan-24-04-concurrency-locking.md) | Job claim via `SELECT … FOR UPDATE SKIP LOCKED`; watch-progress last-writer-wins (debounced); ChromaDB single-writer peer-detect; advisory locks per resource. |
| 24.5 | [Backup & restore](../../../specs/epics/24-data-integrity/story-24-05-backup-restore.md) | [plan-24-05](../../../specs/epics/24-data-integrity/plan-24-05-backup-restore.md) | Daily `pg_dump --format=custom` (Postgres) or `VACUUM INTO` (SQLite); 14-day retention; verify with `pg_restore --list`; CLI restore with `--confirm`. |
| 24.6 | [Disaster recovery](../../../specs/epics/24-data-integrity/story-24-06-disaster-recovery.md) | [plan-24-06](../../../specs/epics/24-data-integrity/plan-24-06-disaster-recovery.md) | Four documented scenarios (DB lost, DB+caches lost, media corrupted, binaries corrupted) with RTO/RPO; `make dr-drill` nightly CI; admin Restore UI. |
| 24.7 | [Integrity verification](../../../specs/epics/24-data-integrity/story-24-07-integrity-verification.md) | [plan-24-07](../../../specs/epics/24-data-integrity/plan-24-07-integrity-verification.md) | Weekly integrity doctor: re-verify content_hash (sample/full), sidecar presence, FK referential, FTS/Chroma parity; repair flag re-enqueues. |
| 24.8 | [Identity stability](../../../specs/epics/24-data-integrity/story-24-08-identity-stability.md) | [plan-24-08](../../../specs/epics/24-data-integrity/plan-24-08-identity-stability.md) | `content_hash = BLAKE3(first_4MiB ‖ last_4MiB ‖ size_le8)` (or whole file <8 MiB); rename/copy reuses hash; modify-in-place creates new row with `superseded_by`. |
| 24.9 | [Forward & backward compat](../../../specs/epics/24-data-integrity/story-24-09-forward-back-compat.md) | [plan-24-09](../../../specs/epics/24-data-integrity/plan-24-09-forward-back-compat.md) | Artifact formats carry `schema_version`; readers tolerate higher minor; cache keys include major version prefix for auto-invalidation. |

## Cross-cutting decisions

- **Atomic writes.** All sidecar outputs (subtitles VTT/SRT, sprites, thumbnails, segment JSON) follow `temp → fsync → atomic rename → fsync_dir`; fallback for non-atomic FS.
- **Idempotency keys.** Stored on `processing_jobs.idempotency_key`; tuple `(content_hash, stage, backend, model, config_hash)` prevents duplicate work across retries and bulk re-enqueues.
- **Soft delete + partial unique.** `deleted_at TIMESTAMPTZ NULL` plus `UNIQUE(...) WHERE deleted_at IS NULL` allows row resurrection.
- **Watch-progress concurrency.** Last-writer-wins without monotonicity; server-side debounce (1 s per (user, video) pair) collapses rapid client updates.
- **Advisory locks.** `pg_advisory_xact_lock(namespace, key)` for per-GPU and per-cache-eviction serialization; released on tx commit; reaper force-releases stale holders.
- **Backup strategy.** `pg_dump --format=custom --jobs=4 --compress=zstd:6` daily at 03:00; verify on completion; retain 14 days; restore with `--clean --if-exists`. Restore drill runs nightly in CI.
- **Single-writer ChromaDB.** Lock file with exclusive `flock` under `chroma_dir`; second writer process refused at boot with clear error.
- **Identity stability.** BLAKE3 sample hash makes move/rename free (only `videos.path` UPDATE; no re-process). Modify-in-place creates a new row linked via `videos.superseded_by`.
- **Forward-back compatibility.** Top-level `schema_version` on JSON artifacts; readers ignore unknown fields on minor bumps. Cache keys prefixed with major version (`v1:hls:<hash>`) so a major bump auto-invalidates.

## API endpoints introduced

- `POST /api/admin/backup/snapshot`, `maktaba-api backup` CLI (story 24.5)
- `POST /api/admin/recovery/<scenario>`, `WS /api/ws/recovery/<id>` (story 24.6)
- `GET /api/admin/integrity/reports` (story 24.7)
- `POST /api/watch-progress`, `POST /api/ws/recovery` (story 24.4)

## Migrations claimed

| Slot | Plan | Tables / changes |
|------|------|------------------|
| `0050` | plan-24-03 | System-wide constraints inventory + full enum validation; `videos.state` and `processing_jobs.state` CHECKs; FK `ON DELETE` clauses; soft-delete patterns. |
| `0051` | plan-24-02 | `processing_jobs.idempotency_key VARCHAR UNIQUE`. |
| `0052` | plan-24-04 | `advisory_locks(namespace, key, held_by_pid, acquired_at, heartbeat_at)` metadata. |
| `0053` | plan-24-05 | `backups(id, timestamp, size_bytes, dialect, status, checksum)`. |
| `0054` | plan-24-06 | `recovery_events(id, scenario_id, step, status, started_at, completed_at, error)`. |
| `0060` | plan-24-08 | `videos.superseded_by UUID NULL REFERENCES videos(id)`. |
| `0061` | plan-24-07 | `integrity_reports(id, library_id, run_timestamp, check_type, finding_count, passed)`. |

## Files & code paths

- `pipeline/src/maktaba_pipeline/media/{atomic_write,reaper}.py`
- `pipeline/src/maktaba_pipeline/pipeline/{idempotency,resume,sidecars,claim,advisory,chroma_lock}.py`
- `api/internal/db/errors.go`, `tools/constraint-lint.go`, `shared/db/constraints.md`
- `api/internal/http/watch_progress.go`
- `api/internal/backup/{backup,restore}.go`, `api/cmd/api/backup.go`
- `api/internal/recovery/scenarios.go`, `web/src/routes/admin/recovery.tsx`, `docs/operations/disaster-recovery.md`
- `pipeline/src/maktaba_pipeline/cli/integrity.py`, `integrity/checks/*.py`, `web/src/routes/admin/integrity.tsx`
- `pipeline/src/maktaba_pipeline/domain/identity.py`, `library/resolve.py`
- `pipeline/src/maktaba_pipeline/compat/schema_version.py`, `api/internal/cache/keys.go`

## Dependencies

- **Epic 1** (Scanner): `content_hash` identity, video state machine (stories 1.2, 1.5, 1.6).
- **Epic 6** (Job Queue): `processing_jobs` base schema, claim loop, heartbeat (stories 6.1–6.3).
- **Epic 22** (DevOps): migration discipline, schema versioning (stories 22.4, 22.6).
- **Epic 21** (Observability): `audit_log` for integrity findings and backups (story 21.6).

## Out of scope

- Media volume backup strategy (operators use existing rsync / ZFS / Time Machine).
- ChromaDB server-mode deployment (deferred; v1 single-writer embedded only).
- Distributed backup replication (single-host backup target assumed).
- Password-protected encrypted backups (v1 trusts local directory).
- Per-tenant backup quotas (multi-tenant deferred).
- Hardware-level RAID / HA (outside platform scope).

## See also

- [Epic 22 — DevOps](epic-22-devops.md) (migrations, upgrade & rollback).
- [Epic 21 — Observability](epic-21-observability.md) (audit log).
- [Epic 19 — Scalability](epic-19-scalability.md) (concurrency caps and ChromaDB single-writer).
- [Migrations catalog](../migrations.md).
- [Glossary](../glossary.md) — atomic write, idempotency key, advisory lock, sidecar, fsync, restore window, RPO, RTO, content_hash, partial unique index, soft delete, schema_version, integrity doctor.
