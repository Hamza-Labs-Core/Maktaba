# Implementation Plan — Story 9.3 Periodic Full Sweep

> Companion to [story-09-03-periodic-sweep.md](story-09-03-periodic-sweep.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on Story 9.1 (settings), Story 9.2 (watcher reuses sweep on
> boot), and Story 9.4 (BLAKE3 dedup for the move case).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Runtime owner | Pipeline Service. Sweeps run as `scan`-stage jobs (existing stage from Story 6.1) with `payload.reason = "periodic" | "manual" | "watcher_boot_catchup"`. The job worker dispatches to `pipeline/sweep/sweep_runner.py`. |
| Scheduling | A single `SweepScheduler` task per Pipeline process, ticking every 60 s, evaluating each library's `sweep_interval_sec` against `library_sweeps.started_at` of the most recent run. |
| Single-flight | Postgres `pg_try_advisory_xact_lock(hash('sweep:'||library_id::text))` taken in the job's transaction. Held for the duration; tick-while-running drops the new tick (AC-2). |
| Diff query | One `SELECT path, size_bytes, mtime, content_hash FROM videos WHERE library_id=$1` snapshot at start; the walker compares each on-disk file against it. New table `library_sweeps` (defined in the README) stores per-sweep stats. |
| Move detection | When a path is new but `(content_hash, size_bytes)` matches an existing row at a different path, the sweep updates `videos.path` and writes a `video_path_history` row (Story 9.2 created the table). Hash is only computed for new-or-changed paths (size+mtime fast path is the gate). |
| Out of scope | The hashing primitive itself (Story 9.4); the user-facing scan progress (Story 9.6); the FSM transition `→ missing` is invoked here using lowercase canonical state strings; the FSM-extension migration that adds `missing` to the CHECK constraint is owned by plan-09-06. |

## 1. Architecture diagram

```
   ┌──────────────────────────────────────────────────────────────┐
   │  SweepScheduler (one per process)                            │
   │                                                              │
   │  every 60 s:                                                 │
   │    for lib in libraries WHERE deleted_at IS NULL:            │
   │      effective = effective_for(lib.id)                       │
   │      if effective.sweep_interval_sec == 0: skip              │
   │      last = SELECT MAX(started_at) FROM library_sweeps       │
   │             WHERE library_id = lib.id                        │
   │      if now - last >= effective.sweep_interval_sec:          │
   │        enqueue(stage=scan, video_id=NULL,                   │
   │                priority=200, payload={                       │
   │                  library_id, reason="periodic"               │
   │                })                                            │
   └─────────────────┬────────────────────────────────────────────┘
                     │ scan job
                     ▼
   ┌──────────────────────────────────────────────────────────────┐
   │  ScanWorker.run_scan_job(job)                                │
   │                                                              │
   │  if not pg_try_advisory_xact_lock(hash('sweep:'||lib_id)):   │
   │    log info "sweep_skipped_lock_busy"; return DONE.          │
   │                                                              │
   │  insert library_sweeps row (started_at = now()).             │
   │                                                              │
   │  catalog = SELECT id, path, size_bytes, mtime, content_hash  │
   │              FROM videos WHERE library_id=lib                │
   │                AND deleted_at IS NULL  -- soft-delete column │
   │  catalog_by_path = {row.path: row}                           │
   │  catalog_by_hash = {row.content_hash: row}                   │
   │                                                              │
   │  visited_paths = set()                                       │
   │                                                              │
   │  for root in lib.roots:                                      │
   │    walker = scandir-recursive with ignore_globs              │
   │    for path, st in walker:                                   │
   │      visited_paths.add(path)                                 │
   │      progress.scanned += 1                                   │
   │                                                              │
   │      cat = catalog_by_path.get(path)                         │
   │      if cat and cat.size_bytes==st.size and cat.mtime==st.mtime:│
   │        continue   # fast path                                │
   │                                                              │
   │      if cat:                                                 │
   │        # path matches but size/mtime drifted: enqueue         │
   │        # (re-probe is the way to handle in-place rewrites)   │
   │        enqueue(scan, video_id=cat.id, priority=150,          │
   │                payload={"reason":"size_changed"})            │
   │        continue                                              │
   │                                                              │
   │      # path is new                                           │
   │      h = blake3_4mib(path, st.size)        # Story 9.4       │
   │      moved = catalog_by_hash.get(h)                          │
   │      if moved and moved.path not in visited_paths:           │
   │        UPDATE videos SET path=$1 WHERE id=$2                 │
   │        INSERT INTO video_path_history(...,'sweep')           │
   │        progress.moved_videos += 1                            │
   │        catalog_by_path[path] = moved                         │
   │      else:                                                   │
   │        enqueue(scan, video_id=None, priority=200,            │
   │                payload={"library_id": lib_id,                │
   │                         "path": path, "size_bytes": st.size, │
   │                         "content_hash": h})  # hex string    │
   │        progress.new_videos += 1                              │
   │                                                              │
   │  # Missing detection (lowercase FSM, canonical Epic-9 ext.):  │
   │  for cat in catalog_by_path.values():                        │
   │    if cat.path not in visited_paths and cat.state <> 'missing':│
   │      UPDATE videos SET state='missing' WHERE id=cat.id       │
   │      progress.removed_videos += 1                            │
   │                                                              │
   │  UPDATE library_sweeps SET finished_at=now(),                │
   │         scanned, new_videos, moved_videos, removed_videos,   │
   │         errors_jsonb WHERE id = sweep_id                     │
   │  notify('library.sweep_done', {...})                         │
   └──────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/sweep/__init__.py` | Re-exports `SweepScheduler`, `run_sweep_job`. |
| `pipeline/src/maktaba_pipeline/sweep/scheduler.py` | The 60 s tick loop. |
| `pipeline/src/maktaba_pipeline/sweep/sweep_runner.py` | The actual walker; called from the scan-job dispatcher. |
| `pipeline/src/maktaba_pipeline/sweep/walker.py` | `scandir`-based recursive iterator with the ignore-glob filter from Story 9.5. |
| `pipeline/tests/sweep/test_scheduler.py` | Unit tests per §6.1. |
| `pipeline/tests/sweep/test_sweep_runner_integration.py` | Filesystem + DB integration tests per §6.2. |
| `shared/db/migrations/0032_library_sweeps.sql` | Creates `library_sweeps` per the README schema. |
| `shared/db/migrations/0032_library_sweeps.sqlite.sql` | SQLite variant. |
| `shared/db/queries/library_sweeps.sql` | sqlc input — `InsertSweep`, `FinalizeSweep`, `GetLastSweep`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/jobs/dispatcher.py` | Route `stage='scan'` to `sweep_runner.run_sweep_job` when `payload.library_id` is present and `payload.video_id` is not. |
| `pipeline/src/maktaba_pipeline/db/pubsub.py` | The canonical channel-name registry (09-01 §2.5) already declares `LIBRARY_SWEEP_DONE`. This plan only consumes it. |
| `api/internal/db/queries/library_sweeps.sql.go` | Generated by sqlc — read access for the API's stats endpoint (Story 9.7). |
| `specs/epics/09-library-management/README.md` | Tick story 9.3. |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/sweep/sweep_runner.py
from __future__ import annotations
from dataclasses import dataclass, field
from pathlib import Path
from uuid import UUID


@dataclass(slots=True)
class SweepProgress:
    library_id: UUID
    sweep_id: UUID
    started_at: float
    scanned: int = 0
    new_videos: int = 0
    moved_videos: int = 0
    removed_videos: int = 0
    errors: list[dict] = field(default_factory=list)


@dataclass(slots=True, frozen=True)
class CatalogRow:
    id: UUID
    path: str
    size_bytes: int                 # videos.size_bytes (architecture §8.1)
    mtime: float
    content_hash: str | None        # 64-char hex BLAKE3 (TEXT in DB)
    state: str
```

## 3. Database migration — `library_sweeps`

### 3.1 Postgres — `0032_library_sweeps.sql`

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE library_sweeps (
    id              UUID PRIMARY KEY,
    library_id      UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    started_at      TIMESTAMPTZ NOT NULL,
    finished_at     TIMESTAMPTZ,
    reason          TEXT NOT NULL DEFAULT 'periodic'
                     CHECK (reason IN ('periodic','manual','watcher_boot_catchup')),
    rehash          BOOLEAN NOT NULL DEFAULT false,
    scanned         INTEGER NOT NULL DEFAULT 0,
    new_videos      INTEGER NOT NULL DEFAULT 0,
    moved_videos    INTEGER NOT NULL DEFAULT 0,
    removed_videos  INTEGER NOT NULL DEFAULT 0,
    errors_jsonb    JSONB
);

CREATE INDEX library_sweeps_lookup
    ON library_sweeps (library_id, started_at DESC);

-- Single-flight at the row level: at most one in-flight sweep per
-- library. The advisory lock is the runtime guard; this index makes
-- the "any in-flight?" probe O(1).
CREATE UNIQUE INDEX library_sweeps_one_in_flight
    ON library_sweeps (library_id)
    WHERE finished_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS library_sweeps;
-- +goose StatementEnd
```

### 3.2 SQLite — `0032_library_sweeps.sqlite.sql`

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE library_sweeps (
    id              TEXT PRIMARY KEY,
    library_id      TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    started_at      TEXT NOT NULL,
    finished_at     TEXT,
    reason          TEXT NOT NULL DEFAULT 'periodic'
                     CHECK (reason IN ('periodic','manual','watcher_boot_catchup')),
    rehash          INTEGER NOT NULL DEFAULT 0,  -- bool
    scanned         INTEGER NOT NULL DEFAULT 0,
    new_videos      INTEGER NOT NULL DEFAULT 0,
    moved_videos    INTEGER NOT NULL DEFAULT 0,
    removed_videos  INTEGER NOT NULL DEFAULT 0,
    errors_jsonb    TEXT  -- JSON
);

CREATE INDEX library_sweeps_lookup
    ON library_sweeps (library_id, started_at DESC);

CREATE UNIQUE INDEX library_sweeps_one_in_flight
    ON library_sweeps (library_id)
    WHERE finished_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS library_sweeps;
-- +goose StatementEnd
```

## 4. Code scaffolding

### 4.1 Scheduler

```python
# pipeline/src/maktaba_pipeline/sweep/scheduler.py
import asyncio, time, logging
from uuid import uuid4

from ..config.library import effective_for
from ..db.jobs import enqueue, Stage

log = logging.getLogger(__name__)
TICK_SECONDS = 60.0


class SweepScheduler:
    def __init__(self, db, *, clock=time.time): ...
    async def run(self) -> None:
        while True:
            try:
                await self._tick()
            except Exception:  # never let the scheduler die
                log.exception("sweep_scheduler_tick_failed")
            await asyncio.sleep(TICK_SECONDS)

    async def _tick(self) -> None:
        rows = await self._db.fetch(
            "SELECT id FROM libraries WHERE deleted_at IS NULL"
        )
        for r in rows:
            eff = await effective_for(self._db, r["id"])
            if eff.sweep_interval_sec == 0:
                continue
            last = await self._db.fetchval(
                "SELECT EXTRACT(EPOCH FROM MAX(started_at)) "
                "FROM library_sweeps WHERE library_id=$1", r["id"],
            )
            now = self._clock()
            if last is not None and now - last < eff.sweep_interval_sec:
                continue
            await enqueue(self._db, video_id=None, stage=Stage.SCAN,
                          priority=200,  # background
                          payload={
                              "library_id": str(r["id"]),
                              "reason": "periodic",
                          })
```

### 4.2 SweepRunner core

```python
# pipeline/src/maktaba_pipeline/sweep/sweep_runner.py
import asyncio, hashlib, json, time
from pathlib import Path
from uuid import UUID, uuid4

from ..hash.blake3_4mib import blake3_4mib   # Story 9.4
from ..config.library import effective_for
from ..ignore.matcher import build_matcher    # Story 9.5
from ..db.jobs import enqueue, Stage
from ..db.pubsub import LIBRARY_SWEEP_DONE, get_bus

PROGRESS_PUSH_HZ = 1.0


async def run_sweep_job(db, job, *, clock=time.time) -> None:
    payload = job.payload or {}
    library_id = UUID(payload["library_id"])
    reason = payload.get("reason", "periodic")
    rehash = bool(payload.get("rehash", False))

    sweep_id = uuid4()

    # Single-flight (AC-2). The lock is held for the duration of the
    # transaction; the worker keeps the transaction open across the walk
    # via a savepoint pattern OR (preferred for long sweeps) acquires
    # the lock in a *short* tx, then drops it for catalog reads — but
    # then the lock no longer protects the run. We do the latter and
    # use the partial-unique index on `library_sweeps` as the durable
    # guard:
    try:
        await db.execute(
            "INSERT INTO library_sweeps "
            "(id, library_id, started_at, reason, rehash) "
            "VALUES ($1, $2, now(), $3, $4)",
            sweep_id, library_id, reason, rehash,
        )
    except UniqueViolation:
        # Another sweep is in flight; AC-2 says drop the new tick.
        return  # job marked DONE by dispatcher; no error.

    progress = SweepProgress(
        library_id=library_id, sweep_id=sweep_id,
        started_at=clock(),
    )
    progress_task = asyncio.create_task(
        _push_progress_loop(db, job, progress))

    try:
        eff = await effective_for(db, library_id)
        ignore = build_matcher(eff.ignore_globs, eff.supported_video_exts)
        roots = await db.fetch(
            "SELECT path FROM library_roots WHERE library_id=$1", library_id)
        catalog = await _load_catalog(db, library_id)
        catalog_by_path = {r.path: r for r in catalog}
        catalog_by_hash = {r.content_hash: r for r in catalog
                           if r.content_hash}

        visited: set[str] = set()
        for root in roots:
            async for path, st in walk(Path(root["path"]), ignore):
                visited.add(str(path))
                progress.scanned += 1
                await _process_one(db, library_id, path, st,
                                   catalog_by_path, catalog_by_hash,
                                   visited, progress, rehash)

        # MISSING detection — anything in catalog the walker did not see.
        # Lowercase FSM strings (architecture canonical); 'missing' is an
        # auxiliary terminal state owned by the Epic-9 FSM-extension
        # migration in plan-09-06.
        for r in catalog:
            if r.path not in visited and r.state != "missing":
                await db.execute(
                    "UPDATE videos SET state='missing', updated_at=now() "
                    "WHERE id=$1", r.id)
                progress.removed_videos += 1

    except Exception as e:
        progress.errors.append({"phase": "walk", "error": repr(e)})
        raise
    finally:
        progress_task.cancel()
        await db.execute(
            "UPDATE library_sweeps "
            "  SET finished_at=now(), scanned=$1, new_videos=$2, "
            "      moved_videos=$3, removed_videos=$4, errors_jsonb=$5 "
            "WHERE id=$6",
            progress.scanned, progress.new_videos,
            progress.moved_videos, progress.removed_videos,
            json.dumps(progress.errors) if progress.errors else None,
            sweep_id,
        )
        get_bus().publish(LIBRARY_SWEEP_DONE, {
            "library_id": str(library_id),
            "sweep_id": str(sweep_id),
            "scanned": progress.scanned,
            "new_videos": progress.new_videos,
            "moved_videos": progress.moved_videos,
            "removed_videos": progress.removed_videos,
            "errors": len(progress.errors),
        })
```

### 4.3 Per-file decision (`_process_one`)

```python
async def _process_one(db, library_id, path, st,
                       catalog_by_path, catalog_by_hash,
                       visited, progress, rehash) -> None:
    cat = catalog_by_path.get(str(path))
    if cat is not None and not rehash \
            and cat.size_bytes == st.st_size \
            and abs(cat.mtime - st.st_mtime) < 1.0:
        return  # fast path

    if cat is not None:
        # Path matches but size/mtime drifted (or rehash=true).
        await enqueue(db, video_id=cat.id, stage=Stage.SCAN, priority=150,
                      payload={"reason": "size_changed",
                               "library_id": str(library_id)})
        return

    # New path. Compute hash (hex string) and check for a moved-file match.
    h = await asyncio.to_thread(blake3_4mib, path, st.st_size)
    moved = catalog_by_hash.get(h)
    if moved is not None and moved.path not in visited:
        async with db.transaction():
            await db.execute(
                "UPDATE videos SET path=$1, updated_at=now() "
                "WHERE id=$2 AND content_hash=$3",
                str(path), moved.id, h)
            await db.execute(
                "INSERT INTO video_path_history "
                "  (video_id, old_path, new_path, detected_by) "
                "VALUES ($1,$2,$3,'sweep')",
                moved.id, moved.path, str(path))
        progress.moved_videos += 1
        catalog_by_path[str(path)] = moved._replace(path=str(path))
        return

    # Genuine new file.
    await enqueue(db, video_id=None, stage=Stage.SCAN, priority=200,
                  payload={
                      "library_id": str(library_id),
                      "path": str(path),
                      "size_bytes": st.st_size,
                      "content_hash": h,    # hex string
                  })
    progress.new_videos += 1
```

### 4.4 Progress push (uses `processing_jobs.processed_seconds` per Story 9.6's contract)

```python
async def _push_progress_loop(db, job, progress: SweepProgress) -> None:
    while True:
        await asyncio.sleep(1.0 / PROGRESS_PUSH_HZ)
        await db.execute(
            "UPDATE processing_jobs "
            "   SET processed_seconds = $1, "
            "       progress_updated_at = now(), "
            "       metrics = jsonb_build_object("
            "         'new_videos', $2::int, "
            "         'moved_videos', $3::int, "
            "         'removed_videos', $4::int) "
            " WHERE id = $5",
            progress.scanned, progress.new_videos,
            progress.moved_videos, progress.removed_videos, job.id,
        )
```

### 4.5 Walker (excerpt — full glob filter from Story 9.5)

```python
# pipeline/src/maktaba_pipeline/sweep/walker.py
import os
from pathlib import Path

async def walk(root: Path, ignore) -> AsyncIterator[tuple[Path, os.stat_result]]:
    visited_inodes: set[tuple[int,int]] = set()  # (dev, inode) — symlink loop guard

    def _iter(d: Path):
        try:
            it = os.scandir(d)
        except (PermissionError, FileNotFoundError):
            return
        with it:
            for entry in it:
                if ignore.matches(entry.path):
                    continue
                try:
                    st = entry.stat(follow_symlinks=True)
                except (FileNotFoundError, PermissionError):
                    continue
                key = (st.st_dev, st.st_ino)
                if key in visited_inodes:
                    continue
                visited_inodes.add(key)
                if entry.is_dir(follow_symlinks=True):
                    yield from _iter(Path(entry.path))
                elif entry.is_file(follow_symlinks=True) \
                        and ignore.is_supported_extension(entry.path):
                    yield Path(entry.path), st

    # Yield in batches of 1000 to surrender control back to asyncio.
    BATCH = 1000
    batch: list[tuple[Path, os.stat_result]] = []
    for item in _iter(root):
        batch.append(item)
        if len(batch) >= BATCH:
            for i in batch:
                yield i
            batch.clear()
            await asyncio.sleep(0)  # cooperative yield
    for i in batch:
        yield i
```

## 5. Test plan

### 5.1 Scheduler unit tests (`test_scheduler.py`)

| Test | What it pins |
|---|---|
| `test_tick_skips_when_interval_zero` | `effective.sweep_interval_sec=0` → no enqueue. AC-3 disable. |
| `test_tick_enqueues_after_interval` | `last_started_at = now - interval - 1` → one enqueue per library. |
| `test_tick_does_not_enqueue_within_interval` | `last_started_at = now` → zero enqueues. |
| `test_tick_handles_no_prior_sweep` | `MAX(started_at) IS NULL` → enqueue (first run). |
| `test_tick_continues_after_per_library_error` | One library raises (e.g., bad config); other libraries are still scheduled. |

### 5.2 SweepRunner integration tests (`test_sweep_runner_integration.py`)

| Test | What it pins |
|---|---|
| `test_fast_path_skips_unchanged_files` | Pre-populate catalog with 100 files; fixture filesystem has the same 100 with matching size+mtime → 0 enqueues, 0 hashes computed. AC-1 fast path. |
| `test_new_file_enqueues_scan` | Catalog empty; one new file → one `enqueue` with `payload.path` and `content_hash`. |
| `test_modified_file_enqueues_scan` | Catalog row with size_bytes=1000; on-disk size 2000 → enqueue with `reason='size_changed'`. |
| `test_moved_file_updates_path` | Catalog row at `/a/x.mp4` with `content_hash=H`; filesystem has `/b/x.mp4` with same hash; `/a/x.mp4` is gone → `videos.path` UPDATE; `video_path_history` row written; **no** scan enqueued; `progress.moved_videos == 1`. AC-1 move path. |
| `test_missing_file_marks_state` | Catalog row at `/a/lost.mp4`; file is gone → `state='missing'`; `progress.removed_videos == 1`. |
| `test_single_flight_drops_concurrent_tick` | Two `run_sweep_job` calls in parallel for the same library → first inserts the `library_sweeps` row; second hits the partial-unique violation and returns without raising. AC-2. |
| `test_sweep_records_telemetry_row` | After completion, exactly one `library_sweeps` row with `finished_at IS NOT NULL`, `scanned`, `new_videos`, etc. matching the run. AC-4. |
| `test_sweep_progress_pushed_to_processing_jobs` | After 2 s of running, `processing_jobs.processed_seconds` reflects the file count. |
| `test_per_library_interval_overrides_default` | Library A `sweep_interval_sec=300`, Library B `sweep_interval_sec=21600` → scheduler enqueues A every 5 min; B every 6 h. AC-3. |
| `test_rehash_mode_recomputes_hash` | `rehash=True` payload → fast-path bypassed; every file hashed. (Story 9.6 also exercises this; this test ensures the runner respects the flag.) |
| `test_size_mtime_match_but_hash_changed_misses_when_no_rehash` | Documents the known limitation: a tool that rewrote a file in place preserving size+mtime is missed unless `?rehash=true`. |

### 5.3 Performance gate

| Test | Target |
|---|---|
| `test_sweep_100k_files_under_30s` | 100,000-file fixture (mostly hard-linked from a template) on local SSD; `state` mostly cached → completes in < 30 s. AC's perf bar in §6 of the story. |
| `test_sweep_walks_at_3000_paths_per_sec_minimum` | Throughput probe. |

### 5.4 Cross-dialect parity

Same parametrized fixture used in Story 6.1: every test in §5.2 runs
once on Postgres and once on SQLite. The `pg_try_advisory_xact_lock`
case is replaced in SQLite with the partial-unique index (already the
durable guard); the test asserts the same drop-on-conflict behaviour.

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| `size+mtime` match but content changed (rare) | Missed. The fast path skips. The user must `POST /api/libraries/{id}/scan?rehash=true` (Story 9.6). | `test_size_mtime_match_but_hash_changed_misses_when_no_rehash` |
| NAS that takes 30 s to wake up | The first `os.scandir` blocks; the watcher buffers events. The sweep job's `last_heartbeat_at` keeps it from being reaped (heartbeat task in §4.4 ticks every second). If the wake takes longer than the reaper's stale threshold (default 60 s), the job is reclaimed; the partial-unique index allows the next attempt to retake the row. | Documented; reaper interaction tested by Epic 6 Story 6.6. |
| Two libraries with overlapping roots | Story 9.16 rejects this at create. AC: this story does not handle it — the assumption is overlap-free roots. A runtime overlap (mount change) is detected and warned by Story 9.16 AC-4; the sweep continues. | Cross-story; defense in depth. |
| Symlink loop in a root | `walker._iter` tracks `(dev, inode)` and skips re-entry. | `test_sweep_runner_does_not_infinite_loop_on_symlink` |
| Permission denied on a directory | `os.scandir` raises; the walker swallows and continues; the directory contents are missed but the sweep records `errors_jsonb=[{"path":..., "error":"PermissionError"}]`. | `test_sweep_records_permission_errors` |
| Catalog row with `content_hash IS NULL` (very early scan) | Excluded from `catalog_by_hash`; new-but-same-content files are enqueued as scan jobs. The next probe stage fills the hash, and the next sweep correctly detects further moves. | Documented; not a hot test. |
| Moved file whose old path is *also* on disk (copy, not move) | The walker visits both. The first one becomes `visited`. The second one matches the hash, the old path isn't in `visited`, but the row is now pointing to the second path. Actually this case requires care: when the walker hits the *original* path first, it takes the fast path (size+mtime match); when it hits the *copy* path, the hash matches, the old path *is* in the catalog and *not* in `visited` (because we visit asynchronously?). Resolution: we only treat it as a move when the original path is *missing on disk now*. The implementation in §4.3 checks `moved.path not in visited`; if both are visited, the second new path is enqueued as a fresh scan. The architectural §8.1 unique `content_hash` will cause that scan to dedup as a duplicate (Story 9.4 handles). | `test_sweep_two_paths_same_hash_records_duplicate` |
| Library deleted mid-sweep | The dispatcher's `pause_requested`/`cancel_requested` flag is honored at each batch boundary. The `library_sweeps` row's FK cascade eventually removes the partial row. | Story 6.4 covers cancellation; this story polls `cancel_requested` between batches. |
| Sweep finishes after the next interval has elapsed | The next scheduler tick checks `MAX(started_at)`; even though the previous sweep took 7 h on a 6 h interval, the next tick fires immediately. No multi-sweep pile-up because of the partial-unique index. | `test_long_sweep_doesnt_stack` |

## 7. Configuration knobs

Read from `effective` per Story 9.1:

| Key | Default | Used by |
|---|---|---|
| `sweep_interval_sec` | 21600 (6 h) | Scheduler |
| `sweep_concurrency` | 1 | Future; this story always runs one sweep per library |
| `hash_timeout_sec` | 30.0 | Story 9.4's hasher (forwarded) |
| `ignore_globs` | `[]` | Walker |

## 8. Dependencies

| Dep | Version | Why |
|---|---|---|
| Story 6.1 `enqueue` | required | Idempotent enqueue with the unique-live partial index. |
| Story 9.4 `blake3_4mib` | required | Hash function for the move case. |
| Story 9.5 `IgnoreMatcher` | required | Pre-walker filter. |
| `os.scandir` (stdlib) | py3.5+ | Iterative walk; orders of magnitude faster than `os.walk`. |
| `asyncpg`/`aiosqlite` | already pinned | Catalog read; progress write. |

No new external deps.

## 9. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/sweep/` package created.
- [ ] `SweepScheduler.run()` ticks every 60 s and enqueues at priority 200.
- [ ] `run_sweep_job` is the dispatcher target for `stage='scan'` jobs whose payload has `library_id` but not `video_id`.
- [ ] Single-flight enforced by the `library_sweeps_one_in_flight` partial-unique index; second concurrent insert quietly returns.
- [ ] `videos.state='missing'` is set for catalog rows the walker did not see (lowercase canonical FSM; `missing` added to the CHECK constraint by plan-09-06's FSM-extension migration).

**Migration**
- [ ] `0032_library_sweeps.sql` creates the table, the lookup index, and the partial-unique index.
- [ ] SQLite variant applies cleanly.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: size+mtime fast path; new files enqueued; moves update path; missing files transition to `missing`.
- [ ] AC-2: a tick during an in-flight sweep is dropped (no second `library_sweeps` row).
- [ ] AC-3: per-library `sweep_interval_sec` overrides the default; `0` disables.
- [ ] AC-4: every sweep writes a `library_sweeps` row.

**Performance**
- [ ] 100,000-file fixture on local SSD completes in < 30 s.
- [ ] Stats query on `library_sweeps` (Story 9.7's `last_sweep`) is single-row, < 5 ms.

**Observability**
- [ ] Counter `maktaba_sweep_runs_total{library_id, reason, outcome}` exported (`outcome ∈ done|skipped_locked|errored`).
- [ ] Histogram `maktaba_sweep_duration_seconds{library_id}`.
- [ ] `LIBRARY_SWEEP_DONE` channel constant published; the API forwards to WS clients.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.3.
- [ ] Operations doc explains how to disable sweeps per library (`sweep_interval_sec: 0`) and the rehash escape hatch (Story 9.6).
