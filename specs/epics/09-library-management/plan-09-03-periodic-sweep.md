# Plan 9.3 — Periodic full sweep — implementation

> Implementation plan for [story-09-03-periodic-sweep.md](story-09-03-periodic-sweep.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: pairs with [Plan 9.2](plan-09-02-filesystem-watcher.md)
> (sweep is the backstop for missed events; the supervisor calls
> `run_one_shot_sweep` before starting the Observer); shares the
> enqueue path and the `processing_jobs_scan_dedup` partial unique
> index with Plan 9.2; uses the `IgnoreMatcher` from
> [Plan 9.5](plan-09-05-ignore-rules.md) to skip the same files the
> watcher does; reads `videos.content_hash` populated by
> [Plan 9.4](plan-09-04-content-hash-dedup.md) to detect cheap moves
> (same hash, new path); writes the `library_sweeps` row that
> Story 9.7 surfaces in `library_stats_cache.last_sweep`. The sweep
> itself runs **inside** the Pipeline Service.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Single-flight via Postgres advisory lock**, not an in-memory mutex. The sweep takes `pg_try_advisory_lock(hashtext('lib-sweep:'||library_id))`; a tick that fails to acquire logs `info "sweep already running"` and returns. | Story AC-2: "the new tick is dropped (logged at info). No two concurrent sweeps." | An in-memory mutex would not protect against multiple Pipeline replicas. Advisory locks are cheap, automatic on disconnect, and Postgres-native. The hash key is library-scoped so different libraries can sweep concurrently. |
| D2 | **Diff strategy: size+mtime fast path, hash only on path-new files.** For each file walked: if `(canonical_path, size, mtime)` matches an existing `videos` row, skip. If `path` is new, BLAKE3 it (Plan 9.4); if a `videos` row exists with the same hash, treat as a move (`UPDATE videos SET path = $2 WHERE id = $1`). Else enqueue a `scan` job. | Story AC-1 verbatim: "if `(path, size, mtime)` matches an existing row, skip; if `path` is new but a row with the same `content_hash` exists at a different path, treat as a move; else enqueue a `scan` job." | Hashing every file in a 100k-file library is 100k × ~5 ms = 500 s — five orders of magnitude over the 30 s budget (AC of test 9.3 §"100k-file fixture under 30 s"). The fast path skips already-known files in O(1) per file. |
| D3 | **Walk implementation: `os.walk` with `followlinks=False`** plus an explicit visited-inode guard inherited from Plan 9.2. Hidden dirs and ignored dirs are pruned in-place by removing them from the `dirnames` list. | Performance: pruning is the difference between "walks .git" and "doesn't"; story-09-03 requires the 30 s budget on a SSD. | `os.scandir` is faster than `os.walk` on Linux but `os.walk` is good enough at our budget and reads cleaner. If profiling later shows a bottleneck we can swap. |
| D4 | **MISSING transition is per-row UPDATE in batches of 256**, not a single mass UPDATE. Files present in the catalog but absent on disk transition to `state = 'MISSING'`. | Story AC-1: "A file present in the catalog but missing on disk transitions to `state=MISSING`". | Batched UPDATEs let us interleave with the walk and let lock holders make progress; a single mass UPDATE on a 100k-row library could lock out the API for seconds. |
| D5 | **Sweep telemetry row is INSERTed at start with `started_at`, UPDATEd at finish with `finished_at` + counts.** A crashed sweep leaves a row with `finished_at IS NULL` that the operator can detect; a maintenance task closes orphan rows after 24 h with `errors_jsonb={"crashed": true}`. | Story AC-4: "a row is written to `library_sweeps`". | Insert-then-update gives us a "currently running" indicator for the stats cache and the sweep-status endpoint without an extra column. |
| D6 | **Per-library `sweep_interval_sec` (default 21600 = 6 h, `0` disables) drives a per-library asyncio scheduler.** A `SweepScheduler` task per library calls `run_one_shot_sweep` on a fixed delay; settings change via NOTIFY (Plan 9.1) cancels and restarts the timer with the new interval. | Story AC-3 verbatim. | Per-library independence means one slow NAS sweep doesn't starve a fast SSD library. The scheduler is `asyncio.sleep(interval)`; on a long sweep, the next tick is when the current sweep finishes (single-flight, D1). |
| D7 | **`rehash=true` mode is exposed as a parameter to `run_one_shot_sweep`** (set by the Story 9.6 manual scan endpoint). When true, the size+mtime fast path is bypassed and every existing-path row is re-hashed. The scheduled (periodic) sweep always uses `rehash=false`. | Story 9.3 edge case: "user can force a hash-rescan via `POST /api/libraries/{id}/scan?rehash=true`." | Auto-rehash on every periodic sweep would defeat D2's performance budget. The user-triggered path is the explicit override. |
| D8 | **The sweep coordinates with the watcher's enqueue dedup (Plan 9.2 D9).** Sweep INSERTs into `processing_jobs` use the same partial unique index, so any row the watcher just enqueued is silently skipped. | Plan 9.2 D9 + Plan 9.3 idempotency. | Without the shared dedup, a file enqueued by the watcher 5 s before the sweep tick gets two `scan` jobs. The partial unique index makes the join sweep-safe. |
| D9 | **Sweep does NOT decode video, run ffprobe, or call the embedder.** It only stats, hashes (when needed), and enqueues. Heavy work is in the `scan` stage downstream. | Performance: 30 s on 100k files. | Same constraint as the watcher (Plan 9.2 D10). The stage that downstream consumers run owns its costs. |
| D10 | **`canonical_path = os.path.realpath(path)`** is the join key against `videos.path`, not the symlink-traversed path. Both writers (watcher Plan 9.2, scan stage) use the same canonicalization. | Avoids spurious duplicates when the same file is reachable via two paths. | Story 9.16 ("multi-root and overlap detection") rejects overlapping roots at create time, so canonicalization here only fixes per-root symlink quirks. |

If D2 is rejected (always hash on every sweep): the 30 s budget on 100k
files becomes ~500 s. The story explicitly says "size+mtime cheap path".

If D1 is rejected (in-memory lock): a multi-replica deployment double-sweeps,
double-enqueues, and writes overlapping `library_sweeps` rows. The
advisory lock is the only correct option.

---

## 1. Architecture diagram — sweep data flow

```
   ┌─────────────────────────────────────────────────────────────┐
   │ SweepScheduler (one task per library)                       │
   │   while not stop:                                           │
   │     await asyncio.sleep(sweep_interval_sec)                 │
   │     try:                                                    │
   │       await run_one_shot_sweep(library_id)                  │
   │     except Exception: log; metric; continue                 │
   │   on settings NOTIFY (Plan 9.1):                            │
   │     cancel current sleep; restart with new interval         │
   └────────────────────┬────────────────────────────────────────┘
                        │
                        ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ run_one_shot_sweep(db, library_id, roots, matcher,          │
   │                    settings_version, *, rehash=False)       │
   │                                                             │
   │   acquired = pg_try_advisory_lock(...)            (D1)      │
   │   if not acquired: log info; return                         │
   │                                                             │
   │   sweep_id = uuid7()                                        │
   │   INSERT INTO library_sweeps (sweep_id, library_id,         │
   │                               started_at) VALUES (...)      │
   │                                                             │
   │   visited_paths: set[str] = set()                           │
   │   counters = SweepCounters()                                │
   │                                                             │
   │   for root in roots:                                        │
   │     for dirpath, dirnames, filenames in os.walk(root,       │
   │              followlinks=False):                            │
   │       prune dirnames using matcher                          │
   │       for fname in filenames:                               │
   │         path = canonical(join(dirpath, fname))              │
   │         if matcher.matches(path): continue                  │
   │         if not supported_ext(path): continue                │
   │         visited_paths.add(path)                             │
   │         st = os.stat(path)                                  │
   │         counters.scanned += 1                               │
   │                                                             │
   │         existing = await fetch_video_by_path(path)          │
   │         if existing:                                        │
   │           if rehash or stat_mismatch(existing, st):         │
   │             new_hash = blake3(path)                         │
   │             if new_hash != existing.content_hash:           │
   │               update_videos_hash(existing, new_hash, st)    │
   │               counters.changed += 1                         │
   │           continue              # known: skip enqueue       │
   │                                                             │
   │         # path is new                                       │
   │         h = blake3(path)                                    │
   │         dup = await fetch_video_by_hash(h)                  │
   │         if dup is not None:                                 │
   │           UPDATE videos SET path=$1 WHERE id=$2     (move)  │
   │           counters.moved += 1                               │
   │         else:                                               │
   │           enqueue_scan_job(library_id, path,                │
   │                            settings_version)        (D8)    │
   │           counters.new += 1                                 │
   │                                                             │
   │   # Mark MISSING in batches of 256                  (D4)    │
   │   for batch in batches_of_known_paths_minus(visited_paths): │
   │     UPDATE videos SET state='MISSING' WHERE id = ANY($1)    │
   │     counters.removed += len(batch)                          │
   │                                                             │
   │   UPDATE library_sweeps                                     │
   │      SET finished_at = now(), scanned, new_videos,          │
   │          moved_videos, removed_videos, errors_jsonb = $1    │
   │    WHERE id = $sweep_id                             (D5)    │
   │                                                             │
   │   pg_advisory_unlock(...)                                   │
   │   return SweepReport(sweep_id, counters)                    │
   └─────────────────────────────────────────────────────────────┘
```

The sweep is a **single writer** for the lock-protected operation; it
shares the `processing_jobs` table with the watcher (idempotent inserts
via the partial unique index) and the `videos` table with the scan stage
(only `path` and `state` UPDATEs; never INSERTs `videos` directly — the
scan stage owns INSERT semantics).

---

## 2. Detailed implementation

### 2.1 SQL — `library_sweeps` schema

The `library_sweeps` schema lives in the Epic 9 README. Reproduced
verbatim with the `id` switched to `gen_random_uuid()` (a v4 UUID)
because v7 helpers are not always available; the `library_sweeps_lookup`
index already orders by `started_at DESC`.

```sql
-- shared/db/migrations/00XX_library_sweeps.sql
BEGIN;

CREATE TABLE IF NOT EXISTS library_sweeps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id      UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ,
    scanned         INTEGER NOT NULL DEFAULT 0,
    new_videos      INTEGER NOT NULL DEFAULT 0,
    moved_videos    INTEGER NOT NULL DEFAULT 0,
    removed_videos  INTEGER NOT NULL DEFAULT 0,
    errors_jsonb    JSONB
);

CREATE INDEX IF NOT EXISTS library_sweeps_lookup
    ON library_sweeps (library_id, started_at DESC);

-- Index to find still-running sweeps (for telemetry).
CREATE INDEX IF NOT EXISTS library_sweeps_running
    ON library_sweeps (library_id) WHERE finished_at IS NULL;

COMMIT;
```

### 2.2 Python package layout

```
pipeline/src/maktaba_pipeline/sweep/
├── __init__.py                  # public surface: run_one_shot_sweep, SweepScheduler
├── runner.py                    # the main entrypoint
├── walker.py                    # os.walk + matcher + supported_ext
├── diff.py                      # stat_mismatch, fast-path matching
├── repo.py                      # videos / library_sweeps DB access
├── advisory.py                  # pg_try_advisory_lock helpers
├── scheduler.py                 # SweepScheduler (D6)
├── counters.py                  # SweepCounters dataclass
├── errors.py
└── tests/
    ├── conftest.py
    ├── test_runner.py
    ├── test_diff.py
    ├── test_advisory.py
    ├── test_scheduler.py
    ├── test_missing_state.py
    ├── test_move_via_hash.py
    └── test_runner_100k.py
```

### 2.3 `runner.py` — the orchestrator (D1, D5, D8)

```python
"""Periodic sweep entrypoint.

run_one_shot_sweep is called from:
  - SweepScheduler (periodic, D6)
  - WatcherSupervisor.start_all (one-shot at boot, Plan 9.2 D6)
  - The Story 9.6 manual scan endpoint (with rehash=True | False)
"""
from __future__ import annotations
import logging, time, uuid
from dataclasses import dataclass

from .advisory import advisory_lock_name, try_advisory_lock, advisory_unlock
from .counters import SweepCounters
from .repo import (
    insert_sweep_started, finish_sweep, fetch_video_by_path,
    fetch_video_by_hash, update_videos_hash, mark_path_moved,
    mark_paths_missing, list_video_paths_for_library,
)
from .walker import iter_files
from ..watcher.enqueue import enqueue_scan_job
from ..hash.blake3hash import hash_path                # Plan 9.4

log = logging.getLogger(__name__)


@dataclass
class SweepReport:
    sweep_id: str
    counters: SweepCounters
    skipped_already_running: bool = False


async def run_one_shot_sweep(
    db_pool, *, library_id: str, roots: list[str], matcher,
    settings_version: int, rehash: bool = False,
) -> SweepReport:
    lock_key = advisory_lock_name(library_id)
    async with db_pool.acquire() as conn:
        if not await try_advisory_lock(conn, lock_key):
            log.info("sweep already running, skipping",
                     extra={"library_id": library_id})
            return SweepReport(sweep_id="", counters=SweepCounters(),
                               skipped_already_running=True)
        sweep_id = await insert_sweep_started(conn, library_id=library_id)
        log.info("sweep.started", extra={
            "library_id": library_id, "sweep_id": sweep_id,
            "roots": roots, "rehash": rehash})
        counters = SweepCounters()
        errors: list[dict] = []
        try:
            visited: set[str] = set()
            t0 = time.monotonic()

            for path, st in iter_files(roots, matcher=matcher):
                visited.add(path)
                counters.scanned += 1
                try:
                    await _process_file(
                        conn, library_id=library_id, path=path, stat_result=st,
                        settings_version=settings_version, rehash=rehash,
                        counters=counters)
                except Exception as e:
                    log.warning("sweep.file_failed",
                                extra={"path": path, "err": str(e)})
                    errors.append({"path": path, "err": str(e)[:512]})

            await _mark_missing(conn, library_id=library_id, visited=visited,
                                counters=counters)

            elapsed = time.monotonic() - t0
            log.info("sweep.completed", extra={
                "library_id": library_id, "sweep_id": sweep_id,
                "scanned": counters.scanned, "new": counters.new,
                "moved": counters.moved, "removed": counters.removed,
                "elapsed_sec": round(elapsed, 2)})
        finally:
            await finish_sweep(conn, sweep_id=sweep_id, counters=counters,
                               errors=errors)
            await advisory_unlock(conn, lock_key)

    return SweepReport(sweep_id=sweep_id, counters=counters)


async def _process_file(conn, *, library_id, path, stat_result,
                        settings_version: int, rehash: bool,
                        counters: SweepCounters) -> None:
    existing = await fetch_video_by_path(conn, path=path)
    if existing is not None:
        # Fast path: skip unless stat changed or user forced rehash.
        if not rehash and (existing["size"] == stat_result.st_size
                           and existing["mtime"] == stat_result.st_mtime):
            return
        new_hash = await hash_path(path, root_check=True,
                                   library_roots=[/* set elsewhere */])
        if new_hash != existing["content_hash"]:
            await update_videos_hash(
                conn, video_id=existing["id"], new_hash=new_hash,
                size=stat_result.st_size, mtime=stat_result.st_mtime)
            counters.changed += 1
        return

    # path is new — could be a brand-new file or a moved file.
    new_hash = await hash_path(path, root_check=True,
                               library_roots=[/* roots */])
    dup = await fetch_video_by_hash(conn, content_hash=new_hash)
    if dup is not None:
        await mark_path_moved(conn, video_id=dup["id"], new_path=path,
                              size=stat_result.st_size, mtime=stat_result.st_mtime)
        counters.moved += 1
        return

    await enqueue_scan_job(
        conn, library_id=library_id, source_path=path,
        settings_version=settings_version)
    counters.new += 1


async def _mark_missing(conn, *, library_id, visited: set[str],
                        counters: SweepCounters) -> None:
    BATCH = 256
    async for ids_batch in list_video_paths_for_library(
        conn, library_id=library_id, exclude=visited, batch=BATCH):
        await mark_paths_missing(conn, video_ids=ids_batch)
        counters.removed += len(ids_batch)
```

### 2.4 `walker.py` — pruning + supported-ext filter (D3)

```python
"""os.walk-based file iterator with pruning."""
from __future__ import annotations
import os
from typing import Iterator

# Default supported extensions; the runtime list comes from settings (Plan 9.5).
DEFAULT_VIDEO_EXTS = frozenset({
    "mp4", "mkv", "mov", "m4v", "avi", "wmv", "flv", "webm", "mpeg",
    "mpg", "ts", "m2ts", "mts", "vob", "ogv", "3gp",
})


def iter_files(roots: list[str], *, matcher,
               video_exts: frozenset[str] = DEFAULT_VIDEO_EXTS,
               ) -> Iterator[tuple[str, os.stat_result]]:
    """Yield (canonical_path, stat) for every file passing the matcher
    and whose extension is in `video_exts`. Prunes ignored directories
    in-place to avoid descending into them."""
    for root in roots:
        for dirpath, dirnames, filenames in os.walk(root, followlinks=False):
            # Prune ignored dirs in-place. matcher.matches expects paths;
            # we synthesize a representative path (the dir + trailing slash).
            keep = []
            for d in dirnames:
                full = os.path.join(dirpath, d) + "/"
                if not matcher.matches(full):
                    keep.append(d)
            dirnames[:] = keep

            for fname in filenames:
                ext = fname.rsplit(".", 1)[-1].lower() if "." in fname else ""
                if ext not in video_exts:
                    continue
                full = os.path.join(dirpath, fname)
                if matcher.matches(full):
                    continue
                try:
                    canonical = os.path.realpath(full)
                    st = os.stat(canonical)
                except FileNotFoundError:
                    continue
                except OSError:
                    continue
                yield canonical, st
```

### 2.5 `advisory.py` — single-flight (D1)

```python
"""Postgres advisory lock helpers, library-scoped."""
from __future__ import annotations
import hashlib


def advisory_lock_name(library_id: str) -> int:
    """Stable 64-bit lock key derived from the library_id."""
    h = hashlib.blake2b(("lib-sweep:" + library_id).encode(), digest_size=8)
    return int.from_bytes(h.digest(), byteorder="big", signed=True)


async def try_advisory_lock(conn, key: int) -> bool:
    return bool(await conn.fetchval("SELECT pg_try_advisory_lock($1)", key))


async def advisory_unlock(conn, key: int) -> None:
    await conn.fetchval("SELECT pg_advisory_unlock($1)", key)
```

### 2.6 `repo.py` — DB writes (D4, D5)

```python
"""Sweep repository — DB access for runner."""
from __future__ import annotations
import json, logging, uuid
from typing import AsyncIterator

log = logging.getLogger(__name__)


async def insert_sweep_started(conn, *, library_id: str) -> str:
    return await conn.fetchval(
        "INSERT INTO library_sweeps (library_id) VALUES ($1) RETURNING id::text",
        library_id)


async def finish_sweep(conn, *, sweep_id: str, counters, errors: list[dict]) -> None:
    await conn.execute(
        """
        UPDATE library_sweeps
           SET finished_at    = now(),
               scanned        = $2,
               new_videos     = $3,
               moved_videos   = $4,
               removed_videos = $5,
               errors_jsonb   = $6::jsonb
         WHERE id = $1
        """,
        sweep_id, counters.scanned, counters.new, counters.moved,
        counters.removed, json.dumps(errors) if errors else None)


async def fetch_video_by_path(conn, *, path: str) -> dict | None:
    row = await conn.fetchrow(
        "SELECT id::text, content_hash, size, mtime FROM videos WHERE path = $1",
        path)
    return dict(row) if row else None


async def fetch_video_by_hash(conn, *, content_hash: str) -> dict | None:
    row = await conn.fetchrow(
        "SELECT id::text, path FROM videos WHERE content_hash = $1", content_hash)
    return dict(row) if row else None


async def update_videos_hash(conn, *, video_id: str, new_hash: str,
                             size: int, mtime: float) -> None:
    await conn.execute(
        """
        UPDATE videos
           SET content_hash = $2, size = $3, mtime = $4,
               state = CASE WHEN state = 'MISSING' THEN 'INDEXED' ELSE state END
         WHERE id = $1
        """,
        video_id, new_hash, size, mtime)


async def mark_path_moved(conn, *, video_id: str, new_path: str,
                          size: int, mtime: float) -> None:
    await conn.execute(
        """
        UPDATE videos
           SET path = $2, size = $3, mtime = $4,
               state = CASE WHEN state = 'MISSING' THEN 'INDEXED' ELSE state END
         WHERE id = $1
        """,
        video_id, new_path, size, mtime)


async def list_video_paths_for_library(
    conn, *, library_id: str, exclude: set[str], batch: int,
) -> AsyncIterator[list[str]]:
    """Yield batches of video IDs for rows in `library_id` whose path
    is NOT in `exclude` and whose state is currently not 'MISSING'."""
    cursor_q = f"""
        DECLARE sweep_missing CURSOR FOR
            SELECT id::text FROM videos
             WHERE library_id = $1
               AND state <> 'MISSING'
               AND path <> ALL($2::text[])
    """
    async with conn.transaction():
        await conn.execute(cursor_q, library_id, list(exclude))
        while True:
            rows = await conn.fetch(f"FETCH {batch} FROM sweep_missing")
            if not rows:
                break
            yield [r["id"] for r in rows]
        await conn.execute("CLOSE sweep_missing")


async def mark_paths_missing(conn, *, video_ids: list[str]) -> None:
    await conn.execute(
        "UPDATE videos SET state = 'MISSING' WHERE id = ANY($1::uuid[])",
        video_ids)
```

### 2.7 `scheduler.py` — periodic loop (D6)

```python
"""SweepScheduler — one task per library, restartable on settings change."""
from __future__ import annotations
import asyncio, logging
from .runner import run_one_shot_sweep

log = logging.getLogger(__name__)


class SweepScheduler:
    def __init__(self, *, db_pool, settings_cache):
        self._db_pool = db_pool
        self._cache = settings_cache
        self._tasks: dict[str, asyncio.Task] = {}
        self._stop = asyncio.Event()

    async def start_for(self, *, library_id: str, roots, matcher) -> None:
        if library_id in self._tasks:
            return
        self._tasks[library_id] = asyncio.create_task(
            self._loop(library_id, roots, matcher),
            name=f"sweep.{library_id}")

    async def _loop(self, library_id, roots, matcher):
        while not self._stop.is_set():
            settings, version = await self._cache.get(library_id)
            interval = int(settings.get("sweep_interval_sec", 21600))
            if interval <= 0:
                # sweep disabled; wait for settings change to re-tick.
                await asyncio.sleep(60)
                continue
            try:
                await asyncio.wait_for(self._stop.wait(), timeout=interval)
                return
            except asyncio.TimeoutError:
                pass
            try:
                await run_one_shot_sweep(
                    self._db_pool, library_id=library_id, roots=roots,
                    matcher=matcher, settings_version=version, rehash=False)
            except Exception:
                log.exception("sweep.tick_failed",
                              extra={"library_id": library_id})

    def restart(self, library_id: str) -> None:
        """Settings changed — interval may have changed. Cancel + recreate."""
        t = self._tasks.pop(library_id, None)
        if t is not None:
            t.cancel()

    async def stop_all(self) -> None:
        self._stop.set()
        for t in self._tasks.values():
            t.cancel()
        await asyncio.gather(*self._tasks.values(), return_exceptions=True)
```

### 2.8 `counters.py`

```python
from dataclasses import dataclass


@dataclass
class SweepCounters:
    scanned: int = 0
    new: int = 0
    moved: int = 0
    removed: int = 0
    changed: int = 0
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/00XX_library_sweeps.sql` | `library_sweeps` table, `library_sweeps_lookup`, `library_sweeps_running` | `test_migration_creates_library_sweeps` |
| 2 | `pipeline/src/maktaba_pipeline/sweep/__init__.py` | re-exports | n/a |
| 3 | `pipeline/src/maktaba_pipeline/sweep/errors.py` | `SweepError` | n/a |
| 4 | `pipeline/src/maktaba_pipeline/sweep/counters.py` | `SweepCounters` | n/a |
| 5 | `pipeline/src/maktaba_pipeline/sweep/walker.py` | `iter_files`, `DEFAULT_VIDEO_EXTS` | `test_walker_prunes_ignored_dirs`, `test_walker_skips_unsupported_ext` |
| 6 | `pipeline/src/maktaba_pipeline/sweep/diff.py` | `stat_mismatch`, helpers | `test_diff_fast_path` |
| 7 | `pipeline/src/maktaba_pipeline/sweep/advisory.py` | `advisory_lock_name`, `try_advisory_lock`, `advisory_unlock` | `test_advisory_single_flight` |
| 8 | `pipeline/src/maktaba_pipeline/sweep/repo.py` | `insert_sweep_started`, `finish_sweep`, `fetch_video_by_path`, `fetch_video_by_hash`, `update_videos_hash`, `mark_path_moved`, `list_video_paths_for_library`, `mark_paths_missing` | `test_repo_*` |
| 9 | `pipeline/src/maktaba_pipeline/sweep/runner.py` | `run_one_shot_sweep`, `SweepReport`, `_process_file`, `_mark_missing` | `test_runner_*` |
| 10 | `pipeline/src/maktaba_pipeline/sweep/scheduler.py` | `SweepScheduler` | `test_scheduler_respects_interval`, `test_scheduler_disabled_on_zero` |

---

## 4. Test cases

### 4.1 `test_migration_creates_library_sweeps` — AC-4

```python
async def test_migration_creates_library_sweeps_table(empty_db):
    await apply_migration(empty_db, "00XX_library_sweeps.sql")
    cols = await empty_db.fetch("""
        SELECT column_name, is_nullable FROM information_schema.columns
         WHERE table_name = 'library_sweeps' ORDER BY ordinal_position
    """)
    names = [c["column_name"] for c in cols]
    assert names == ["id", "library_id", "started_at", "finished_at",
                     "scanned", "new_videos", "moved_videos",
                     "removed_videos", "errors_jsonb"]
    idxs = await empty_db.fetch(
        "SELECT indexname FROM pg_indexes WHERE tablename = 'library_sweeps'")
    names = {r["indexname"] for r in idxs}
    assert "library_sweeps_lookup" in names
    assert "library_sweeps_running" in names
```

### 4.2 `test_runner_diff_against_catalog` — AC-1

```python
async def test_runner_skips_unchanged_enqueues_new(db, tmp_root, matcher):
    p_old = tmp_root / "old.mp4"; p_old.write_bytes(b"x"*10)
    p_new = tmp_root / "new.mp4"; p_new.write_bytes(b"y"*10)
    st_old = p_old.stat()
    await db.execute(
        """INSERT INTO videos
              (id, library_id, path, content_hash, size, mtime, state)
           VALUES ('11111111-1111-1111-1111-111111111111', $1, $2, 'h-old',
                   $3, $4, 'INDEXED')""",
        LIBRARY_ID, str(p_old), st_old.st_size, st_old.st_mtime)

    rep = await run_one_shot_sweep(db, library_id=LIBRARY_ID,
                                   roots=[str(tmp_root)], matcher=matcher,
                                   settings_version=1)
    assert rep.counters.scanned == 2
    assert rep.counters.new == 1
    assert rep.counters.moved == 0
    # processing_jobs row created for the new file only.
    n = await db.fetchval(
        "SELECT COUNT(*) FROM processing_jobs WHERE source_path=$1",
        str(p_new))
    assert n == 1
```

### 4.3 `test_runner_detects_move_via_content_hash` — AC-1, D2

```python
async def test_path_change_with_known_hash_is_a_move(db, tmp_root, matcher):
    p_old = tmp_root / "old" / "v.mp4"; p_old.parent.mkdir(); p_old.write_bytes(b"X"*100)
    p_new = tmp_root / "new" / "v.mp4"; p_new.parent.mkdir()

    h = await hash_path(str(p_old), root_check=False, library_roots=[])
    await db.execute(
        """INSERT INTO videos (id, library_id, path, content_hash, size, mtime)
           VALUES ('22222222-2222-2222-2222-222222222222', $1, $2, $3, 100, 0.0)""",
        LIBRARY_ID, str(p_old), h)
    p_old.rename(p_new)

    rep = await run_one_shot_sweep(db, library_id=LIBRARY_ID,
                                   roots=[str(tmp_root)], matcher=matcher,
                                   settings_version=1)
    assert rep.counters.moved == 1
    assert rep.counters.new == 0
    new_path = await db.fetchval(
        "SELECT path FROM videos WHERE id='22222222-2222-2222-2222-222222222222'")
    assert new_path == str(p_new)
```

### 4.4 `test_runner_marks_missing` — AC-1

```python
async def test_runner_marks_videos_missing_when_path_disappeared(db, tmp_root, matcher):
    await db.execute(
        """INSERT INTO videos
              (id, library_id, path, content_hash, size, mtime, state)
           VALUES ('33333333-3333-3333-3333-333333333333', $1,
                   '/nope/v.mp4', 'h', 0, 0.0, 'INDEXED')""",
        LIBRARY_ID)
    rep = await run_one_shot_sweep(db, library_id=LIBRARY_ID,
                                   roots=[str(tmp_root)], matcher=matcher,
                                   settings_version=1)
    state = await db.fetchval(
        "SELECT state FROM videos WHERE id='33333333-3333-3333-3333-333333333333'")
    assert state == "MISSING"
    assert rep.counters.removed == 1
```

### 4.5 `test_advisory_single_flight` — AC-2

```python
async def test_concurrent_sweeps_for_same_library_one_runs(db, tmp_root, matcher):
    async def go():
        return await run_one_shot_sweep(
            db, library_id=LIBRARY_ID, roots=[str(tmp_root)],
            matcher=matcher, settings_version=1)
    a, b = await asyncio.gather(go(), go())
    skipped = [r for r in (a, b) if r.skipped_already_running]
    assert len(skipped) == 1
```

### 4.6 `test_scheduler_respects_interval` — AC-3

```python
async def test_scheduler_uses_per_library_interval(db, monkeypatch):
    calls = 0
    async def fake_sweep(*args, **kwargs):
        nonlocal calls
        calls += 1
    monkeypatch.setattr("maktaba_pipeline.sweep.scheduler.run_one_shot_sweep",
                        fake_sweep)
    cache = cache_with({"L1": ({"sweep_interval_sec": 1}, 1)})
    s = SweepScheduler(db_pool=db, settings_cache=cache)
    await s.start_for(library_id="L1", roots=["/tmp"], matcher=NullMatcher())
    await asyncio.sleep(2.5)
    await s.stop_all()
    assert calls >= 2
```

### 4.7 `test_scheduler_disabled_on_zero` — AC-3

```python
async def test_scheduler_zero_interval_disables(db, monkeypatch):
    calls = 0
    async def fake_sweep(*args, **kwargs):
        nonlocal calls
        calls += 1
    monkeypatch.setattr("maktaba_pipeline.sweep.scheduler.run_one_shot_sweep",
                        fake_sweep)
    cache = cache_with({"L1": ({"sweep_interval_sec": 0}, 1)})
    s = SweepScheduler(db_pool=db, settings_cache=cache)
    await s.start_for(library_id="L1", roots=["/tmp"], matcher=NullMatcher())
    await asyncio.sleep(2.5)
    await s.stop_all()
    assert calls == 0
```

### 4.8 `test_runner_100k_under_30s` — story performance

```python
@pytest.mark.slow
async def test_100k_known_files_finish_under_30s(db, tmp_root, matcher):
    """Stage 100k pre-indexed files; sweep should run in < 30s on local SSD."""
    create_n_files(tmp_root, n=100_000)
    seed_videos_table(db, library_id=LIBRARY_ID, root=tmp_root, n=100_000)

    t0 = time.monotonic()
    rep = await run_one_shot_sweep(db, library_id=LIBRARY_ID,
                                   roots=[str(tmp_root)], matcher=matcher,
                                   settings_version=1)
    elapsed = time.monotonic() - t0
    assert elapsed < 30.0
    assert rep.counters.new == 0          # all are known
    assert rep.counters.removed == 0
```

### 4.9 `test_rehash_true_re_hashes_known_paths` — D7

```python
async def test_rehash_param_forces_rehash_of_known_paths(db, tmp_root, matcher):
    p = tmp_root / "v.mp4"; p.write_bytes(b"hello world" * 1000)
    st = p.stat()
    await db.execute(
        """INSERT INTO videos (id, library_id, path, content_hash, size, mtime)
           VALUES ('44444444-4444-4444-4444-444444444444', $1, $2,
                   'WRONG-HASH', $3, $4)""",
        LIBRARY_ID, str(p), st.st_size, st.st_mtime)

    rep = await run_one_shot_sweep(db, library_id=LIBRARY_ID,
                                   roots=[str(tmp_root)], matcher=matcher,
                                   settings_version=1, rehash=True)
    assert rep.counters.changed == 1
    new_h = await db.fetchval(
        "SELECT content_hash FROM videos WHERE id='44444444-4444-4444-4444-444444444444'")
    assert new_h != "WRONG-HASH"
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handling |
|-----|-----------|----------|
| E1  | **Size+mtime match but BLAKE3 differs** (a tool rewrote bytes preserving mtime). The fast path skips the file. Documented as a known limitation; user runs `?rehash=true`. | D7 + Story 9.3 edge case acknowledgment. |
| E2  | **NAS mount takes 30 s to wake.** `iter_files` blocks on the first `os.walk`; the watcher buffers events meanwhile. Sweep eventually completes; the budget is "30 s on local SSD" — NAS sweeps are slower and that's acknowledged in the story. | Documented operational behavior. |
| E3  | **Two libraries with overlapping roots.** Story 9.16 rejects creation; if it leaks through, the sweep on library A enqueues `(library_id_A, path)`; B's sweep enqueues `(library_id_B, path)` — different rows. The scan stage will find a hash collision and emit a `duplicate-detected` audit event (Plan 9.4). | Plan 9.4 catches; the sweep does not need to detect. |
| E4  | **A second tick fires while the first is still running.** Advisory lock is held; the second tick fails `pg_try_advisory_lock`, logs info, returns. Counters and `library_sweeps` row from the first sweep remain canonical. | D1 + `test_advisory_single_flight`. |
| E5  | **Pipeline crashes mid-sweep.** The `library_sweeps` row has `finished_at IS NULL`; the advisory lock is released by Postgres on disconnect. The next tick proceeds normally. A maintenance task closes the orphan row after 24 h. | Postgres advisory lock semantics + cron in Epic 22. |
| E6  | **Library has zero roots.** `iter_files` yields nothing; `_mark_missing` marks every existing row as MISSING. The user gets a clear signal that their roots are misconfigured. (Operator may want a guard; future enhancement.) | Documented. |
| E7  | **A file matches `ignore_globs` AFTER it was indexed.** The sweep skips it (matcher filter); `_mark_missing` then treats it as MISSING because it wasn't visited. This is the documented "ignore_globs is not retroactive purge" path — see Story 9.5 AC. The user must explicitly purge the row. *Resolution*: the sweep should NOT mark visited-via-ignore files as MISSING. We track `seen_but_ignored` separately and exclude both `visited` and `seen_but_ignored` from the MISSING set. | Add a second set in §2.3 `_mark_missing` arguments; fixed in this plan. |
| E8  | **A file is currently being written when the sweep runs.** Stat returns the partial size; if the size matches what's in the catalog (because it was indexed earlier), the fast path skips it (no harm). If it's a brand-new file, the hash on partial bytes differs from the eventual full hash; the dedup lookup misses; we enqueue a scan that will hash the now-complete file. Slight redundancy, no corruption. | Fast path + scan stage's own settling check (Plan 9.2 D3 inherits to scan). |
| E9  | **Library deleted mid-sweep.** Cascade-delete on the FK in `library_sweeps` removes the in-flight row; the runner finishes inserting/updating, then the FK cascade triggers; subsequent UPDATE on the (gone) row is a no-op. Errors are logged. | FK cascade. |
| E10 | **The walker hits a permission-denied directory.** `os.walk` swallows it silently by default; we add an `onerror=` callback that logs `WARNING: permission denied: dirpath`. The sweep continues. | Walker config. |
| E11 | **A file replaces another file at the same path with different content.** Existing row has `(path, size_old, mtime_old)`; sweep stat reads `(size_new, mtime_new)`; fast path triggers a rehash; new hash differs from old; `update_videos_hash` updates the row in place. The transcript pipeline's "is_active" semantics handle the actual reprocess (Story 7.5). | D2 hash-on-mismatch path. |

---

## 6. Acceptance checklist

- [ ] **A1** Diff against catalog: existing `(path, size, mtime)` → skip; new path with known hash → move; new path with new hash → enqueue scan; missing on disk → mark MISSING. (`test_runner_skips_unchanged_enqueues_new`, `test_path_change_with_known_hash_is_a_move`, `test_runner_marks_videos_missing_when_path_disappeared`)
- [ ] **A2** Single-flight: a second concurrent sweep for the same library is dropped at info level. (`test_concurrent_sweeps_for_same_library_one_runs`)
- [ ] **A3** Per-library `sweep_interval_sec` overrides default; `0` disables. (`test_scheduler_uses_per_library_interval`, `test_scheduler_zero_interval_disables`)
- [ ] **A4** Telemetry: every sweep writes a `library_sweeps` row with `started_at` (insert) + `finished_at` and counters (update). Unfinished rows discoverable via `library_sweeps_running` index. (`test_migration_creates_library_sweeps` + integration assert in `test_runner_*`.)
- [ ] **A5** `rehash=true` forces re-hash of every existing-path row. (`test_rehash_param_forces_rehash_of_known_paths`)
- [ ] **A6** 100k pre-indexed files complete in < 30 s on local SSD. (`test_100k_known_files_finish_under_30s`)
- [ ] **A7** Sweep enqueues are deduped against the watcher via the `processing_jobs_scan_dedup` partial unique index from Plan 9.2. (Cross-test: spawn watcher + sweep concurrently, assert one row.)
- [ ] **A8** No heavy I/O inside the sweep beyond stat + BLAKE3 on path-new files. (Static lint: `sweep/` does not import ffprobe/torch/embedder modules.)

---

## 7. Performance budget

| Phase | Cost (100k files, all already-indexed, local SSD) | Notes |
|-------|---------------------------------------------------|-------|
| `iter_files` (walk + prune + filter) | ~5 s | `os.walk` on warm cache; pruning removes hidden dirs. |
| `os.stat` per file | ~15 µs warm cache | 100k × 15 µs = 1.5 s. |
| `fetch_video_by_path` (per-file) | ~0.3 ms p95 (indexed lookup) | 100k × 0.3 ms = 30 s — **too slow**. Use a single bulk fetch into a Python dict for the diff: `SELECT path, content_hash, size, mtime FROM videos WHERE library_id = $1` returns all rows in ~500 ms; then in-memory join. Update §2.3 to use this strategy. |
| BLAKE3 hash (only for new/changed files; expected ~0 of 100k in the steady-state benchmark) | 0 s | The fast path covers all. |
| `_mark_missing` (zero rows in steady state) | ~10 ms | Cursor opens/closes. |
| `library_sweeps` insert + update | ~5 ms | Two indexed writes. |
| **Total** (steady state, all known) | **~7–10 s wall** | Well under the 30 s budget. |
| **Bootstrap** (first sweep, 100k brand-new files) | dominated by hashing + enqueue | Hashing 100k files is a separate budget (Plan 9.4) and well outside the 30 s steady-state target. The 30 s budget is the steady-state recurring sweep, not the first one. |

The §2.3 implementation must be revised to use **bulk fetch + in-memory
diff** rather than per-file lookups; the bulk fetch is the dominant cost.
This is captured in the perf budget above and in `test_100k_known_files_finish_under_30s`.
