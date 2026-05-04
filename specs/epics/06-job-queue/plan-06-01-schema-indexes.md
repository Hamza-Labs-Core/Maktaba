# Implementation Plan — Story 6.1 Schema, Migration, Indexes

> Companion to [story-06-01-schema-indexes.md](story-06-01-schema-indexes.md).
> The story states *what* and *why*; this plan states *how*.
> The schema follows [architecture.md §7.1](../../architecture.md), and
> the canonical stage enum is owned by
> [Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0002_processing_jobs.sql` (Postgres) and `0002_processing_jobs.sqlite.sql` (SQLite). |
| Numbering | Slot `0002` per the canonical [migration manifest](../../../shared/db/migrations/MANIFEST.md): `processing_jobs` is a foundation table that ships immediately after slot `0001` (libraries + videos). Earlier plans (notably plan-01-01's enqueue path) declare a hard dependency on this slot landing first. The four indexes ship in the same file. |
| Helper code | `pipeline/src/maktaba_pipeline/db/jobs.py` — async helpers backed by `asyncpg` (Postgres) and `aiosqlite` (SQLite); a thin `enqueue` function lives here. |
| Notify trigger | Postgres `AFTER INSERT` trigger on `processing_jobs` emits `pg_notify('jobs.new', payload::text)`. SQLite has no LISTEN/NOTIFY → a Python-side fanout shim publishes to an in-process `asyncio.Queue` keyed by channel name. |
| Out of scope | The claim loop (Story 6.2), heartbeat (6.3), pause/resume (6.4), retries (6.5), reaper (6.6). This story stops at "row exists; one notify fires; uniqueness holds." |

## 1. Architecture diagram

```
                   ┌──────────────────────────────────────────┐
                   │  Caller: scanner / API / reprocess job   │
                   │  (Story 1.x → enqueue scan/probe;        │
                   │   Story 1.6 → enqueue downstream stages) │
                   └─────────────────┬────────────────────────┘
                                     │ enqueue(video_id, stage, priority, payload)
                                     ▼
        ┌─────────────────────────────────────────────────────────────┐
        │  pipeline/db/jobs.py :: enqueue()                           │
        │                                                             │
        │   1. Look up video.updated_at (for skip-when-done logic)    │
        │   2. INSERT INTO processing_jobs (video_id, stage, ...)     │
        │      ON CONFLICT (video_id, stage)                          │
        │        WHERE state IN (live states) DO NOTHING              │
        │   3. If conflict, fall back to:                             │
        │        SELECT id FROM processing_jobs                       │
        │         WHERE video_id=? AND stage=?                        │
        │           AND state IN (live OR done-but-stale)             │
        │   4. Return job_id; commit transaction                      │
        └─────────────────┬───────────────────────────────────────────┘
                          │
                          ▼
        ┌─────────────────────────────────────────────────────────────┐
        │  Postgres trigger:                                          │
        │    AFTER INSERT ON processing_jobs                          │
        │    → pg_notify('jobs.new',                                  │
        │                json_build_object(                           │
        │                  'id', NEW.id,                              │
        │                  'video_id', NEW.video_id,                  │
        │                  'stage', NEW.stage,                        │
        │                  'priority', NEW.priority)::text)           │
        │                                                             │
        │  SQLite: same payload published via the in-process          │
        │  PubsubBus (db/pubsub.py) which Story 6.2's claim loop      │
        │  subscribes to.                                             │
        └─────────────────┬───────────────────────────────────────────┘
                          │ NOTIFY 'jobs.new' { id, video_id, stage, priority }
                          ▼
        ┌─────────────────────────────────────────────────────────────┐
        │  Subscribers:                                                │
        │   - claim loop in every Pipeline worker (Story 6.2)         │
        │   - API → WebSocket fanout (Epic 2 Story 2.5)                │
        └─────────────────────────────────────────────────────────────┘
```

The four indexes are sized to the four query shapes that touch
`processing_jobs`:

```
       ┌─────────────────────────────────────────────────────────┐
       │ Query shape                            Index             │
       ├─────────────────────────────────────────────────────────┤
       │ Claim next eligible job                #1: (state, priority, not_before) │
       │ "What's pending for video X?"          #2: (video_id, stage)            │
       │ Reaper: stale claimed/running rows     #3: partial on (state, last_heartbeat_at) │
       │ Pause poller: rows asking to pause     #4: partial on pause_requested = true     │
       └─────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/migrations/0002_processing_jobs.sql` | Postgres migration: table, all 4 indexes, partial unique index for liveness, CHECK constraints, NOTIFY trigger. |
| `shared/db/migrations/0002_processing_jobs.sqlite.sql` | SQLite variant (no partial CHECK on enums; no trigger; type swaps). |
| `pipeline/src/maktaba_pipeline/db/jobs.py` | `enqueue()`, `get_job()`, type stubs `Job`, `JobState`, `Stage`. |
| `pipeline/src/maktaba_pipeline/db/pubsub.py` | Channel-name constants (`JOBS_NEW = 'jobs.new'`, etc.) and the in-process bus shim used in SQLite mode. |
| `pipeline/tests/db/test_jobs_enqueue.py` | Unit tests for §6 below. |
| `pipeline/tests/db/test_jobs_migration.py` | Schema-introspection tests (indexes, CHECKs, partial-unique). |
| `shared/db/queries/jobs.sql` | sqlc input — Go-side `EnqueueJob`, `GetJobByID` for the API surface (Stories 6.4/6.9). |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/db/__init__.py` | Re-export `enqueue`, `Job`, `JobState`. |
| `pipeline/pyproject.toml` | Add `asyncpg>=0.29` and `aiosqlite>=0.20` if not yet pinned. |
| `api/internal/db/queries.sql.go` | Regenerate from `shared/db/queries/jobs.sql` via `sqlc`. |
| `specs/epics/06-job-queue/README.md` | Tick story 6.1 once landed. |

### 2.3 Type definitions (canonical)

```python
# pipeline/src/maktaba_pipeline/db/jobs.py
from __future__ import annotations
from dataclasses import dataclass
from datetime import datetime
from enum import StrEnum
from typing import Any
from uuid import UUID


class JobState(StrEnum):
    PENDING   = "pending"
    CLAIMED   = "claimed"
    RUNNING   = "running"
    PAUSED    = "paused"
    RESUMING  = "resuming"
    DONE      = "done"
    FAILED    = "failed"
    CANCELLED = "cancelled"


# Single source of truth for the stage list — also CHECK'd in the migration.
# Mirrors Epic 1 Story 1.6 + the "subtitle_gen" addition resolved in Epic 6 README.
class Stage(StrEnum):
    SCAN         = "scan"
    PROBE        = "probe"
    EXTRACT      = "extract"
    TRANSCRIBE   = "transcribe"
    SUBTITLE_GEN = "subtitle_gen"
    INDEX        = "index"
    THUMBNAIL    = "thumbnail"


LIVE_STATES: frozenset[JobState] = frozenset({
    JobState.PENDING, JobState.CLAIMED, JobState.RUNNING,
    JobState.RESUMING, JobState.PAUSED,
})

TERMINAL_STATES: frozenset[JobState] = frozenset({
    JobState.DONE, JobState.FAILED, JobState.CANCELLED,
})


@dataclass(slots=True, frozen=True)
class Job:
    id: int
    video_id: UUID
    stage: Stage
    state: JobState
    priority: int
    attempts: int
    max_attempts: int
    claimed_by: str | None
    claimed_at: datetime | None
    last_heartbeat_at: datetime | None
    not_before: datetime | None
    error: str | None
    total_duration_seconds: float | None
    processed_seconds: float
    segments_completed: int
    last_segment_end_sec: float
    estimated_remaining_sec: float | None
    realtime_factor: float | None
    progress_updated_at: datetime | None
    pause_requested: bool
    cancel_requested: bool
    paused_at: datetime | None
    paused_at_sec: float | None
    paused_reason: str | None
    resumed_at: datetime | None
    resume_count: int
    metrics: dict[str, Any] | None
    payload: dict[str, Any] | None
    created_at: datetime
    finished_at: datetime | None


@dataclass(slots=True, frozen=True)
class EnqueueResult:
    """What enqueue() returns — id plus an outcome the caller can log."""
    id: int
    outcome: str   # 'inserted' | 'reused' | 'skipped_done_unchanged'
```

The `payload` JSONB column is added beyond architecture §7.1 to carry
stage-specific options (e.g., the `extract` track index, the `transcribe`
backend pin) without polluting the schema; Story 6.2 reads it back into
the worker's stage handler.

### 2.4 Function signatures

```python
# pipeline/src/maktaba_pipeline/db/jobs.py
async def enqueue(
    db: DBConn,
    *,
    video_id: UUID,
    stage: Stage,
    priority: int = 100,
    payload: dict[str, Any] | None = None,
    max_attempts: int = 3,
) -> EnqueueResult: ...


async def get_job(db: DBConn, job_id: int) -> Job | None: ...


async def list_pending_for_video(db: DBConn, video_id: UUID) -> list[Job]: ...
```

`DBConn` is the project's existing typed wrapper around `asyncpg.Connection`
or `aiosqlite.Connection`; the same call site works against both.

## 3. Database migration — Postgres

`shared/db/migrations/0002_processing_jobs.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- 1. The table itself, mirroring architecture §7.1 verbatim plus the
--    `payload` JSONB and the `progress_updated_at` column already in §7.1.
CREATE TABLE processing_jobs (
    id                       BIGSERIAL PRIMARY KEY,
    video_id                 UUID NOT NULL
                              REFERENCES videos(id) ON DELETE CASCADE,
    stage                    TEXT NOT NULL,
    state                    TEXT NOT NULL DEFAULT 'pending',
    priority                 INT  NOT NULL DEFAULT 100,
    attempts                 INT  NOT NULL DEFAULT 0,
    max_attempts             INT  NOT NULL DEFAULT 3,
    claimed_by               TEXT,
    claimed_at               TIMESTAMPTZ,
    last_heartbeat_at        TIMESTAMPTZ,
    not_before               TIMESTAMPTZ,
    error                    TEXT,

    total_duration_seconds   REAL,
    processed_seconds        REAL NOT NULL DEFAULT 0,
    segments_completed       INT  NOT NULL DEFAULT 0,
    last_segment_end_sec     REAL NOT NULL DEFAULT 0,
    estimated_remaining_sec  REAL,
    realtime_factor          REAL,
    progress_updated_at      TIMESTAMPTZ,

    pause_requested          BOOLEAN NOT NULL DEFAULT false,
    cancel_requested         BOOLEAN NOT NULL DEFAULT false,
    paused_at                TIMESTAMPTZ,
    paused_at_sec            REAL,
    paused_reason            TEXT,
    resumed_at               TIMESTAMPTZ,
    resume_count             INT  NOT NULL DEFAULT 0,

    metrics                  JSONB,
    payload                  JSONB,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at              TIMESTAMPTZ,

    -- Stage enum CHECK — the canonical list lives here and is the single
    -- source of truth for what stages exist. Adding a stage requires a
    -- new migration that ALTERs this constraint.
    CONSTRAINT processing_jobs_stage_chk CHECK (
        stage IN (
            'scan', 'probe', 'extract', 'transcribe',
            'subtitle_gen', 'index', 'thumbnail'
        )
    ),

    CONSTRAINT processing_jobs_state_chk CHECK (
        state IN (
            'pending', 'claimed', 'running', 'paused',
            'resuming', 'done', 'failed', 'cancelled'
        )
    ),

    CONSTRAINT processing_jobs_priority_chk CHECK (priority >= 0),
    CONSTRAINT processing_jobs_attempts_chk CHECK (attempts >= 0
                                            AND attempts <= max_attempts + 1),

    -- Story 6.10 invariant: resume offset never goes negative or beyond
    -- the known total duration. Tightened here, not in a separate migration,
    -- because anyone who creates the table without it has already broken
    -- the invariant.
    CONSTRAINT processing_jobs_resume_offset_chk CHECK (
        last_segment_end_sec >= 0
        AND last_segment_end_sec <= COALESCE(total_duration_seconds,
                                             last_segment_end_sec)
    )
);

-- 2. The four canonical indexes per §7.1.

-- (a) Claim index — exactly the columns the claim loop's WHERE filters by.
--     `priority ASC, id ASC` order matches the claim's ORDER BY (Story 6.2).
CREATE INDEX processing_jobs_claim_idx
    ON processing_jobs (state, priority, not_before);

-- (b) "What's pending for this video" — used by the API's per-video status
--     view and by the scanner's "is anything in flight?" check.
CREATE INDEX processing_jobs_video_stage_idx
    ON processing_jobs (video_id, stage);

-- (c) Reaper's partial index — only live-claim states matter. Partial keeps
--     it tiny: a 1M-row table with 99% terminal entries indexes ~10K rows.
CREATE INDEX processing_jobs_reaper_idx
    ON processing_jobs (state, last_heartbeat_at)
    WHERE state IN ('claimed', 'running', 'resuming');

-- (d) Pause poller's partial index — only rows asking to be paused.
CREATE INDEX processing_jobs_pause_pending_idx
    ON processing_jobs (pause_requested)
    WHERE pause_requested = true;

-- 3. Liveness uniqueness — at most one non-terminal job per (video, stage).
--    The unique partial index is what makes `enqueue` idempotent without a
--    SELECT-then-INSERT race window.
CREATE UNIQUE INDEX processing_jobs_one_live_per_video_stage
    ON processing_jobs (video_id, stage)
    WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused');

-- 4. NOTIFY trigger — fires `jobs.new` on every insert. The payload is the
--    minimum the claim loop and the API/WS need. Bigger payloads (full
--    Job row) belong on the API side reading the row by id.
CREATE OR REPLACE FUNCTION processing_jobs_notify_new() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'jobs.new',
        json_build_object(
            'id',       NEW.id,
            'video_id', NEW.video_id,
            'stage',    NEW.stage,
            'priority', NEW.priority
        )::text
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER processing_jobs_notify_new_trg
    AFTER INSERT ON processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION processing_jobs_notify_new();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS processing_jobs_notify_new_trg ON processing_jobs;
DROP FUNCTION IF EXISTS processing_jobs_notify_new();
DROP TABLE IF EXISTS processing_jobs;
-- +goose StatementEnd
```

### 3.1 Migration — SQLite variant

`shared/db/migrations/0002_processing_jobs.sqlite.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE processing_jobs (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id                 TEXT NOT NULL
                              REFERENCES videos(id) ON DELETE CASCADE,
    stage                    TEXT NOT NULL,
    state                    TEXT NOT NULL DEFAULT 'pending',
    priority                 INTEGER NOT NULL DEFAULT 100,
    attempts                 INTEGER NOT NULL DEFAULT 0,
    max_attempts             INTEGER NOT NULL DEFAULT 3,
    claimed_by               TEXT,
    claimed_at               TEXT,                  -- ISO-8601
    last_heartbeat_at        TEXT,
    not_before               TEXT,
    error                    TEXT,

    total_duration_seconds   REAL,
    processed_seconds        REAL NOT NULL DEFAULT 0,
    segments_completed       INTEGER NOT NULL DEFAULT 0,
    last_segment_end_sec     REAL NOT NULL DEFAULT 0,
    estimated_remaining_sec  REAL,
    realtime_factor          REAL,
    progress_updated_at      TEXT,

    pause_requested          INTEGER NOT NULL DEFAULT 0,  -- bool
    cancel_requested         INTEGER NOT NULL DEFAULT 0,
    paused_at                TEXT,
    paused_at_sec            REAL,
    paused_reason            TEXT,
    resumed_at               TEXT,
    resume_count             INTEGER NOT NULL DEFAULT 0,

    metrics                  TEXT,                  -- JSON
    payload                  TEXT,
    created_at               TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    finished_at              TEXT,

    CHECK (stage IN ('scan','probe','extract','transcribe',
                     'subtitle_gen','index','thumbnail')),
    CHECK (state IN ('pending','claimed','running','paused',
                     'resuming','done','failed','cancelled')),
    CHECK (priority >= 0),
    CHECK (attempts >= 0 AND attempts <= max_attempts + 1),
    CHECK (last_segment_end_sec >= 0
           AND last_segment_end_sec <= COALESCE(total_duration_seconds,
                                                last_segment_end_sec))
);

CREATE INDEX processing_jobs_claim_idx
    ON processing_jobs (state, priority, not_before);

CREATE INDEX processing_jobs_video_stage_idx
    ON processing_jobs (video_id, stage);

CREATE INDEX processing_jobs_reaper_idx
    ON processing_jobs (state, last_heartbeat_at)
    WHERE state IN ('claimed', 'running', 'resuming');

CREATE INDEX processing_jobs_pause_pending_idx
    ON processing_jobs (pause_requested)
    WHERE pause_requested = 1;

CREATE UNIQUE INDEX processing_jobs_one_live_per_video_stage
    ON processing_jobs (video_id, stage)
    WHERE state IN ('pending','claimed','running','resuming','paused');

-- No trigger here — SQLite has no LISTEN/NOTIFY. The Python helper publishes
-- on the in-process PubsubBus after the INSERT commits.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS processing_jobs;
-- +goose StatementEnd
```

SQLite supports partial indexes since 3.8.0; both indexes (c) and (d)
work the same. The `BOOLEAN`-typed columns become `INTEGER` (0/1).

## 4. Python code scaffolding

`pipeline/src/maktaba_pipeline/db/pubsub.py`:

```python
"""Channel-name constants and the SQLite in-process pubsub shim.

Postgres callers use `LISTEN/NOTIFY` directly; SQLite callers route
through `PubsubBus.publish(channel, payload)` and subscribers `await
bus.subscribe(channel)`. The constants are shared so a refactor of a
channel name only happens here.
"""
from __future__ import annotations

import asyncio
import json
from collections import defaultdict
from typing import Any


# Canonical channel names — never inline a string elsewhere.
JOBS_NEW         = "jobs.new"
JOBS_FLAG_SET    = "jobs.flag_set"
JOBS_PROGRESS    = "jobs.progress"
JOBS_HEARTBEAT   = "jobs.heartbeat"
JOBS_REAPED      = "jobs.reaped"
JOBS_FORCE_PAUSE = "jobs.force_pause"


class PubsubBus:
    """In-process fanout for SQLite (and tests). One bus per process."""

    def __init__(self) -> None:
        self._subs: dict[str, list[asyncio.Queue[str]]] = defaultdict(list)

    def publish(self, channel: str, payload: dict[str, Any]) -> None:
        text = json.dumps(payload, separators=(",", ":"), default=str)
        for q in self._subs[channel]:
            q.put_nowait(text)

    async def subscribe(self, channel: str) -> asyncio.Queue[str]:
        q: asyncio.Queue[str] = asyncio.Queue()
        self._subs[channel].append(q)
        return q


_BUS: PubsubBus | None = None


def get_bus() -> PubsubBus:
    global _BUS
    if _BUS is None:
        _BUS = PubsubBus()
    return _BUS
```

`pipeline/src/maktaba_pipeline/db/jobs.py` (the enqueue function):

```python
import json
from typing import Any
from uuid import UUID

from .pubsub import JOBS_NEW, get_bus


_INSERT_SQL_PG = """
INSERT INTO processing_jobs
       (video_id, stage, state, priority, payload, max_attempts)
VALUES ($1, $2, 'pending', $3, $4::jsonb, $5)
ON CONFLICT (video_id, stage)
   WHERE state IN ('pending','claimed','running','resuming','paused')
DO NOTHING
RETURNING id
"""

_FALLBACK_LIVE_SQL_PG = """
SELECT id FROM processing_jobs
 WHERE video_id = $1
   AND stage    = $2
   AND state IN ('pending','claimed','running','resuming','paused')
 LIMIT 1
"""

_DONE_ROW_SQL_PG = """
SELECT pj.id, pj.finished_at, v.updated_at
  FROM processing_jobs pj
  JOIN videos v ON v.id = pj.video_id
 WHERE pj.video_id = $1
   AND pj.stage    = $2
   AND pj.state    = 'done'
 ORDER BY pj.finished_at DESC
 LIMIT 1
"""


async def enqueue(
    db,
    *,
    video_id: UUID,
    stage: Stage,
    priority: int = 100,
    payload: dict[str, Any] | None = None,
    max_attempts: int = 3,
) -> EnqueueResult:
    """Insert a row in `pending`, or return the existing live row's id.

    Idempotency rules:
      - Live row exists → return its id, outcome='reused'.
      - Done row exists and the source video is unchanged
        (videos.updated_at <= done.finished_at) → outcome='skipped_done_unchanged'.
      - Otherwise (no live row, or done but source changed) → INSERT.
    """
    payload_text = json.dumps(payload) if payload is not None else None

    async with db.transaction():
        # 1. Try to insert. The unique partial index decides if this is a no-op.
        row = await db.fetchrow(
            _INSERT_SQL_PG, video_id, stage.value, priority,
            payload_text, max_attempts,
        )
        if row is not None:
            new_id = row["id"]
            # Trigger fires NOTIFY 'jobs.new' for Postgres; SQLite needs us
            # to publish manually (the trigger doesn't exist there).
            if db.dialect == "sqlite":
                get_bus().publish(JOBS_NEW, {
                    "id": new_id, "video_id": str(video_id),
                    "stage": stage.value, "priority": priority,
                })
            return EnqueueResult(id=new_id, outcome="inserted")

        # 2. Conflict on the unique-live partial index → reuse.
        live = await db.fetchrow(_FALLBACK_LIVE_SQL_PG, video_id, stage.value)
        if live is not None:
            return EnqueueResult(id=live["id"], outcome="reused")

        # 3. No live row but conflict raised — must be a done row whose
        #    state moved to 'done' between (1) and (2). Fall back to the
        #    done-source-unchanged check.
        done = await db.fetchrow(_DONE_ROW_SQL_PG, video_id, stage.value)
        if done is not None and done["updated_at"] <= done["finished_at"]:
            return EnqueueResult(id=done["id"],
                                 outcome="skipped_done_unchanged")

        # 4. Done row but the source has changed since the last run → insert
        #    a fresh pending row. The unique-live index permits this because
        #    'done' is not in the live set.
        row = await db.fetchrow(
            _INSERT_SQL_PG, video_id, stage.value, priority,
            payload_text, max_attempts,
        )
        if row is None:
            # Truly impossible by construction; defensive raise.
            raise RuntimeError("enqueue: lost both insert races")
        return EnqueueResult(id=row["id"], outcome="inserted")
```

The SQLite path uses `BEGIN IMMEDIATE` (the project's `db.transaction()`
helper switches mode based on dialect) so two concurrent enqueues
serialize cleanly under WAL.

## 5. Go side — query surface for the API

`shared/db/queries/jobs.sql` (sqlc input — Go code-gen for the API):

```sql
-- name: EnqueueJob :one
-- The Go API's "process now" endpoint goes through this. The Python helper
-- in §4 is the system of record for the idempotency rules; this query
-- intentionally only does the simplest insert. The API layer calls into
-- the pipeline service over gRPC (architecture §3.6) for the full enqueue.
INSERT INTO processing_jobs (video_id, stage, priority, payload)
VALUES ($1, $2, $3, $4)
ON CONFLICT (video_id, stage)
   WHERE state IN ('pending','claimed','running','resuming','paused')
DO NOTHING
RETURNING id;

-- name: GetJobByID :one
SELECT * FROM processing_jobs WHERE id = $1;

-- name: ListJobsByVideo :many
SELECT * FROM processing_jobs
 WHERE video_id = $1
 ORDER BY created_at DESC;
```

The Go-side wrapper:

```go
// api/internal/jobs/enqueue.go
package jobs

import (
    "context"
    "encoding/json"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgtype"

    "maktaba/api/internal/db"
)

type EnqueueRequest struct {
    VideoID  uuid.UUID
    Stage    string
    Priority int32
    Payload  any
}

type EnqueueResult struct {
    ID      int64
    Outcome string // "inserted" | "reused"
}

// Enqueue is the API-side façade. It calls EnqueueJob and, if the unique
// partial index swallowed the insert, re-fetches the live row. Mirrors
// the Python helper's first two branches; the "skipped_done_unchanged"
// branch is intentionally absent here — the Go side never enqueues
// re-runs of `done` rows. Those flow through the Pipeline gRPC.
func Enqueue(ctx context.Context, q *db.Queries, r EnqueueRequest) (EnqueueResult, error) {
    payload, err := json.Marshal(r.Payload)
    if err != nil {
        return EnqueueResult{}, err
    }
    var jb pgtype.JSONB
    if err := jb.Scan(payload); err != nil {
        return EnqueueResult{}, err
    }

    id, err := q.EnqueueJob(ctx, db.EnqueueJobParams{
        VideoID:  r.VideoID,
        Stage:    r.Stage,
        Priority: r.Priority,
        Payload:  jb,
    })
    if err == nil {
        return EnqueueResult{ID: id, Outcome: "inserted"}, nil
    }
    // ON CONFLICT ... DO NOTHING returns ErrNoRows on conflict.
    if !errors.Is(err, pgx.ErrNoRows) {
        return EnqueueResult{}, err
    }
    existing, err := q.FindLiveJob(ctx, db.FindLiveJobParams{
        VideoID: r.VideoID, Stage: r.Stage,
    })
    if err != nil {
        return EnqueueResult{}, err
    }
    return EnqueueResult{ID: existing.ID, Outcome: "reused"}, nil
}
```

(`FindLiveJob` is a sister sqlc query; omitted for brevity but follows
`_FALLBACK_LIVE_SQL_PG`.)

## 6. Test plan

### 6.1 Migration tests (`pipeline/tests/db/test_jobs_migration.py`)

| Test | What it pins |
|---|---|
| `test_migration_creates_table` | After `goose up 0010`, `processing_jobs` exists with the documented column count and types (introspect via `information_schema.columns`). |
| `test_migration_creates_all_four_indexes` | Query `pg_indexes WHERE tablename='processing_jobs'` (SQLite: `sqlite_master WHERE type='index'`); assert all four index names and the `processing_jobs_one_live_per_video_stage` unique partial. |
| `test_migration_creates_notify_trigger` | Postgres only: `pg_trigger` row exists with name `processing_jobs_notify_new_trg`. |
| `test_migration_down_then_up` | `goose down 0010 && goose up 0010` round-trip leaves no orphan trigger/function/index. |
| `test_stage_check_constraint_rejects_thumb` | INSERT with `stage='thumb'` raises CHECK violation; `stage='thumbnail'` succeeds. |
| `test_stage_check_constraint_accepts_subtitle_gen` | INSERT with `stage='subtitle_gen'` succeeds (regression for the canonical-enum addition). |
| `test_state_check_constraint_rejects_unknown` | INSERT with `state='walking'` raises CHECK violation. |
| `test_resume_offset_check_constraint` | INSERT with `last_segment_end_sec=-1` raises; `last_segment_end_sec=10, total_duration_seconds=5` raises (>total). |

### 6.2 `enqueue` tests (`pipeline/tests/db/test_jobs_enqueue.py`)

| Test | What it pins |
|---|---|
| `test_enqueue_inserts_pending_row` | Single call → row with `state='pending'`, `attempts=0`, `not_before=NULL`, `priority=100` default. Returns `outcome='inserted'`. |
| `test_enqueue_idempotent_returns_same_id` | Two calls with same `(video_id, stage)` → second returns `outcome='reused'` and same id; only one row in DB. |
| `test_enqueue_concurrent_idempotent` | 8 asyncio tasks call enqueue against the same pair; exactly one row created; all eight return the same id; `inserted` count == 1. |
| `test_enqueue_skips_when_done_and_source_unchanged` | Insert a `done` row with `finished_at = now()`; do not bump `videos.updated_at`; enqueue → `outcome='skipped_done_unchanged'`, no new row. |
| `test_enqueue_creates_new_when_source_changed` | Same as above but `UPDATE videos SET updated_at = now() + interval '1s'`; enqueue → `outcome='inserted'`, new row exists alongside the done row. |
| `test_enqueue_payload_round_trips` | Payload `{"audio_index": 1, "track_uuid": "abc"}` survives in `payload` JSONB and reads back equal. |
| `test_enqueue_emits_jobs_new_notify` | (Postgres) `LISTEN jobs.new`; one enqueue → exactly one notification with payload `{id, video_id, stage, priority}`. |
| `test_enqueue_emits_jobs_new_pubsub_sqlite` | (SQLite) Subscribe to `PubsubBus.subscribe(JOBS_NEW)`; enqueue → one event with the same payload shape. |
| `test_enqueue_invalid_stage_raises` | enqueue with a freeform stage string (not in the enum) raises and inserts no row. |

### 6.3 Cross-dialect parity

`pipeline/tests/db/test_jobs_dialect_parity.py` uses the parametrized
fixture pattern that Story 1.5's plan introduces (`@pytest.mark.parametrize
("dialect", ["postgres", "sqlite"])`). Every test in §6.2 runs once per
dialect. Divergences (e.g., partial-index syntax) get a single `xfail`
row, not a quiet skip.

### 6.4 Performance gate

`test_enqueue_throughput_baseline` — 10 000 enqueues against an empty
table, single connection, no notifies subscribed: must complete in
≤ 5 s on the standard CI runner (≈ 2000 enqueue/s, comfortably above
the worst-case scanner rate of ~50 enqueue/s).

## 7. Test code scaffolding

```python
# pipeline/tests/db/test_jobs_enqueue.py
import asyncio
import json
from uuid import uuid4

import pytest

from maktaba_pipeline.db.jobs import enqueue, Stage
from maktaba_pipeline.db.pubsub import JOBS_NEW, get_bus


@pytest.mark.asyncio
async def test_enqueue_inserts_pending_row(db, video):
    res = await enqueue(db, video_id=video.id, stage=Stage.PROBE)
    assert res.outcome == "inserted"

    row = await db.fetchrow(
        "SELECT * FROM processing_jobs WHERE id=$1", res.id,
    )
    assert row["state"] == "pending"
    assert row["attempts"] == 0
    assert row["not_before"] is None
    assert row["priority"] == 100


@pytest.mark.asyncio
async def test_enqueue_idempotent_returns_same_id(db, video):
    a = await enqueue(db, video_id=video.id, stage=Stage.PROBE)
    b = await enqueue(db, video_id=video.id, stage=Stage.PROBE)

    assert a.id == b.id
    assert a.outcome == "inserted"
    assert b.outcome == "reused"

    n = await db.fetchval(
        "SELECT count(*) FROM processing_jobs "
        "WHERE video_id=$1 AND stage='probe'", video.id,
    )
    assert n == 1


@pytest.mark.asyncio
async def test_enqueue_concurrent_idempotent(db, video):
    async def go() -> tuple[int, str]:
        r = await enqueue(db, video_id=video.id, stage=Stage.PROBE)
        return r.id, r.outcome

    results = await asyncio.gather(*(go() for _ in range(8)))
    ids = {r[0] for r in results}
    inserted = sum(1 for r in results if r[1] == "inserted")
    reused = sum(1 for r in results if r[1] == "reused")

    assert len(ids) == 1
    assert inserted == 1
    assert reused == 7


@pytest.mark.asyncio
async def test_enqueue_skips_when_done_and_source_unchanged(db, video):
    # Stand up a done row.
    await db.execute(
        "INSERT INTO processing_jobs (video_id, stage, state, finished_at) "
        "VALUES ($1, 'probe', 'done', now())", video.id,
    )
    res = await enqueue(db, video_id=video.id, stage=Stage.PROBE)
    assert res.outcome == "skipped_done_unchanged"


@pytest.mark.asyncio
async def test_enqueue_creates_new_when_source_changed(db, video):
    await db.execute(
        "INSERT INTO processing_jobs (video_id, stage, state, finished_at) "
        "VALUES ($1, 'probe', 'done', now() - interval '1 hour')", video.id,
    )
    await db.execute(
        "UPDATE videos SET updated_at = now() WHERE id = $1", video.id,
    )
    res = await enqueue(db, video_id=video.id, stage=Stage.PROBE)
    assert res.outcome == "inserted"

    n = await db.fetchval(
        "SELECT count(*) FROM processing_jobs WHERE video_id=$1 AND stage='probe'",
        video.id,
    )
    assert n == 2


@pytest.mark.asyncio
async def test_enqueue_emits_jobs_new_notify_pg(pg_db, video):
    received: list[dict] = []
    conn = await pg_db.acquire_listener()
    await conn.add_listener(JOBS_NEW,
        lambda *args: received.append(json.loads(args[-1])))

    res = await enqueue(pg_db, video_id=video.id, stage=Stage.PROBE)
    await asyncio.sleep(0.05)  # let the trigger fire and the listener wake

    assert len(received) == 1
    note = received[0]
    assert note == {
        "id":       res.id,
        "video_id": str(video.id),
        "stage":    "probe",
        "priority": 100,
    }


@pytest.mark.asyncio
async def test_enqueue_emits_pubsub_sqlite(sqlite_db, video):
    bus = get_bus()
    queue = await bus.subscribe(JOBS_NEW)
    res = await enqueue(sqlite_db, video_id=video.id, stage=Stage.PROBE)

    note = json.loads(await asyncio.wait_for(queue.get(), timeout=1.0))
    assert note["id"] == res.id
    assert note["stage"] == "probe"
```

## 8. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Two callers race on the same `(video_id, stage)` | Unique partial index makes the loser's INSERT a no-op; `enqueue` falls back to the SELECT branch and returns the same id. | `test_enqueue_concurrent_idempotent` |
| Stage spelled `'thumb'` instead of `'thumbnail'` | Rejected by CHECK; INSERT raises `CheckViolation`. The Python `Stage` enum prevents this in the application layer; the CHECK is defense in depth. | `test_stage_check_constraint_rejects_thumb` |
| `subtitle_gen` not in earlier draft | Added to the canonical CHECK list; the README's "stage list addition" entry references this. | `test_stage_check_constraint_accepts_subtitle_gen` |
| Done row exists but source video changed (mtime/size update) | New pending row inserted alongside the done row; the unique-live partial index allows this because `done` is not in the live set. | `test_enqueue_creates_new_when_source_changed` |
| Done row + source unchanged | enqueue returns the existing done row id with `outcome='skipped_done_unchanged'`; the caller can decide whether to surface this in the UI. | `test_enqueue_skips_when_done_and_source_unchanged` |
| Caller passes `payload=None` | Stored as SQL NULL, reads back as `None`. No JSON serialization of `null` literal. | `test_enqueue_payload_round_trips` (variant) |
| SQLite under WAL with two writers | `BEGIN IMMEDIATE` taken inside `enqueue` serializes the writes; readers continue without blocking. | The dialect-aware `db.transaction()` helper (covered by Story 1.1's plan §3) |
| LISTEN connection died before NOTIFY fires | Postgres queues the notification on the trigger side; the listener reconnect path (Story 6.2) re-subscribes and receives any pending notifications via a `SELECT id FROM processing_jobs WHERE state='pending' AND created_at > $last_seen` re-sync query. | Story 6.2 owns the listener; this story owns the trigger fire only. |
| `videos` row deleted while a job is pending | `ON DELETE CASCADE` removes the job row. Stories 6.4 (cancel) and 6.6 (reaper) tolerate the absence. | FK constraint |
| Migration applied twice | `goose` tracks applied migrations in `goose_db_version`; double-apply is a no-op. The bare SQL would error on duplicate index/constraint names, which is the desired behaviour outside the migration runner. | Migration runner contract |

## 9. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `asyncpg` | ≥ 0.29 | Native LISTEN/NOTIFY support, the cleanest way to subscribe to `jobs.new` from Python. |
| `aiosqlite` | ≥ 0.20 | Mirrors asyncpg's API for tests against the SQLite dialect. |
| `goose` (Go) | already in repo | Migration runner. The `-- +goose Up/Down` framing is the existing project convention. |
| `sqlc` | dev-only | Generates `EnqueueJob`, `GetJobByID`, `ListJobsByVideo`, `FindLiveJob` for the API. |

No new heavy deps. The notify trigger is plain `pg_notify`; no extension required.

## 10. Acceptance checklist

Before this story is marked done:

**Migration**
- [ ] `shared/db/migrations/0002_processing_jobs.sql` applies cleanly on a fresh Postgres schema; `goose down` reverts cleanly.
- [ ] `shared/db/migrations/0002_processing_jobs.sqlite.sql` applies cleanly on a fresh SQLite schema.
- [ ] All four indexes from architecture §7.1 are present (introspection test passes).
- [ ] The unique partial index `processing_jobs_one_live_per_video_stage` exists.
- [ ] The CHECK constraints for `stage`, `state`, `priority`, `attempts`, and `last_segment_end_sec` reject the negative cases listed in §6.1.
- [ ] The `processing_jobs_notify_new_trg` trigger fires on every insert (Postgres).

**Code**
- [ ] `pipeline/src/maktaba_pipeline/db/jobs.py` exposes `enqueue`, `get_job`, `list_pending_for_video`, `Job`, `JobState`, `Stage`, `EnqueueResult`.
- [ ] `pipeline/src/maktaba_pipeline/db/pubsub.py` defines all six channel-name constants and a working `PubsubBus`.
- [ ] `shared/db/queries/jobs.sql` generates `EnqueueJob`, `GetJobByID`, `ListJobsByVideo`, `FindLiveJob` via sqlc.

**Behaviour (story acceptance criteria)**
- [ ] AC: enqueue returns the existing id when a live row exists.
- [ ] AC: enqueue is a no-op when a done row exists and `videos.updated_at <= finished_at`.
- [ ] AC: enqueue creates a fresh row when `videos.updated_at > finished_at`.
- [ ] AC: every successful insert triggers exactly one `jobs.new` notification with the documented payload.
- [ ] All `test_enqueue_*` and `test_migration_*` cases pass on both dialects.

**Observability**
- [ ] INFO log line `enqueued_job video_id=… stage=… priority=… outcome=…` is structured (matches the JSON shape in `pipeline/observability.py`).
- [ ] Counter `maktaba_jobs_enqueued_total{stage, outcome}` is exported (used by Story 6.9's `/metrics`).

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.1.
- [ ] The canonical channel-name table in the README links to `pipeline/db/pubsub.py` for the constants.
