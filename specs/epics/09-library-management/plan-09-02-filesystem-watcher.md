# Implementation Plan — Story 9.2 Filesystem Watcher

> Companion to [story-09-02-filesystem-watcher.md](story-09-02-filesystem-watcher.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on Story 9.1 (effective settings) and feeds Story 9.3 (sweep
> backstop) and Story 9.4 (content-hash dedup for the move case).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Runtime owner | Pipeline Service. One `WatcherSupervisor` per process; one `LibraryWatcher` per library; one `watchdog` `Observer` per `LibraryWatcher`, attached to all roots of that library (recursively). |
| Library code | `pipeline/src/maktaba_pipeline/watcher/` package: `supervisor.py`, `library_watcher.py`, `debouncer.py`, `move_detector.py`. |
| Settling | Per-path size+mtime poll loop driven by `asyncio` tasks scheduled by `Debouncer`. The `watchdog` thread only enqueues; all decisions happen on the asyncio event loop. |
| Move detection | Same-inode + same-size match within a 10 s correlation window inside `MoveDetector`. macOS `FSEventStreamEventFlagItemRenamed` short-circuits the heuristic. |
| Ignore rules | Pre-debounce filter using the matcher from Story 9.5; this story consumes the API but does not implement the matcher. |
| Restart resilience | On watcher boot, supervise enqueues a one-shot `scan` job for each library at priority 80 (between user-priority 50 and background 100). |
| Out of scope | The cross-library "delete+add" path is mentioned but the FK cascade is owned by Story 9.15; this story only emits an audit event. |

## 1. Architecture diagram

```
   ┌──────────────────────────────────────────────────────────────┐
   │                     Pipeline process                         │
   │                                                              │
   │   ┌───────────────────────────────────────────────────────┐ │
   │   │  WatcherSupervisor                                    │ │
   │   │   start():                                            │ │
   │   │     for lib in libraries.list_active():               │ │
   │   │       LibraryWatcher(lib).start()                     │ │
   │   │       enqueue(scan, lib_id, priority=80, payload={    │ │
   │   │         "reason": "watcher_boot_catchup"              │ │
   │   │       })                                              │ │
   │   │   subscribe('library.settings_changed')               │ │
   │   │     → debounce 1 s → reload affected watcher          │ │
   │   └───────────────────────────────────────────────────────┘ │
   │                       │ owns N                              │
   │                       ▼                                     │
   │   ┌───────────────────────────────────────────────────────┐ │
   │   │  LibraryWatcher                                       │ │
   │   │   - Observer (watchdog) thread → on_event(path,kind)  │ │
   │   │   - asyncio queue → on_event_async                    │ │
   │   │   - apply ignore_globs filter (Story 9.5)             │ │
   │   │   - if event_kind == 'moved' (mac/Win):               │ │
   │   │       → MoveDetector.record_move(src, dst)            │ │
   │   │   - else (created/modified/deleted):                  │ │
   │   │       → Debouncer.touch(path, kind, inode, size)      │ │
   │   │   - subscribe('library.settings_changed') for own id  │ │
   │   └───────────────────────────────────────────────────────┘ │
   │                       │ owns one                            │
   │                       ▼                                     │
   │   ┌───────────────────────────────────────────────────────┐ │
   │   │  Debouncer                                            │ │
   │   │   _pending: {path: PendingEntry}                      │ │
   │   │   touch(path,kind,inode,size):                        │ │
   │   │     reset timer, update last_seen, last_size          │ │
   │   │   _tick(path):                                        │ │
   │   │     - if delete_observed and not exists → emit DELETE │ │
   │   │     - if create/modify and stat(path).size            │ │
   │   │       == last_size and now-mtime > settle_sec → emit  │ │
   │   │       SCAN(path)                                      │ │
   │   │     - else reschedule                                 │ │
   │   └───────────────────────────────────────────────────────┘ │
   │                       │                                      │
   │                       ▼                                      │
   │   ┌───────────────────────────────────────────────────────┐ │
   │   │  MoveDetector                                         │ │
   │   │   _candidates: {(inode,size): (path, ts)}             │ │
   │   │   record_delete(path, inode, size, ts):               │ │
   │   │     remember as candidate, schedule expire(10 s)      │ │
   │   │   record_create(path, inode, size, ts):               │ │
   │   │     if (inode,size) in _candidates:                   │ │
   │   │       emit MOVE(src=cand_path, dst=path)              │ │
   │   │     else: forward to Debouncer as CREATE              │ │
   │   └───────────────────────────────────────────────────────┘ │
   │                       │                                      │
   │                       ▼                                      │
   │           enqueue(scan, video_id_or_root, payload)          │
   │           OR update videos.path (for moves) directly        │
   └──────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/watcher/__init__.py` | Re-exports `WatcherSupervisor`, `LibraryWatcher`. |
| `pipeline/src/maktaba_pipeline/watcher/supervisor.py` | Owns the per-library lifecycle, watcher-boot catchup, and reload on settings change. |
| `pipeline/src/maktaba_pipeline/watcher/library_watcher.py` | Bridges the `watchdog` thread to the asyncio event loop; applies the ignore filter; routes events to `Debouncer` and `MoveDetector`. |
| `pipeline/src/maktaba_pipeline/watcher/debouncer.py` | Per-path settling state machine. |
| `pipeline/src/maktaba_pipeline/watcher/move_detector.py` | Inode/size correlation for move-without-rename-event platforms. |
| `pipeline/src/maktaba_pipeline/watcher/events.py` | Frozen dataclasses for `WatcherEvent` (`SCAN`, `DELETE`, `MOVE`). The supervisor subscribes to these for tests. |
| `pipeline/tests/watcher/conftest.py` | Async fixtures: tmp roots, fake clock, `EventCollector`. |
| `pipeline/tests/watcher/test_debouncer.py` | Unit tests per §6.1. |
| `pipeline/tests/watcher/test_move_detector.py` | Unit tests per §6.2. |
| `pipeline/tests/watcher/test_library_watcher_integration.py` | Filesystem-touching integration tests per §6.3. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/cli.py` | Add `pipeline run-watcher` subcommand for ops; the `pipeline serve` default already starts the supervisor. |
| `pipeline/src/maktaba_pipeline/db/jobs.py` | Re-use `enqueue` (Story 6.1); add a thin helper `enqueue_scan_for_path(library_id, path, reason)`. |
| `pipeline/pyproject.toml` | Add `watchdog>=4.0`. |
| `specs/epics/09-library-management/README.md` | Tick story 9.2. |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/watcher/events.py
from __future__ import annotations
from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from uuid import UUID


class EventKind(StrEnum):
    SCAN   = "scan"     # new or modified file ready for the scan stage
    DELETE = "delete"   # file is gone; mark videos row MISSING
    MOVE   = "move"     # within-library move; update videos.path


@dataclass(slots=True, frozen=True)
class WatcherEvent:
    library_id: UUID
    kind: EventKind
    path: Path                # destination path for MOVE; absolute
    src_path: Path | None     # only for MOVE
    size: int | None
    inode: int | None
    detected_at: float        # monotonic seconds
```

```python
# pipeline/src/maktaba_pipeline/watcher/debouncer.py
from __future__ import annotations
import asyncio
from dataclasses import dataclass, field
from pathlib import Path
from typing import Awaitable, Callable


@dataclass(slots=True)
class PendingEntry:
    path: Path
    last_kind: str            # "created" | "modified" | "deleted"
    last_size: int | None
    last_seen_at: float
    settle_streak: int = 0    # consecutive ticks where size unchanged
    timer: asyncio.TimerHandle | None = field(default=None, repr=False)


class Debouncer:
    def __init__(
        self,
        emit: Callable[[WatcherEvent], Awaitable[None]],
        *,
        debounce_sec: float = 2.0,
        settle_sec: float = 5.0,
        required_streak: int = 2,
        clock: Callable[[], float] = None,
    ) -> None: ...

    def touch(self, path: Path, kind: str, inode: int | None,
              size: int | None) -> None: ...

    async def _tick(self, path: Path) -> None: ...
```

### 2.4 Function signatures

```python
# pipeline/src/maktaba_pipeline/watcher/supervisor.py
class WatcherSupervisor:
    def __init__(self, db, bus: PubsubBus, clock=None): ...
    async def start(self) -> None: ...
    async def stop(self) -> None: ...
    async def reload_library(self, library_id: UUID) -> None: ...
```

```python
# pipeline/src/maktaba_pipeline/watcher/library_watcher.py
class LibraryWatcher:
    def __init__(self, library, debouncer, move_detector,
                 ignore_matcher, *, loop=None): ...
    def start(self) -> None: ...
    def stop(self) -> None: ...
    # callable from the watchdog thread; thread-safe (uses
    # asyncio.run_coroutine_threadsafe(loop=self._loop)).
    def on_event(self, event: watchdog.events.FileSystemEvent) -> None: ...
```

## 3. Database migration

No new tables for this story — the watcher writes via the existing
`enqueue` (Story 6.1) and updates `videos.path` directly.

The `videos` table already has `path TEXT NOT NULL UNIQUE` per the
catalog spec. One small addition:

`shared/db/migrations/0031_videos_path_history.sql`

```sql
-- +goose Up
-- +goose StatementBegin

-- Move audit (AC-3) requires a history of previous paths so the user
-- can see "moved from /a/b to /a/c"; FK cascade in Story 9.15 reaches
-- this table.
CREATE TABLE video_path_history (
    id              BIGSERIAL PRIMARY KEY,
    video_id        UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    old_path        TEXT NOT NULL,
    new_path        TEXT NOT NULL,
    detected_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    detected_by     TEXT NOT NULL CHECK (detected_by IN ('watcher','sweep','manual'))
);
CREATE INDEX video_path_history_video ON video_path_history (video_id, detected_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS video_path_history;
-- +goose StatementEnd
```

The SQLite variant (`.sqlite.sql`) replaces `BIGSERIAL` with
`INTEGER PRIMARY KEY AUTOINCREMENT` and `TIMESTAMPTZ` with `TEXT`.

## 4. Code scaffolding

### 4.1 `Debouncer` core (excerpt)

```python
# pipeline/src/maktaba_pipeline/watcher/debouncer.py
import asyncio, os, time
from pathlib import Path

class Debouncer:
    def __init__(self, emit, *, debounce_sec=2.0, settle_sec=5.0,
                 required_streak=2, clock=None) -> None:
        self._emit = emit
        self._debounce_sec = debounce_sec
        self._settle_sec = settle_sec
        self._required_streak = required_streak
        self._clock = clock or time.monotonic
        self._loop = asyncio.get_event_loop()
        self._pending: dict[Path, PendingEntry] = {}

    def touch(self, path, kind, inode, size) -> None:
        e = self._pending.get(path)
        if e is None:
            e = PendingEntry(path=path, last_kind=kind,
                             last_size=size, last_seen_at=self._clock())
            self._pending[path] = e
        else:
            if e.last_size != size:
                e.settle_streak = 0
            e.last_kind = kind
            e.last_size = size
            e.last_seen_at = self._clock()
            if e.timer is not None:
                e.timer.cancel()
        e.timer = self._loop.call_later(self._debounce_sec,
            lambda: asyncio.create_task(self._tick(path)))

    async def _tick(self, path) -> None:
        e = self._pending.get(path)
        if e is None:
            return

        if e.last_kind == "deleted":
            if not path.exists():
                await self._emit(WatcherEvent(
                    library_id=self._lib_id, kind=EventKind.DELETE,
                    path=path, src_path=None, size=None, inode=None,
                    detected_at=self._clock(),
                ))
                self._pending.pop(path, None)
                return
            # File reappeared — collapse the delete; treat as modify.
            e.last_kind = "modified"

        try:
            st = path.stat()
        except FileNotFoundError:
            # Race: vanished between event and tick. Re-arm; if next
            # tick also sees nothing, AC-1 requires us to enqueue
            # nothing (file came and went within window).
            self._pending.pop(path, None)
            return

        # Settling: same size as previous tick AND mtime quiet for
        # settle_sec. Two consecutive matches required (AC-2).
        if st.st_size == e.last_size:
            e.settle_streak += 1
        else:
            e.settle_streak = 0
            e.last_size = st.st_size

        if (e.settle_streak >= self._required_streak
                and self._clock() - st.st_mtime >= self._settle_sec):
            await self._emit(WatcherEvent(
                library_id=self._lib_id, kind=EventKind.SCAN,
                path=path, src_path=None,
                size=st.st_size, inode=st.st_ino,
                detected_at=self._clock(),
            ))
            self._pending.pop(path, None)
            return

        # Re-arm.
        e.timer = self._loop.call_later(self._debounce_sec,
            lambda: asyncio.create_task(self._tick(path)))
```

### 4.2 `MoveDetector` core (excerpt)

```python
# pipeline/src/maktaba_pipeline/watcher/move_detector.py
class MoveDetector:
    def __init__(self, emit, *, correlation_sec=10.0, clock=None) -> None:
        self._emit = emit
        self._window = correlation_sec
        self._clock = clock or time.monotonic
        self._candidates: dict[tuple[int,int], tuple[Path, float]] = {}

    def record_delete(self, path: Path, inode: int, size: int) -> None:
        self._candidates[(inode, size)] = (path, self._clock())
        # opportunistic GC — single pass over a small dict
        for k, (_, t) in list(self._candidates.items()):
            if self._clock() - t > self._window:
                del self._candidates[k]

    async def record_create(self, path: Path, inode: int,
                            size: int) -> bool:
        """Returns True if matched as a move and emitted; False if caller
        should treat as a fresh create."""
        cand = self._candidates.pop((inode, size), None)
        if cand is None:
            return False
        src, _ = cand
        await self._emit(WatcherEvent(
            library_id=self._lib_id, kind=EventKind.MOVE,
            path=path, src_path=src, size=size, inode=inode,
            detected_at=self._clock(),
        ))
        return True
```

### 4.3 Supervisor: boot catchup + settings reload

```python
# pipeline/src/maktaba_pipeline/watcher/supervisor.py
class WatcherSupervisor:
    async def start(self) -> None:
        libs = await self._db.fetch("SELECT id FROM libraries WHERE deleted_at IS NULL")
        for r in libs:
            await self._spawn(r["id"])
        # AC-4: enqueue a sweep per library at priority 80 with
        # payload={"reason": "watcher_boot_catchup"}.
        for r in libs:
            await enqueue(self._db, video_id=None, stage=Stage.SCAN,
                          priority=80,
                          payload={"library_id": str(r["id"]),
                                   "reason": "watcher_boot_catchup"})
        # Subscribe to settings changes for hot reload.
        q = await self._bus.subscribe(LIBRARY_SETTINGS_CHANGED)
        asyncio.create_task(self._reload_loop(q))

    async def _reload_loop(self, q: asyncio.Queue[str]) -> None:
        while True:
            note = json.loads(await q.get())
            await self.reload_library(UUID(note["library_id"]))
```

### 4.4 LibraryWatcher: bridging watchdog → asyncio

```python
# pipeline/src/maktaba_pipeline/watcher/library_watcher.py
class _ThreadEventHandler(watchdog.events.FileSystemEventHandler):
    def __init__(self, owner): self._owner = owner

    def on_any_event(self, event):
        # Called on the watchdog thread.
        asyncio.run_coroutine_threadsafe(
            self._owner._on_event_async(event), self._owner._loop,
        )


class LibraryWatcher:
    def __init__(self, library, debouncer, move_detector,
                 ignore_matcher, *, db, loop=None) -> None:
        self._lib = library
        self._debouncer = debouncer
        self._move = move_detector
        self._ignore = ignore_matcher
        self._db = db
        self._loop = loop or asyncio.get_event_loop()
        self._observer = watchdog.observers.Observer()
        # Architecture §8.1: library_roots is canonical; libraries.roots
        # TEXT[] is deprecated. Read from library_roots here.
        roots = self._loop.run_until_complete(self._db.fetch(
            "SELECT path FROM library_roots WHERE library_id=$1",
            library.id,
        ))
        for r in roots:
            self._observer.schedule(
                _ThreadEventHandler(self), path=str(r["path"]), recursive=True)

    async def _on_event_async(self, e: watchdog.events.FileSystemEvent) -> None:
        # Filter ignored paths early — keeps the debouncer small.
        if self._ignore.matches(e.src_path):
            return
        path = Path(e.src_path)
        try:
            st = path.stat()
            inode, size = st.st_ino, st.st_size
        except FileNotFoundError:
            inode, size = None, None  # delete events

        if e.event_type == "moved" and not e.is_directory:
            # macOS / Windows native rename events.
            await self._move._emit(WatcherEvent(
                library_id=self._lib.id, kind=EventKind.MOVE,
                path=Path(e.dest_path),
                src_path=Path(e.src_path),
                size=size, inode=inode, detected_at=time.monotonic(),
            ))
            return

        if e.event_type == "deleted":
            self._move.record_delete(path, inode or 0, size or 0)
            self._debouncer.touch(path, "deleted", inode, size)
            return

        if e.event_type == "created":
            matched = await self._move.record_create(path, inode or 0, size or 0)
            if matched:
                return  # MOVE emitted; no scan
            self._debouncer.touch(path, "created", inode, size)
            return

        if e.event_type == "modified":
            self._debouncer.touch(path, "modified", inode, size)
            return
```

### 4.5 Emit callback — DB writes

```python
# Hooked into Debouncer/MoveDetector via the supervisor:
async def emit(event: WatcherEvent) -> None:
    if event.kind == EventKind.SCAN:
        await enqueue_scan_for_path(db, event.library_id, event.path,
                                    reason="watcher")
        bus.publish(WATCHER_SCAN_QUEUED, {
            "library_id": str(event.library_id),
            "path": str(event.path),
        })
    elif event.kind == EventKind.MOVE:
        async with db.transaction():
            row = await db.fetchrow(
                "SELECT id FROM videos WHERE path = $1", str(event.src_path))
            if row is None:
                # Source unknown — treat as a fresh create.
                await enqueue_scan_for_path(db, event.library_id,
                                            event.path, reason="watcher")
                return
            await db.execute(
                "UPDATE videos SET path=$1, updated_at=now() WHERE id=$2",
                str(event.path), row["id"])
            await db.execute(
                "INSERT INTO video_path_history "
                "  (video_id, old_path, new_path, detected_by) "
                "VALUES ($1,$2,$3,'watcher')",
                row["id"], str(event.src_path), str(event.path))
    elif event.kind == EventKind.DELETE:
        # FSM uses lowercase canonical state strings (architecture §8.1).
        # Soft-delete via deleted_at for tombstones is the catalog's
        # affordance; for "file is gone" we transition to the auxiliary
        # terminal state `missing` (canonical Epic-9 FSM extension owned
        # by plan-09-06).
        await db.execute(
            "UPDATE videos SET state='missing', updated_at=now() "
            "WHERE path=$1 AND state <> 'missing'", str(event.path))
```

## 5. Test plan

### 5.1 Debouncer unit tests (`test_debouncer.py`)

| Test | What it pins |
|---|---|
| `test_single_event_emits_after_debounce` | One `created` event with stable size emits one `SCAN` after `debounce_sec`. |
| `test_burst_collapses_to_one_emit` | 50 modify events at 50 ms intervals → exactly one `SCAN` after the last touch + debounce_sec. |
| `test_growing_file_does_not_emit` | A simulated copy with monotonically increasing size never emits `SCAN`; once size stabilizes for two ticks AND mtime quiet for `settle_sec`, it emits. |
| `test_create_then_delete_in_window_emits_nothing` | `created` → `deleted` within debounce window → no event emitted; pending state cleared. |
| `test_delete_event_emits_delete` | `deleted` followed by no further events → after debounce, emits `DELETE`. |
| `test_settle_streak_resets_on_size_change` | After one streak hit, a size change resets streak to 0; emit deferred until two more clean ticks. |
| `test_size_stable_but_mtime_recent_does_not_emit` | Size unchanged but `now-mtime < settle_sec` → no emit; rearm. |

The debouncer tests use a fake monotonic clock and a fake `path.stat()`
so no real files are involved — keeps the unit tests sub-millisecond.

### 5.2 MoveDetector unit tests (`test_move_detector.py`)

| Test | What it pins |
|---|---|
| `test_paired_delete_then_create_emits_move` | `record_delete(/a/b, ino=42, size=N)`; `record_create(/a/c, ino=42, size=N)` → returns True, emits MOVE. |
| `test_create_outside_window_treated_as_create` | `record_delete(...)` → wait `correlation_sec + 1`; `record_create(same_inode_size)` → returns False, no MOVE. |
| `test_create_first_then_delete_no_emit` | Creates that don't match a prior delete return False; no spurious MOVE on later delete. |
| `test_inode_reused_after_delete_is_not_move` | `record_delete(/a/b, ino=42, size=N)`; `record_create(/x/y, ino=42, size=M)` (different size) → returns False. |

### 5.3 Library-watcher integration tests (`test_library_watcher_integration.py`)

| Test | What it pins |
|---|---|
| `test_100_mib_copy_enqueues_once` | `dd if=/dev/zero of=tmp/movie.mp4 bs=1M count=100` (simulated) → exactly one `enqueue` call after copy completes. AC-2 verbatim. |
| `test_atomic_rename_emits_no_scan_on_linux` | `mv tmp/foo.mp4 tmp/bar.mp4` within one root → exactly one `videos.path` UPDATE; no `enqueue`. AC-3. |
| `test_native_rename_event_macos` | Simulates a `FileMovedEvent` from `watchdog`'s macOS emitter → MOVE emitted directly, no debounce. |
| `test_settings_change_reloads_watcher` | PATCH library settings (changes `ignore_globs`); within 2 s the watcher's matcher has the new globs. Verified by writing a now-ignored file and confirming no enqueue. |
| `test_boot_catchup_enqueues_scan_per_library` | Stop watcher; create a file; start supervisor → exactly one `scan` job at priority 80 with `payload.reason='watcher_boot_catchup'` per library. AC-4. |
| `test_cross_library_move_treated_as_delete_then_create` | Move a file from libA root to libB root → libA emits DELETE (path → MISSING), libB emits SCAN. No `videos.path` UPDATE. |
| `test_symlink_loop_does_not_recurse_infinitely` | Create `a → ../a` symlink loop in a root → watcher boots without stack overflow; visited-inode set caps recursion. |

### 5.4 Performance gate

`test_watcher_throughput_smoke` — 10 000 file creations across 10
subdirectories should produce 10 000 `enqueue` calls within 30 s on the
CI runner (≈ 333/s, well above the worst-case scanner rate).

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Created and deleted within debounce window | Pending entry cleared on `_tick` when `path.exists() == False`; no enqueue. | `test_create_then_delete_in_window_emits_nothing` |
| File grows for 30 s then stabilizes | First 14 ticks see growing size → streak resets every time; last two ticks see stable size + mtime quiet → emit once. | `test_growing_file_does_not_emit` (extended) |
| Mass `mv` of 10k files | Every event hits the per-path debouncer; the `_tick` callbacks land on the asyncio loop and serialize. The orchestrator is rate-limited by the scan stage's concurrency cap (4 per §7.4); back-pressure flows naturally through `enqueue`'s idempotency. | Documented; not a hot test (would be a 30s burn) |
| Symlink loop in a root | `watchdog`'s `recursive=True` follows symlinks; we wrap the observer with a visited-inode set guard at scan start. | `test_symlink_loop_does_not_recurse_infinitely` |
| FUSE mount that drops events | The watcher cannot detect this — Story 9.3's periodic sweep is the backstop. The boot-catchup sweep also kicks in after every restart. | Documented in §1; sweep tests live with Story 9.3 |
| Ignored path bypasses debouncer | The ignore matcher (Story 9.5) runs before any debounce work — keeps the pending dict bounded. | `test_settings_change_reloads_watcher` (variant: write `.crdownload` file → no enqueue) |
| Boot-catchup duplicates user events | The boot scan and the watcher's first event for the same file both call `enqueue`; the unique partial index from Story 6.1 collapses the second into `outcome='reused'`. | Already pinned by Story 6.1's `test_enqueue_idempotent_returns_same_id` |
| Library deleted while watcher running | `WatcherSupervisor` listens for `library.settings_changed` and a future `library.deleted` channel (Story 9.15); on receive, calls `LibraryWatcher.stop()`. | Cross-story; verified once Story 9.15 is in. |

## 7. Configuration knobs

The watcher reads, at minimum, these effective-settings keys (all merged
in Story 9.1):

| Key | Default | Used by |
|---|---|---|
| `watch_debounce_sec` | 2.0 | `Debouncer.debounce_sec` |
| `watch_settle_sec` | 5.0 | `Debouncer.settle_sec` |
| `watch_required_streak` | 2 | `Debouncer.required_streak` |
| `move_correlation_sec` | 10.0 | `MoveDetector.correlation_sec` |
| `ignore_globs` | `[]` | The Story-9.5 matcher constructed from this list |

These are added to `library_settings.schema.json` in Story 9.1's
revision tied to this story landing — the Story 9.1 plan §4 schema
already reserves the prefix `watch_*` for forward-compatibility through
`additionalProperties: true`.

## 8. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `watchdog` | ≥ 4.0 | Single cross-platform observer with macOS FSEvents, Linux inotify, Windows ReadDirectoryChangesW. The `Observer` class is recursive out of the box. |
| `aiosqlite` / `asyncpg` | already pinned (Story 6.1) | Used through the existing `db` wrapper. |

No native modules. `watchdog`'s observer runs in a single thread per
library; the asyncio loop owns all decisions.

## 9. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/watcher/` package created with the four modules from §2.1.
- [ ] `WatcherSupervisor.start()` boots one watcher per library and enqueues one `scan` job per library at priority 80.
- [ ] `LibraryWatcher` filters ignored paths *before* touching the debouncer.
- [ ] Settings reload (NOTIFY → reload) takes < 2 s end-to-end.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: bursts collapse to one enqueue per `debounce_sec` window.
- [ ] AC-2: settling check requires two consecutive same-size ticks AND `now-mtime ≥ settle_sec`.
- [ ] AC-3: same-library moves update `videos.path` and emit no scan; `video_path_history` row written.
- [ ] AC-4: restart catches up via a one-shot sweep enqueued at priority 80.

**Migration**
- [ ] `0031_videos_path_history.sql` applies on Postgres and SQLite (variant).

**Observability**
- [ ] Counter `maktaba_watcher_events_total{library_id, kind}` exported (`kind ∈ scan|delete|move|ignored`).
- [ ] Gauge `maktaba_watcher_pending_paths{library_id}` exported (size of `Debouncer._pending`).

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.2.
- [ ] `docs/operations/filesystem-quirks.md` lists known FUSE / NFS event-drop scenarios and points to Story 9.3 as the backstop.
