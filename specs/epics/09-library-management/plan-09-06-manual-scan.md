# Plan 9.6 — Manual scan trigger and scan progress — implementation

> Implementation plan for [story-09-06-manual-scan.md](story-09-06-manual-scan.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: depends on the watcher contract from
> [Plan 9.2](plan-09-02-filesystem-watcher.md) (we share the same
> `videos` insert shape and `INSERT … ON CONFLICT (content_hash)` rule);
> shares the size+mtime fast path with [Plan 9.3](plan-09-03-periodic-sweep.md);
> writes the cache row updated by [Plan 9.7](plan-09-07-library-stats.md)
> via the `videos` and `processing_jobs` triggers. The HTTP endpoint
> contract is defined in Epic 7 Story 7.3 AC-5; this story owns the
> *behavior* behind that handler. The §7.10 WebSocket event shape is
> reused unchanged — we only repurpose two numeric fields
> (`processed_seconds` ↦ files scanned, `total_duration_seconds` ↦ files
> to scan) as the story specifies.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **API enqueues; Pipeline executes.** The Go API handler `POST /api/libraries/{id}/scan` validates the request and inserts a single row into `processing_jobs(stage='scan', state='queued', priority=50, video_id=NULL)`. The Pipeline scan worker claims it via the standard claim query (Epic 7 §7.1) and walks the roots. The API does **not** open Inotify, hash files, or write to `videos`. | Story AC-1 ("a `scan` job is enqueued at priority 50"); architecture §7 (job store is the universal handoff). | Keeping the scan inside the Pipeline matches every other long-running stage and lets the existing pause/cancel infrastructure (Story 7.12) work unchanged. The API stays I/O-light and request-time-bounded. |
| D2 | **`processing_jobs.video_id` is nullable for `scan` jobs**, with a partial CHECK that allows NULL only when `stage='scan'`. We add the column nullability via migration `0024_processing_jobs_video_id_nullable.sql`. | Refines architecture §7.1 (which has `video_id NOT NULL`). Story AC-3: "`processing_jobs.processed_seconds` is repurposed". | A scan job is library-scoped, not video-scoped — there is no `videos` row to point at when the scan starts (the whole point is to discover them). We add a `library_id` column instead, NOT NULL when `stage IN ('scan','sweep')`, and keep the existing `(video_id, stage)` index by changing it to `(COALESCE(video_id, '00000000-0000-0000-0000-000000000000'::uuid), stage)` — actually a separate `(library_id, stage)` index is cleaner; we add it. |
| D3 | **Repurposed progress fields, no schema change to `processing_jobs`.** We keep `processed_seconds` (REAL) and `total_duration_seconds` (REAL) as-is and document the semantic re-use for `stage='scan'`: integer counts cast to REAL. The §7.10 WS event shape is preserved verbatim — the UI sees `processed_seconds: 412.0, total_duration_seconds: 1000.0` and renders "412 / 1000 files". | Story AC-3 explicitly: "`processing_jobs.processed_seconds` is repurposed to mean files scanned … the §7.10 WS event shape is preserved." | A schema change for two fields would ripple through Epic 7's WS fan-out, the UI's job-card component, and every test fixture. The cost of overloading two REAL fields is one extra paragraph in the UI's tooltip; the cost of changing the schema is days of churn. The job's `metrics` JSONB carries the typed counts (`files_scanned`, `files_total`, `bytes_hashed`) for any consumer that wants exact integers. |
| D4 | **Two-phase walk: count then process.** Phase 1 walks every root with `os.scandir` (no hashing) to compute `files_total` (only files that pass the supported-extension filter from Story 9.5). Phase 2 walks again and processes each file. We cache the phase-1 result in memory; we do **not** persist the file list. | Story AC-3: "estimated via a fast `find` count first". | A first-pass count gives the user an immediate ETA. Phase-1 cost on 50k files over NVMe is <1 s; over a slow NAS the cost is bounded by `scandir` (~5 s for 50k). The alternative (estimating from disk usage / file count from `library_stats_cache`) lies when the library hasn't been scanned before. The phase-1 list is bounded by RAM at ~200 B/path → 10 MB for 50k files; we don't keep the list, only a counter. |
| D5 | **Default mode (`rehash=false`): size+mtime fast path.** For each candidate file, `SELECT id, content_hash, size_bytes, mtime FROM videos WHERE library_id = $1 AND path = $2`. If `size_bytes` and `mtime` match the row, skip. Otherwise compute BLAKE3 and `INSERT … ON CONFLICT (content_hash) DO UPDATE SET path = EXCLUDED.path, size_bytes = EXCLUDED.size_bytes, mtime = EXCLUDED.mtime, updated_at = now()`. | Story AC-1 ("only computes BLAKE3 for new/changed files"); Story 9.4's `INSERT … ON CONFLICT (content_hash) DO UPDATE` rule for move/rename. | The fast path covers the common no-op rescan case. A 50k-file library with no changes finishes in seconds, dominated by `stat()`. The hash-on-mismatch path covers in-place edits naturally — `mtime` changes when ffmpeg rewrites a file. The ON CONFLICT clause makes us idempotent against a watcher event firing for the same file in parallel (the edge case named in the story). |
| D6 | **Rehash mode (`rehash=true`): always hash; supersede on mismatch.** For each file, hash unconditionally. If `videos.content_hash` for `(library_id, path)` differs from the new hash, mark the old row `state = 'SUPERSEDED'`, set `superseded_by_video_id` (a new column added by `0025_videos_superseded.sql`), and INSERT a fresh `videos` row with the new hash. | Story AC-2: "every file is re-hashed regardless of size+mtime, and a `videos` row whose hash no longer matches the file is split into a new row + the old row marked `state='SUPERSEDED'`". | Splitting into a new row preserves history — the user can still see "this file used to be transcribed as X, the new content is Y" and decide whether to delete the old row or keep it for archival. Overwriting in place would lose the old transcript without consent. The `superseded_by_video_id` link makes the audit trail walkable. |
| D7 | **Progress flush every 1 s OR every 100 files**, whichever comes first. The Python worker accumulates `files_scanned` in-process and pushes to `processing_jobs` via `UPDATE … SET processed_seconds = $1, last_segment_end_sec = $1, progress_updated_at = now() WHERE id = $2; NOTIFY job_progress, '$2'`. The LISTEN/NOTIFY listener (Epic 7 Story 7.10) translates that into a WS event. | Story test "reports progress at 1 Hz to the WS"; architecture §7.10. | Per-file UPDATE+NOTIFY would saturate the DB on a NAS scan (potentially 1000+ files/s on warm cache). 1 Hz matches the UI throttle exactly, so the user sees every update. The 100-file ceiling means short scans (< 1 s) still get at least one progress event before completion. |
| D8 | **Cancel honoured at file boundary, not byte boundary.** The worker checks `cancel_requested` once per file (cheap: same row already locked-for-update). If true, it commits the partial state, sets `state='canceled'`, and returns. Files already inserted into `videos` remain. | Story edge case: "Scan canceled via Epic 7 Story 7.12 — the in-progress walk stops at the next file boundary; partial progress is preserved." | Mid-file cancel during BLAKE3 hashing of a 4 GB file would mean throwing away ~10 s of CPU work; once-per-file is the natural commit point. The check is one fetched bool per loop iteration — negligible. |
| D9 | **Watcher-vs-scan race resolved by ON CONFLICT.** Both the watcher (Story 9.2) and the scan worker write videos with the same shape: `INSERT INTO videos (...) VALUES (...) ON CONFLICT (content_hash) DO UPDATE SET path = EXCLUDED.path, size_bytes = EXCLUDED.size_bytes, mtime = EXCLUDED.mtime, updated_at = now() WHERE EXCLUDED.path <> videos.path OR EXCLUDED.size_bytes <> videos.size_bytes OR EXCLUDED.mtime <> videos.mtime`. | Story edge case: "Scan started while watcher events are in-flight — both processes update the same `videos` table; an `INSERT … ON CONFLICT (content_hash) DO UPDATE SET path = EXCLUDED.path` handles the race deterministically." | One canonical SQL clause shared between the two writers means we can change one place. The `WHERE EXCLUDED.path <> videos.path …` guard avoids spurious `updated_at` bumps that would invalidate the stats cache. |
| D10 | **Job heartbeat every 5 s during scan.** The worker spawns a tiny `asyncio` task that updates `last_heartbeat_at` while the walk runs. The job-reaper (Epic 7 Story 7.13) reclaims a stale scan after 60 s of silence. | Architecture §7.1 (`last_heartbeat_at` is updated every 5 s while running). | The phase-1 count walk on a slow NAS could exceed the default 30 s heartbeat window if files are deep. A dedicated heartbeat task decouples liveness from progress updates. |

If D2 is rejected (no nullable `video_id` for scan jobs): we'd need a sentinel "library video" row, which corrupts every JOIN against `videos` and breaks cascade-delete semantics. The CHECK-constrained nullability is the cleanest path.

If D4 is rejected (single-pass scan with no count): the WS progress events have no `total_duration_seconds`, the UI shows an indeterminate spinner, and the user has no ETA. Two-pass costs at most 5 s; the UX win is large.

---

## 1. Architecture diagram — manual scan flow

```
   User clicks "Scan now" in UI
            │
            ▼
   ┌────────────────────────────────┐
   │ API (Go) — POST /api/libraries │
   │   /{id}/scan?rehash=true       │
   │                                │
   │  - validate library_id (UUID)  │
   │  - check user is owner/admin   │
   │  - check no scan already       │
   │    running for this library    │
   │    (idempotency)               │
   │  - INSERT processing_jobs      │
   │    (stage='scan',              │
   │     state='queued',            │
   │     priority=50,               │
   │     library_id=$1,             │
   │     video_id=NULL,             │
   │     metrics={"rehash": true})  │
   │  - return 202 + job_id         │
   └────────────────────────────────┘
            │
            ▼ (Pipeline polling claim, Epic 7 §7.4)
   ┌────────────────────────────────────────────┐
   │ Pipeline (Python) — scan worker            │
   │                                            │
   │  Phase 1: count files                      │
   │   for root in library.roots:               │
   │     for path in walk(root, ext_filter):    │
   │       files_total += 1                     │
   │   UPDATE jobs SET total_duration_seconds=N │
   │                                            │
   │  Phase 2: process files                    │
   │   for path in walk(root, ext_filter):      │
   │     if rehash:                             │
   │       hash = blake3(path)                  │
   │       handle_rehash(path, hash)            │
   │     else:                                  │
   │       row = SELECT … WHERE path=$1         │
   │       if row.size==stat.size and           │
   │          row.mtime==stat.mtime:            │
   │         skip                               │
   │       else:                                │
   │         hash = blake3(path)                │
   │         INSERT … ON CONFLICT DO UPDATE     │
   │     files_scanned += 1                     │
   │     if files_scanned % 100 == 0 or         │
   │        wall - last_flush > 1.0:            │
   │       flush_progress()                     │
   │     if cancel_requested:                   │
   │       break                                │
   │                                            │
   │  finish: state='done', emit WS event       │
   └────────────────────────────────────────────┘
            │
            ▼
   ┌────────────────────────────────────────────┐
   │ Postgres LISTEN/NOTIFY → WS fan-out         │
   │   {type:"job.progress", stage:"scan", ...} │
   │   API broadcasts to subscribed UI clients  │
   └────────────────────────────────────────────┘
```

The scan worker is a **read-only consumer** of `libraries.roots` and a
**multi-row writer** to `videos` and `processing_jobs`. It does not call
the watcher, the probe stage, or any other pipeline stage.

---

## 2. Detailed implementation

### 2.1 Package layout — Go (API Service)

```
apps/api/internal/
├── http/
│   ├── libraries/
│   │   ├── handler.go            // existing; we extend with ScanHandler
│   │   ├── scan.go               // POST /api/libraries/{id}/scan
│   │   └── scan_test.go
│   └── router.go                 // wire scan route
├── scan/
│   ├── enqueue.go                // EnqueueScan(ctx, libraryID, rehash) → jobID
│   ├── enqueue_test.go
│   └── repo.go                   // sqlc-backed insert
└── store/
    └── queries/
        └── scan_jobs.sql         // sqlc input
```

### 2.2 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── scan/
│   ├── __init__.py               // public surface: ScanWorker
│   ├── worker.py                 // ScanWorker.run(claimed_job)
│   ├── walk.py                   // walk_roots(library) → AsyncIterator[Path]
│   ├── hasher.py                 // blake3_file(path, chunk=4MiB) — async
│   ├── upserter.py               // VideoUpserter — ON CONFLICT logic
│   ├── superseder.py             // rehash mode: detect mismatch, supersede
│   ├── progress.py               // ProgressFlusher (1 Hz / 100 files)
│   ├── heartbeat.py              // HeartbeatTask (5 s)
│   ├── errors.py
│   └── tests/
│       ├── conftest.py           // fixtures: tmp library, fake DB
│       ├── test_walk.py
│       ├── test_upserter.py
│       ├── test_superseder.py
│       ├── test_progress.py
│       ├── test_worker_default_mode.py
│       ├── test_worker_rehash_mode.py
│       ├── test_worker_progress_ws.py
│       └── test_worker_cancel.py
└── pipeline/
    └── stages/
        └── scan.py               // claim adapter that delegates to ScanWorker
```

### 2.3 Schema migrations

```sql
-- shared/db/migrations/0024_processing_jobs_video_id_nullable.sql
BEGIN;

ALTER TABLE processing_jobs
    ADD COLUMN library_id UUID
        REFERENCES libraries(id) ON DELETE CASCADE;

ALTER TABLE processing_jobs
    ALTER COLUMN video_id DROP NOT NULL;

-- Either video_id is set (per-video stages) OR library_id is set (scan/sweep).
ALTER TABLE processing_jobs
    ADD CONSTRAINT processing_jobs_target_chk
    CHECK (
        (video_id IS NOT NULL AND library_id IS NULL)
        OR (video_id IS NULL AND library_id IS NOT NULL
            AND stage IN ('scan', 'sweep'))
    );

CREATE INDEX processing_jobs_library_stage
    ON processing_jobs (library_id, stage)
    WHERE library_id IS NOT NULL;

-- Idempotency guard: at most one queued/running scan per library.
CREATE UNIQUE INDEX processing_jobs_one_active_scan
    ON processing_jobs (library_id)
    WHERE stage = 'scan'
      AND state IN ('queued', 'claimed', 'running', 'paused');

COMMIT;
```

```sql
-- shared/db/migrations/0025_videos_superseded.sql
BEGIN;

ALTER TABLE videos
    ADD COLUMN superseded_by_video_id UUID
        REFERENCES videos(id) ON DELETE SET NULL;

CREATE INDEX videos_superseded_by
    ON videos (superseded_by_video_id)
    WHERE superseded_by_video_id IS NOT NULL;

COMMIT;
```

### 2.4 Go — `EnqueueScan` (D1, D2, idempotency)

```go
// apps/api/internal/scan/enqueue.go
package scan

import (
    "context"
    "errors"
    "fmt"
    "log/slog"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgconn"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/maktaba/api/internal/store"
)

var (
    ErrLibraryNotFound = errors.New("library not found")
    ErrScanInFlight    = errors.New("scan already in flight for this library")
)

type Enqueuer struct {
    pool *pgxpool.Pool
    q    *store.Queries
    log  *slog.Logger
}

func NewEnqueuer(pool *pgxpool.Pool, log *slog.Logger) *Enqueuer {
    return &Enqueuer{pool: pool, q: store.New(pool), log: log}
}

// EnqueueScan inserts a scan job at priority 50 (Epic 7 7.3 AC-5). The
// partial-unique index processing_jobs_one_active_scan guarantees at
// most one active scan per library; concurrent calls return ErrScanInFlight.
func (e *Enqueuer) EnqueueScan(ctx context.Context, libraryID uuid.UUID, rehash bool) (int64, error) {
    metrics, _ := store.JSON(map[string]any{"rehash": rehash})

    var jobID int64
    err := e.pool.QueryRow(ctx, `
        INSERT INTO processing_jobs
            (stage, state, priority, library_id, video_id, metrics, created_at)
        VALUES
            ('scan', 'queued', 50, $1, NULL, $2::jsonb, now())
        RETURNING id
    `, libraryID, metrics).Scan(&jobID)

    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) {
        switch pgErr.Code {
        case "23505": // unique_violation on processing_jobs_one_active_scan
            return 0, ErrScanInFlight
        case "23503": // foreign_key_violation on library_id
            return 0, ErrLibraryNotFound
        }
    }
    if err != nil {
        return 0, fmt.Errorf("insert scan job: %w", err)
    }
    e.log.Info("scan_enqueued",
        "library_id", libraryID, "job_id", jobID, "rehash", rehash)
    return jobID, nil
}

// CancelScan flips cancel_requested=true; the worker honours it at the
// next file boundary (D8). No-op if no active scan exists.
func (e *Enqueuer) CancelScan(ctx context.Context, libraryID uuid.UUID) error {
    _, err := e.pool.Exec(ctx, `
        UPDATE processing_jobs
           SET cancel_requested = true
         WHERE library_id = $1
           AND stage = 'scan'
           AND state IN ('queued', 'claimed', 'running', 'paused')
    `, libraryID)
    return err
}
```

### 2.5 Go — `ScanHandler` (chi route, request validation)

```go
// apps/api/internal/http/libraries/scan.go
package libraries

import (
    "encoding/json"
    "errors"
    "log/slog"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "github.com/maktaba/api/internal/auth"
    "github.com/maktaba/api/internal/scan"
)

type ScanHandler struct {
    enq *scan.Enqueuer
    log *slog.Logger
}

func NewScanHandler(enq *scan.Enqueuer, log *slog.Logger) *ScanHandler {
    return &ScanHandler{enq: enq, log: log}
}

// POST /api/libraries/{id}/scan?rehash=true
func (h *ScanHandler) Scan(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    libraryID, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid library id", http.StatusBadRequest)
        return
    }

    rehash := false
    if v := r.URL.Query().Get("rehash"); v != "" {
        if b, err := strconv.ParseBool(v); err == nil {
            rehash = b
        } else {
            http.Error(w, "rehash must be a boolean", http.StatusBadRequest)
            return
        }
    }

    if err := auth.RequireLibraryWrite(ctx, libraryID); err != nil {
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }

    jobID, err := h.enq.EnqueueScan(ctx, libraryID, rehash)
    switch {
    case errors.Is(err, scan.ErrLibraryNotFound):
        http.Error(w, "library not found", http.StatusNotFound)
        return
    case errors.Is(err, scan.ErrScanInFlight):
        // 409 Conflict; client can poll WS to see the existing job.
        http.Error(w, "scan already in flight", http.StatusConflict)
        return
    case err != nil:
        h.log.Error("scan_enqueue_failed", "err", err, "library_id", libraryID)
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusAccepted)
    _ = json.NewEncoder(w).Encode(map[string]any{
        "job_id": jobID,
        "rehash": rehash,
    })
}
```

### 2.6 Python — `walk.py` (D4 phase-1 count + phase-2 yield)

```python
"""walk.py — recursive directory walker with extension filter.

Two entry points:
    - count_files(roots, ext_filter)        → int (phase 1)
    - iter_files(roots, ext_filter)         → AsyncIterator[Path] (phase 2)

Both share the inner _walk() implementation; we run them sequentially in
the worker so that count is always done before the yielding pass starts
(D4). We deliberately do NOT cache the file list: phase 2 re-walks. This
costs an extra `scandir` per directory but keeps memory bounded for
million-file libraries and tolerates files added/removed between phases
(they show up — or don't — in phase 2 naturally).
"""
from __future__ import annotations
import asyncio
import os
from pathlib import Path
from typing import AsyncIterator, Iterable

from ..config.ignore import is_ignored


def _walk(roots: Iterable[str], ext_filter: frozenset[str]):
    """Synchronous generator over (root, file_path)."""
    for root in roots:
        root_p = Path(root)
        if not root_p.exists():
            continue
        for dirpath, dirnames, filenames in os.walk(root_p, followlinks=False):
            # In-place filter dirnames so we don't descend into ignored dirs.
            dirnames[:] = [d for d in dirnames if not is_ignored(Path(dirpath) / d)]
            for fn in filenames:
                p = Path(dirpath) / fn
                if is_ignored(p):
                    continue
                if p.suffix.lower() not in ext_filter:
                    continue
                yield p


async def count_files(roots: Iterable[str], ext_filter: frozenset[str]) -> int:
    """Phase 1: count without hashing. Yields control every 1000 files."""
    n = 0
    for _ in _walk(roots, ext_filter):
        n += 1
        if n % 1000 == 0:
            await asyncio.sleep(0)
    return n


async def iter_files(roots: Iterable[str], ext_filter: frozenset[str]) -> AsyncIterator[Path]:
    """Phase 2: yield file paths, one at a time, cooperative."""
    for path in _walk(roots, ext_filter):
        yield path
        await asyncio.sleep(0)
```

### 2.7 Python — `upserter.py` (D5 default-mode upsert, D9 race-safe)

```python
"""upserter.py — VideoUpserter encapsulates the canonical INSERT shape.

The same SQL is used by the watcher (Plan 9.2) and the scanner. Any
change to the upsert semantics (e.g., a new `state` column default)
goes here.
"""
from __future__ import annotations
import dataclasses, os, time
from pathlib import Path
from typing import Optional

from .hasher import blake3_file


@dataclasses.dataclass(frozen=True)
class FastPathProbe:
    """Cheap check: does videos already have an unchanged row for this path?"""
    video_id: str | None
    content_hash: str | None
    size_bytes: int | None
    mtime_ns: int | None  # ns since epoch, matches stat.st_mtime_ns


_FAST_PATH_SQL = """
SELECT id::text, content_hash, size_bytes,
       extract(epoch FROM mtime) * 1e9
  FROM videos
 WHERE library_id = $1 AND path = $2
"""

_UPSERT_SQL = """
INSERT INTO videos
    (library_id, content_hash, path, filename, size_bytes, mtime, state)
VALUES
    ($1, $2, $3, $4, $5, to_timestamp($6 / 1e9), 'discovered')
ON CONFLICT (content_hash) DO UPDATE
   SET path = EXCLUDED.path,
       size_bytes = EXCLUDED.size_bytes,
       mtime = EXCLUDED.mtime,
       updated_at = now()
 WHERE EXCLUDED.path <> videos.path
    OR EXCLUDED.size_bytes <> videos.size_bytes
    OR EXCLUDED.mtime <> videos.mtime
RETURNING id::text, (xmax = 0) AS inserted
"""


class VideoUpserter:
    def __init__(self, db_pool):
        self._db = db_pool

    async def fast_path_probe(self, conn, *, library_id: str, path: str) -> FastPathProbe:
        row = await conn.fetchrow(_FAST_PATH_SQL, library_id, path)
        if row is None:
            return FastPathProbe(None, None, None, None)
        return FastPathProbe(
            video_id=row[0], content_hash=row[1],
            size_bytes=row[2], mtime_ns=int(row[3]) if row[3] else None)

    async def upsert(self, conn, *, library_id: str, path: Path,
                     content_hash: str, size_bytes: int, mtime_ns: int) -> tuple[str, bool]:
        """Returns (video_id, was_inserted)."""
        row = await conn.fetchrow(
            _UPSERT_SQL, library_id, content_hash, str(path), path.name,
            size_bytes, mtime_ns)
        return row["id"], bool(row["inserted"])
```

### 2.8 Python — `worker.py` (orchestration, D7 progress, D8 cancel, D10 heartbeat)

```python
"""worker.py — ScanWorker.run(): the entry called from the queue claim loop.

Responsibilities:
  1. Resolve library config (roots, ext_filter, rehash flag from metrics).
  2. Phase 1 count → set total_duration_seconds.
  3. Phase 2 process loop with progress flush (1 Hz / 100 files) and
     cancel check at every file boundary.
  4. On exit (normal / cancel / error), commit final state.
"""
from __future__ import annotations
import asyncio, json, logging, os, time
from dataclasses import dataclass
from pathlib import Path

from .walk import count_files, iter_files
from .hasher import blake3_file
from .upserter import VideoUpserter
from .superseder import Superseder
from .progress import ProgressFlusher
from .heartbeat import HeartbeatTask

log = logging.getLogger(__name__)


@dataclass(frozen=True)
class ScanContext:
    job_id: int
    library_id: str
    roots: tuple[str, ...]
    ext_filter: frozenset[str]
    rehash: bool


class ScanWorker:
    def __init__(self, *, db_pool, libraries_repo):
        self._db = db_pool
        self._libs = libraries_repo
        self._upserter = VideoUpserter(db_pool)
        self._superseder = Superseder(db_pool)

    async def run(self, *, claimed_job) -> dict:
        sctx = await self._build_ctx(claimed_job)
        flusher = ProgressFlusher(self._db, job_id=sctx.job_id,
                                  interval_sec=1.0, file_threshold=100)
        heartbeat = HeartbeatTask(self._db, job_id=sctx.job_id, interval_sec=5.0)
        await heartbeat.start()
        t0 = time.monotonic()
        files_total = 0
        files_scanned = 0
        new_videos = 0
        try:
            files_total = await count_files(sctx.roots, sctx.ext_filter)
            await self._db.execute(
                "UPDATE processing_jobs SET total_duration_seconds=$1 WHERE id=$2",
                float(files_total), sctx.job_id)

            async for path in iter_files(sctx.roots, sctx.ext_filter):
                if await self._cancel_requested(sctx.job_id):
                    log.info("scan_canceled", extra={"job_id": sctx.job_id,
                                                     "files_scanned": files_scanned})
                    break
                inserted = await self._process_file(sctx, path)
                files_scanned += 1
                if inserted:
                    new_videos += 1
                await flusher.tick(files_scanned)

            await flusher.flush(files_scanned, force=True)
            return {"files_scanned": files_scanned, "files_total": files_total,
                    "new_videos": new_videos, "wall_sec": time.monotonic() - t0}
        finally:
            await heartbeat.stop()

    async def _process_file(self, sctx: ScanContext, path: Path) -> bool:
        try:
            st = path.stat()
        except FileNotFoundError:
            return False  # file removed mid-scan
        size_bytes, mtime_ns = st.st_size, st.st_mtime_ns

        async with self._db.acquire() as conn:
            if not sctx.rehash:
                probe = await self._upserter.fast_path_probe(
                    conn, library_id=sctx.library_id, path=str(path))
                if (probe.size_bytes == size_bytes
                        and probe.mtime_ns == mtime_ns
                        and probe.content_hash is not None):
                    return False  # fast-path skip (D5)

            # Either rehash mode or fast-path miss → hash.
            content_hash = await blake3_file(path)

            if sctx.rehash and (probe := await self._upserter.fast_path_probe(
                    conn, library_id=sctx.library_id, path=str(path))).content_hash \
                    and probe.content_hash != content_hash:
                # Rehash mode mismatch → supersede (D6).
                await self._superseder.supersede(
                    conn, old_video_id=probe.video_id,
                    new_content_hash=content_hash,
                    library_id=sctx.library_id, path=path,
                    size_bytes=size_bytes, mtime_ns=mtime_ns)
                return True

            _, inserted = await self._upserter.upsert(
                conn, library_id=sctx.library_id, path=path,
                content_hash=content_hash, size_bytes=size_bytes, mtime_ns=mtime_ns)
            return inserted
```

### 2.9 Python — `progress.py` (D7 throttled flush + WS NOTIFY)

```python
"""progress.py — ProgressFlusher: writes to processing_jobs at most 1 Hz
or every 100 files, whichever comes first."""
from __future__ import annotations
import time


class ProgressFlusher:
    def __init__(self, db_pool, *, job_id: int, interval_sec: float, file_threshold: int):
        self._db = db_pool
        self._job_id = job_id
        self._interval = interval_sec
        self._threshold = file_threshold
        self._last_flush_wall = 0.0
        self._last_flush_count = 0

    async def tick(self, files_scanned: int) -> None:
        now = time.monotonic()
        if (files_scanned - self._last_flush_count >= self._threshold
                or now - self._last_flush_wall >= self._interval):
            await self.flush(files_scanned)

    async def flush(self, files_scanned: int, *, force: bool = False) -> None:
        async with self._db.acquire() as conn:
            await conn.execute("""
                UPDATE processing_jobs
                   SET processed_seconds = $1,
                       last_segment_end_sec = $1,
                       progress_updated_at = now()
                 WHERE id = $2
            """, float(files_scanned), self._job_id)
            await conn.execute("SELECT pg_notify('job_progress', $1)", str(self._job_id))
        self._last_flush_wall = time.monotonic()
        self._last_flush_count = files_scanned
```

### 2.10 WS event shape (preserved verbatim, §7.10)

```json
{
  "type": "job.progress",
  "id": 842,
  "video_id": null,
  "library_id": "f7c1...",
  "stage": "scan",
  "state": "running",
  "total_duration_seconds": 1000.0,
  "processed_seconds": 412.0,
  "segments_completed": 0,
  "last_segment_end_sec": 412.0,
  "realtime_factor": null,
  "estimated_remaining_sec": null,
  "updated_at": "2026-05-04T15:42:11.218Z"
}
```

The fan-out code in Epic 7 Story 7.10 needs a **two-line addition** to
emit `library_id` for `stage='scan'` jobs (the field already exists on
`processing_jobs` after migration 0024).

---

## 3. File-by-file scaffolding checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0024_processing_jobs_video_id_nullable.sql` | `library_id` column, CHECK, partial unique idx | `TestMigration0024` |
| 2 | `shared/db/migrations/0025_videos_superseded.sql` | `superseded_by_video_id` column + index | `TestMigration0025` |
| 3 | `apps/api/internal/store/queries/scan_jobs.sql` | sqlc inputs (`InsertScanJob`, `CancelScan`) | (n/a) |
| 4 | `apps/api/internal/scan/enqueue.go` | `Enqueuer`, `EnqueueScan`, `CancelScan`, `ErrScanInFlight`, `ErrLibraryNotFound` | `TestEnqueueScan*` |
| 5 | `apps/api/internal/scan/repo.go` | thin wrapper over sqlc Queries | (n/a) |
| 6 | `apps/api/internal/http/libraries/scan.go` | `ScanHandler`, `Scan` (HTTP) | `TestScanHandler*` |
| 7 | `apps/api/internal/http/router.go` (extend) | `r.Post("/api/libraries/{id}/scan", ...)` | `TestRouterRegistersScan` |
| 8 | `pipeline/src/maktaba_pipeline/scan/walk.py` | `count_files`, `iter_files` | `test_walk` |
| 9 | `pipeline/src/maktaba_pipeline/scan/hasher.py` | `blake3_file` | `test_hasher` |
| 10 | `pipeline/src/maktaba_pipeline/scan/upserter.py` | `VideoUpserter`, `FastPathProbe` | `test_upserter` |
| 11 | `pipeline/src/maktaba_pipeline/scan/superseder.py` | `Superseder.supersede` | `test_superseder` |
| 12 | `pipeline/src/maktaba_pipeline/scan/progress.py` | `ProgressFlusher` | `test_progress` |
| 13 | `pipeline/src/maktaba_pipeline/scan/heartbeat.py` | `HeartbeatTask` | `test_heartbeat` |
| 14 | `pipeline/src/maktaba_pipeline/scan/worker.py` | `ScanWorker`, `ScanContext` | `test_worker_*` |
| 15 | `pipeline/src/maktaba_pipeline/pipeline/stages/scan.py` (extend) | claim adapter dispatching to ScanWorker | `test_stage_scan_dispatch` |

---

## 4. Test cases

### 4.1 `TestEnqueueScan_Inserts_PriorityFifty` (AC-1, Go)

```go
func TestEnqueueScan_Inserts_PriorityFifty(t *testing.T) {
    db := testdb.Fresh(t)
    enq := scan.NewEnqueuer(db.Pool, slog.Default())
    libID := testdb.SeedLibrary(t, db, "videos")

    jobID, err := enq.EnqueueScan(t.Context(), libID, false)
    require.NoError(t, err)
    require.Greater(t, jobID, int64(0))

    var stage, state string
    var priority int
    var libIDOut uuid.UUID
    var rehash bool
    err = db.Pool.QueryRow(t.Context(), `
        SELECT stage, state, priority, library_id, (metrics->>'rehash')::bool
          FROM processing_jobs WHERE id=$1
    `, jobID).Scan(&stage, &state, &priority, &libIDOut, &rehash)
    require.NoError(t, err)
    require.Equal(t, "scan", stage)
    require.Equal(t, "queued", state)
    require.Equal(t, 50, priority)
    require.Equal(t, libID, libIDOut)
    require.False(t, rehash)
}
```

### 4.2 `TestEnqueueScan_Idempotent` (D9 idempotency)

```go
func TestEnqueueScan_RejectsConcurrent(t *testing.T) {
    db := testdb.Fresh(t)
    enq := scan.NewEnqueuer(db.Pool, slog.Default())
    libID := testdb.SeedLibrary(t, db, "videos")

    _, err := enq.EnqueueScan(t.Context(), libID, false)
    require.NoError(t, err)
    _, err = enq.EnqueueScan(t.Context(), libID, true)
    require.ErrorIs(t, err, scan.ErrScanInFlight)
}
```

### 4.3 `test_worker_default_mode_skips_unchanged` (AC-1, Python)

```python
async def test_default_mode_skips_files_with_matching_size_mtime(
    db, tmp_library, scan_worker, ext_filter,
):
    """A 100-file library re-scanned twice: the second pass skips all."""
    paths = tmp_library.create(n=100)
    job = await db.queue_scan_job(library_id=tmp_library.id, rehash=False)

    # First pass: all new.
    metric_a = await scan_worker.run(claimed_job=job)
    assert metric_a["new_videos"] == 100

    # Second pass: all skipped via fast path.
    job2 = await db.queue_scan_job(library_id=tmp_library.id, rehash=False)
    metric_b = await scan_worker.run(claimed_job=job2)
    assert metric_b["files_scanned"] == 100
    assert metric_b["new_videos"] == 0
```

### 4.4 `test_rehash_mode_supersedes_changed_file` (AC-2)

```python
async def test_rehash_detects_inplace_edit(
    db, tmp_library, scan_worker,
):
    """Edit a file in place; rehash detects mismatch and supersedes."""
    paths = tmp_library.create(n=1)
    p = paths[0]

    # First pass: original content.
    await scan_worker.run(claimed_job=await db.queue_scan_job(
        library_id=tmp_library.id, rehash=False))
    old_id = await db.fetchval(
        "SELECT id FROM videos WHERE path=$1", str(p))

    # Edit in place — keep size, change content; touch mtime to old value
    # to simulate a tool that preserves mtime.
    p.write_bytes(b"X" * p.stat().st_size)
    os.utime(p, (p.stat().st_atime, p.stat().st_mtime - 60))  # rewind mtime

    # Default mode would skip; rehash catches it.
    metric = await scan_worker.run(claimed_job=await db.queue_scan_job(
        library_id=tmp_library.id, rehash=True))
    assert metric["new_videos"] == 1

    rows = await db.fetch(
        "SELECT id, state, superseded_by_video_id FROM videos WHERE path=$1 ORDER BY created_at",
        str(p))
    assert len(rows) == 2
    old, new = rows
    assert str(old["id"]) == str(old_id)
    assert old["state"] == "SUPERSEDED"
    assert old["superseded_by_video_id"] == new["id"]
```

### 4.5 `test_progress_flushes_at_1hz_and_per_100_files` (AC-3)

```python
async def test_progress_emits_at_least_one_event_per_second(
    db, tmp_library, scan_worker, ws_collector,
):
    """1000-file scan → at least 1 ws event per second of wall time."""
    tmp_library.create(n=1000)
    job = await db.queue_scan_job(library_id=tmp_library.id, rehash=False)

    t0 = time.monotonic()
    await scan_worker.run(claimed_job=job)
    elapsed = time.monotonic() - t0

    events = [e for e in ws_collector.events if e["type"] == "job.progress"
              and e["stage"] == "scan"]
    # At least floor(elapsed) progress events; final one shows complete.
    assert len(events) >= max(1, int(elapsed))
    final = events[-1]
    assert final["processed_seconds"] == 1000.0
    assert final["total_duration_seconds"] == 1000.0
```

### 4.6 `test_cancel_at_file_boundary` (D8, edge case)

```python
async def test_cancel_request_stops_at_next_file(
    db, tmp_library, scan_worker,
):
    """Setting cancel_requested mid-scan stops within ~1 file boundary."""
    tmp_library.create(n=500)
    job = await db.queue_scan_job(library_id=tmp_library.id, rehash=False)

    # Run scan; halfway through, set cancel_requested.
    async def cancel_after_50():
        while True:
            n = await db.fetchval(
                "SELECT processed_seconds FROM processing_jobs WHERE id=$1",
                job.id)
            if n and n >= 50:
                await db.execute(
                    "UPDATE processing_jobs SET cancel_requested=true WHERE id=$1",
                    job.id)
                return
            await asyncio.sleep(0.01)

    await asyncio.gather(scan_worker.run(claimed_job=job), cancel_after_50())

    final_count = await db.fetchval(
        "SELECT processed_seconds FROM processing_jobs WHERE id=$1", job.id)
    # Cancel honoured within the next 100-file flush window.
    assert 50 <= final_count < 200
```

### 4.7 `test_watcher_scan_race_uses_on_conflict` (edge)

```python
async def test_concurrent_watcher_event_and_scanner_dont_dup(
    db, tmp_library, scan_worker, watcher,
):
    """Watcher INSERT + scanner INSERT for the same file → exactly one row."""
    p = tmp_library.create_one("a.mp4")
    # Race: kick off scan and watcher INSERT concurrently.
    job = await db.queue_scan_job(library_id=tmp_library.id, rehash=False)
    await asyncio.gather(
        scan_worker.run(claimed_job=job),
        watcher.observe_path(p),
    )
    n = await db.fetchval(
        "SELECT COUNT(*) FROM videos WHERE library_id=$1", tmp_library.id)
    assert n == 1
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Empty library (no roots).** `count_files` returns 0; `iter_files` yields nothing; the worker emits one progress event with `processed_seconds=0, total_duration_seconds=0` and finishes. | `test_empty_library` |
| E2  | **Root path doesn't exist or is a broken symlink.** `_walk` skips missing roots silently. Worker logs `scan_root_missing` but does not fail the job. | `test_missing_root_skipped` |
| E3  | **File deleted mid-scan** (between phase 1 and phase 2 hashing). `path.stat()` raises `FileNotFoundError`; `_process_file` returns False; the file is excluded from this scan's results without error. | `test_file_deleted_mid_scan` |
| E4  | **Permission denied on a directory.** `os.walk` raises `PermissionError` via the inner `scandir` (we do NOT pass `onerror=`); the loop continues to the next directory. We catch and log via a custom onerror handler. | `test_permission_denied_logged` |
| E5  | **Watcher event fires for the same file** (story-named edge). The shared `INSERT … ON CONFLICT (content_hash) DO UPDATE` makes both writers converge to the same row. The `WHERE EXCLUDED.path <> videos.path …` guard prevents spurious `updated_at` bumps. | `test_watcher_scan_race_uses_on_conflict` |
| E6  | **Cancel mid-scan** (story-named edge). `_cancel_requested(job_id)` is checked once per file (D8). Files already inserted remain. The job state flips to `canceled` on exit. | `test_cancel_at_file_boundary` |
| E7  | **`?rehash=true` with a clean library.** Every file is hashed, every fast-path probe finds a matching `content_hash`, no supersede triggers, no new rows, but `bytes_hashed` in metrics reflects the work done. | `test_rehash_clean_library_no_changes` |
| E8  | **Hash matches but path moved.** Watcher and scanner alike: `INSERT … ON CONFLICT (content_hash) DO UPDATE SET path = EXCLUDED.path` updates the path; no new row. This is Story 9.4's move/rename detection running through the scan code path. | `test_rehash_path_change` |
| E9  | **Concurrent scan attempt** (user double-clicks "Scan now"). Partial unique index `processing_jobs_one_active_scan` rejects the second insert with 23505 → API returns 409 Conflict. | `TestEnqueueScan_RejectsConcurrent` |
| E10 | **Worker crash mid-scan.** `last_heartbeat_at` stops updating; the job-reaper (Epic 7 Story 7.13) flips `state` to `failed` after 60 s. The user can re-trigger; the new scan picks up where the old one left off because already-inserted rows are fast-path-skipped. | `test_reaper_recovers_dead_scan` (deferred to Story 7.13 plan) |
| E11 | **Library deleted mid-scan.** FK `library_id REFERENCES libraries(id) ON DELETE CASCADE` cascades; the next worker UPDATE on `processing_jobs` finds the row gone (cascade) — we treat `0 rows updated` as "job withdrawn" and exit cleanly. | `test_library_deleted_during_scan` |
| E12 | **Hashing a 50 GB file** (e.g., a Blu-ray rip). BLAKE3 streams in 4 MiB chunks; memory usage flat. CPU dominates; one file can take minutes. The 5 s heartbeat keeps the reaper from killing the job. Cancel is honoured at the *next* file, so the user waits up to one file's worth. | Documented; not separately tested. |

---

## 6. Acceptance checklist

- [ ] **A1** `POST /api/libraries/{id}/scan` enqueues a `processing_jobs` row with `stage='scan', state='queued', priority=50, library_id=$id, video_id=NULL`. (`TestEnqueueScan_Inserts_PriorityFifty`)
- [ ] **A2** Default mode applies the size+mtime fast path: a no-op rescan over an unchanged 1000-file library hashes zero files and inserts zero new rows. (`test_default_mode_skips_files_with_matching_size_mtime`)
- [ ] **A3** `?rehash=true` re-hashes every file regardless of size+mtime. A file edited in place (mtime preserved by tooling) is detected: the old row flips to `state='SUPERSEDED'` with `superseded_by_video_id` populated; a new row carries the new hash. (`test_rehash_detects_inplace_edit`)
- [ ] **A4** Progress reporting: `processing_jobs.processed_seconds` carries files-scanned (REAL), `total_duration_seconds` carries files-to-scan from phase 1, and the §7.10 WS event shape is preserved verbatim. (`test_progress_emits_at_least_one_event_per_second`)
- [ ] **A5** Concurrent scan attempts for the same library return 409 Conflict (partial unique index `processing_jobs_one_active_scan`). (`TestEnqueueScan_RejectsConcurrent`)
- [ ] **A6** Cancel via Epic 7 Story 7.12 stops the walk at the next file boundary; rows already inserted are preserved. (`test_cancel_at_file_boundary`)
- [ ] **A7** Watcher-vs-scanner race resolved by shared `ON CONFLICT (content_hash) DO UPDATE` upsert; exactly one `videos` row results. (`test_concurrent_watcher_event_and_scanner_dont_dup`)
- [ ] **A8** Migration `0024_processing_jobs_video_id_nullable.sql` makes `video_id` nullable, adds `library_id`, the CHECK, and the partial unique idx. (`TestMigration0024`)
- [ ] **A9** Migration `0025_videos_superseded.sql` adds `superseded_by_video_id`. (`TestMigration0025`)
- [ ] **A10** Heartbeat task updates `last_heartbeat_at` every 5 s during the scan; phase-1 count alone (no heartbeat) does not exceed the reaper window on a 50k-file library. (`test_heartbeat_during_phase_one`)

---

## 7. Performance budget

(Story 9.7 owns the explicit 50 ms target; this story has no fixed
budget but documents reference numbers.)

| Phase | Cost (50k files, 100 GB total) | Notes |
|-------|--------------------------------|-------|
| Phase 1 count | ~3 s on NVMe; ~30 s on slow NAS | `os.scandir` + `is_ignored` + extension check; no I/O beyond directory reads. |
| Phase 2 fast-path scan (no changes) | ~10 s on NVMe; ~120 s on NAS | `stat()` per file + DB SELECT; no hashing. |
| Phase 2 cold scan (every file new) | ~2 hours @ 100 GB / (500 MB/s BLAKE3) | Hash-bound; CPU dominates. |
| Phase 2 rehash | same as cold scan | every file hashed. |
| Progress flush | < 1 ms per flush | one UPDATE + one NOTIFY; debounced. |

Cold scans are intentionally not bounded by a wall-clock target — the
user accepts they will take time. The plan ensures that **incremental**
rescans (the common case) are fast enough that the user can run them
freely.
