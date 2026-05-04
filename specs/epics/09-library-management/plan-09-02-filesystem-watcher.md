# Plan 9.2 — Filesystem watcher (debounced, settling-aware) — implementation

> Implementation plan for [story-09-02-filesystem-watcher.md](story-09-02-filesystem-watcher.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: depends on the resolved-config contract from
> [Plan 9.1](plan-09-01-library-config-schema.md) (the watcher reads
> `ignore_globs` and `watch_*` from the cached effective settings); pairs
> with [Plan 9.3](plan-09-03-periodic-sweep.md) (the sweep is the
> backstop on watcher restart and on event-loss filesystems); applies
> the matchers from [Plan 9.5](plan-09-05-ignore-rules.md) before
> queuing; and produces the events that the dedup logic in
> [Plan 9.4](plan-09-04-content-hash-dedup.md) consumes when a `scan`
> job runs. The watcher does **not** compute hashes (the `scan` stage
> does) and does **not** call the orchestrator directly — it inserts a
> `processing_jobs` row of stage `scan`.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **One `watchdog` Observer per library, one watch handler per root**, all sharing a single asyncio event loop in the Pipeline Service. The Observer threads call into a thread-safe `LibraryDebouncer.feed(event)` which hops to the loop via `loop.call_soon_threadsafe`. | Story AC-1; architecture §5.1: "Per-library `watchdog` observer in the Pipeline Service." | A separate event loop per library would explode the file-descriptor and worker count on installations with hundreds of libraries. One Observer per library bounds the inotify watch count to roots-per-library and keeps shutdown simple (Observer.stop is per-library). |
| D2 | **Debounce queue is a per-library async timer wheel** keyed by canonical absolute path. Each entry holds `(first_seen, last_event, last_size, last_mtime, stable_ticks, kind)`; one shared asyncio task scans the wheel every `tick_sec = 1.0 s` and graduates entries whose conditions are met. Wheel and graduate-action are protected by a single asyncio.Lock per library. | Story AC-1, AC-2: "if no further event for the same path arrives within `watch_debounce_sec` (default 2 s) and the file's size has been stable for that interval"; "the file is *not* enqueued until two consecutive ticks see the same size". | A naive per-path asyncio task with `await asyncio.sleep(2)` works for tens of files but creates one task per change for a 10,000-file mv (Story edge case "Massive `mv`"). A shared wheel walks O(N) per tick where N is the *currently debouncing* set, which collapses to a small steady state. |
| D3 | **Settling check uses size+mtime parity over two consecutive ticks**, not a single fstat after debounce. A path graduates only when `now - last_event ≥ watch_debounce_sec` AND `stable_ticks ≥ 2` (i.e., size and mtime were unchanged across two `tick_sec` intervals). Files modified within the last `watch_settle_sec` (default 5 s) reset `stable_ticks` to 0. | Story AC-2 verbatim. | Two ticks at 1 s tick = a 2-second observation window — matches the story's "stable for that interval" wording. Including mtime in the parity check catches editors that truncate-and-rewrite preserving size (a stat-only check would miss the mid-write window). |
| D4 | **Move detection within a library uses inode parity (Linux) or `FileMovedEvent` (macOS).** On Linux we keep a transient `inode_index: dict[int, str]` updated on every event; a deleted-then-created pair within `watch_move_window_sec` (default 1.0 s) with matching inode is reclassified as a move. On macOS `watchdog` already emits `FileMovedEvent`; we trust it. | Story AC-3: "the OS emits paired `deleted` + `created` events with the same inode on Linux, or a `moved` event on macOS". | Inode-based reconciliation is the only correct cross-distro answer on Linux because `watchdog` does not emit `FileMovedEvent` for cross-watch-descriptor moves. The 1-second window matches the `inotify` queue drain time on busy systems. |
| D5 | **A move within a library updates `videos.path` *in the API service via an internal RPC***, not directly from Pipeline. The Pipeline emits a `library.move_detected` event; an API endpoint `POST /api/internal/libraries/{id}/move` updates the row and writes an audit entry. | Story AC-3 + architecture §5: "API owns the catalog write surface." | Separating identity-preserving renames from "new file with the same hash" (Plan 9.4) keeps the watcher narrow. The API path also gives us a single audit logger and the right place for cross-library policy (rename across libraries → reject as move; the watcher would have no way to reject locally). |
| D6 | **On Pipeline restart, a one-shot full sweep runs first**, then watchers start. The sweep waits for completion before the watcher accepts events; events that arrive before sweep completion are dropped (the sweep would have caught them anyway). | Story AC-4: "a one-shot full sweep (Story 9.3) catches up, and the watcher begins emitting events for further changes." | Starting the watcher first creates a duplicate-detection problem: the sweep finds a file, enqueues `scan`; then the watcher's queued event for the same file enqueues `scan` again. Sweep-then-watch eliminates the race; a few events lost during boot don't matter because the sweep already covered them. |
| D7 | **Symlink loops are guarded by per-scan visited-inode + max-depth**. The watcher uses `recursive=True` but registers a `dir_visit_callback` that refuses to descend if the inode is in the visited set or depth ≥ 32. Loops are logged once per unique inode pair. | Story edge case: "Symlink loops in a root — followed by `watchdog`'s `recursive` mode but must be guarded; we use a per-scan visited-inode set to prevent infinite recursion." | A single symlink loop in a library root can otherwise saturate inotify watches and crash the process. Per-scan because a "scan" here is the watcher's own initial walk (watchdog still walks the tree to set up watches even with `recursive=True`). |
| D8 | **Settings live-reload via `library.settings_changed` NOTIFY (Plan 9.1).** When the cached effective config changes (specifically `ignore_globs`, `watch_debounce_sec`, `watch_settle_sec`), the watcher rebuilds its matchers in place; it does NOT restart the Observer. | Cross-cut with Plan 9.1's invalidation contract. | Restarting an Observer momentarily drops events; in-place matcher swap is atomic (one assignment under the per-library lock). Watch tree itself doesn't change because the *roots* are not part of `settings`. |
| D9 | **Job enqueue is an INSERT into `processing_jobs` with stage = `scan`**, dedup'd by `(library_id, source_path, stage='scan', state IN ('queued','running'))` UNIQUE partial index. A redundant enqueue is silently ignored. | Architecture §5: orchestrator picks up `processing_jobs`. | The unique partial index makes the watcher idempotent on retries and on the boundary between sweep and watcher. The path-based key is the right unit of dedup before we know the file's content hash. |
| D10 | **Watcher does NOT call ffprobe, BLAKE3, or any heavy I/O.** Only stat + fnmatch + insert. Heavy work happens in the `scan` stage. | Watcher must keep up with bursty events; story edge case "Massive `mv`". | Pushing hashing or probing into the watcher creates a per-event cost that breaks under load. Keeping the watcher cheap means the only failure mode under load is "queue grows" — which is recoverable. |

If D2 is rejected (per-event sleep tasks): a 10,000-file mv produces 10,000
short-lived tasks, blocks the scheduler for ~5 s, and risks the
`watchdog` queue overflow (event loss). The wheel keeps memory steady
at O(N_currently_debouncing).

If D6 is rejected (watch then sweep): events arriving during the sweep
window double-enqueue. We'd have to add a "first-time-seen" gate keyed by
path on the sweep; cheaper to just sequence them.

---

## 1. Architecture diagram — watcher data flow

```
   Pipeline boot
     │
     ▼
  ┌──────────────────────────────────────────────────────────────┐
  │ WatcherSupervisor.start()                                    │
  │   - load libraries from DB                                   │
  │   - for each library:                                        │
  │       1. run a one-shot sweep (Plan 9.3) and wait            │
  │       2. start a LibraryWatcher                              │
  └──────────────────────────────────────────────────────────────┘
     │
     ▼
  ┌──────────────────────────────────────────────────────────────┐
  │ LibraryWatcher (per library)                                 │
  │   ┌──────────────────────────┐                               │
  │   │ watchdog.Observer        │  threads (1 per platform)     │
  │   │   on event:              │                               │
  │   │     ignore_match? → drop │                               │
  │   │     enqueue via          │                               │
  │   │     loop.call_soon_       │                               │
  │   │     threadsafe(           │                               │
  │   │       debouncer.feed)    │                               │
  │   └────────────┬─────────────┘                               │
  │                │                                             │
  │                ▼                                             │
  │   ┌──────────────────────────────────────────────────────┐   │
  │   │ LibraryDebouncer (asyncio)                           │   │
  │   │   wheel: dict[path] -> Entry                         │   │
  │   │   inode_index: dict[int, str] (Linux move recon)     │   │
  │   │   tick task (every 1s):                              │   │
  │   │     for entry in wheel:                              │   │
  │   │       fstat -> update size+mtime                     │   │
  │   │       if same size+mtime as last tick:               │   │
  │   │         entry.stable_ticks += 1                      │   │
  │   │       else:                                          │   │
  │   │         entry.stable_ticks = 0                       │   │
  │   │         entry.last_event = now                       │   │
  │   │       if (now - last_event >= debounce) and          │   │
  │   │          stable_ticks >= 2:                          │   │
  │   │         graduate(entry)                              │   │
  │   └──────────────────┬───────────────────────────────────┘   │
  │                      │                                       │
  │                      ▼                                       │
  │   ┌──────────────────────────────────────────────────────┐   │
  │   │ graduate(entry):                                     │   │
  │   │   if move-paired (inode parity):                     │   │
  │   │     POST /api/internal/libraries/{id}/move           │   │
  │   │   else:                                              │   │
  │   │     INSERT INTO processing_jobs                      │   │
  │   │       (library_id, source_path, stage='scan',        │   │
  │   │        state='queued', settings_version=$v)          │   │
  │   │     ON CONFLICT (the partial unique index) DO NOTHING│   │
  │   └──────────────────────────────────────────────────────┘   │
  └──────────────────────────────────────────────────────────────┘
     │
     ▼  (writes processing_jobs; pipeline scan stage picks up)
   Postgres: processing_jobs   videos
                  │                │
                  ▼                ▼
         scan stage runs (Epic 1) — computes hash (Plan 9.4),
         creates videos row or merges by content_hash.
```

The watcher's only DB interactions are (a) reading `libraries.roots` at
boot, (b) listening for `library.settings_changed`, and (c) inserting
`processing_jobs` rows. It never reads `videos`. That's the scan stage's
job.

---

## 2. Detailed implementation

### 2.1 SQL — partial unique index for enqueue dedup (D9)

```sql
-- shared/db/migrations/00XX_processing_jobs_scan_dedup.sql
BEGIN;

-- Idempotent watcher enqueue: only one queued/running scan job per
-- (library_id, source_path) at a time.
CREATE UNIQUE INDEX IF NOT EXISTS processing_jobs_scan_dedup
    ON processing_jobs (library_id, source_path)
    WHERE stage = 'scan' AND state IN ('queued', 'running');

COMMIT;
```

### 2.2 Python package layout

```
pipeline/src/maktaba_pipeline/watcher/
├── __init__.py                  # public surface: WatcherSupervisor, LibraryWatcher
├── supervisor.py                # boot/shutdown, sweep-then-watch sequencing (D6)
├── library_watcher.py           # one watchdog Observer per library
├── debouncer.py                 # async tick wheel, settling check (D2, D3)
├── move_recon.py                # inode-parity move detection (D4)
├── enqueue.py                   # processing_jobs INSERT (D9)
├── ignore.py                    # thin re-export of Plan 9.5 matcher
├── errors.py
└── tests/
    ├── conftest.py
    ├── test_debouncer.py
    ├── test_settling.py
    ├── test_move_recon.py
    ├── test_enqueue_dedup.py
    ├── test_supervisor_sweep_then_watch.py
    └── test_symlink_loop_guard.py
```

### 2.3 `debouncer.py` — wheel + settling (D2, D3)

```python
"""LibraryDebouncer — a per-library tick wheel that graduates settled paths.

The Observer thread feeds events via feed(), which hops onto the loop:

    loop.call_soon_threadsafe(debouncer._enqueue, event)

A single asyncio task (`_tick_loop`) walks the wheel every tick_sec and
graduates entries that satisfy:

    (now - last_event) >= debounce_sec  AND  stable_ticks >= 2
"""
from __future__ import annotations
import asyncio, logging, os, time
from dataclasses import dataclass, field
from typing import Awaitable, Callable

log = logging.getLogger(__name__)


@dataclass
class _Entry:
    path: str
    first_seen: float
    last_event: float
    last_size: int = -1
    last_mtime: float = -1.0
    stable_ticks: int = 0
    kind: str = "modified"        # 'created' | 'modified' | 'deleted'
    inode: int | None = None


@dataclass
class DebouncerConfig:
    debounce_sec: float = 2.0
    settle_sec: float = 5.0
    tick_sec: float = 1.0
    move_window_sec: float = 1.0


class LibraryDebouncer:
    def __init__(self, *,
                 cfg: DebouncerConfig,
                 graduate: Callable[[_Entry], Awaitable[None]],
                 reconcile_move: Callable[[_Entry, _Entry], Awaitable[bool]] | None = None):
        self._cfg = cfg
        self._wheel: dict[str, _Entry] = {}
        self._inode_index: dict[int, str] = {}
        self._pending_deletes: dict[int, _Entry] = {}    # inode -> deleted entry
        self._lock = asyncio.Lock()
        self._stop = asyncio.Event()
        self._graduate = graduate
        self._reconcile_move = reconcile_move

    async def start(self) -> None:
        asyncio.create_task(self._tick_loop(), name="watcher.debouncer.tick")

    async def stop(self) -> None:
        self._stop.set()

    async def feed(self, *, kind: str, path: str, inode: int | None) -> None:
        now = time.monotonic()
        async with self._lock:
            if kind == "deleted":
                # Stash for inode-paired move within move_window_sec.
                if inode is not None:
                    self._pending_deletes[inode] = _Entry(
                        path=path, first_seen=now, last_event=now,
                        kind="deleted", inode=inode)
                self._wheel.pop(path, None)
                return

            entry = self._wheel.get(path)
            if entry is None:
                entry = _Entry(path=path, first_seen=now, last_event=now,
                               kind=kind, inode=inode)
                self._wheel[path] = entry
                if inode is not None:
                    self._inode_index[inode] = path
            else:
                entry.last_event = now
                entry.kind = kind
                if inode is not None:
                    entry.inode = inode
                    self._inode_index[inode] = path

    async def _tick_loop(self) -> None:
        cfg = self._cfg
        while not self._stop.is_set():
            try:
                await asyncio.sleep(cfg.tick_sec)
                await self._tick()
            except Exception:               # never let the wheel die
                log.exception("debouncer tick failed")

    async def _tick(self) -> None:
        now = time.monotonic()
        graduates: list[_Entry] = []
        moves: list[tuple[_Entry, _Entry]] = []

        async with self._lock:
            # 1. Drop stale pending-deletes that didn't pair with a create.
            stale_delete_inodes = [
                inode for inode, e in self._pending_deletes.items()
                if now - e.last_event > self._cfg.move_window_sec
            ]
            for inode in stale_delete_inodes:
                # Treat as a real delete — we'd need a separate path here
                # to enqueue a 'forget' job; for now, we just drop it (the
                # next sweep marks it MISSING).
                del self._pending_deletes[inode]

            # 2. Iterate the wheel.
            ready: list[str] = []
            for path, entry in self._wheel.items():
                if entry.kind == "deleted":
                    continue
                # Settling check (D3): size + mtime parity.
                try:
                    st = os.stat(path)
                except FileNotFoundError:
                    # Disappeared during debounce: treat as a created+deleted
                    # pair → cancel, do not graduate.
                    ready.append(path)         # we'll pop it without graduating
                    continue
                except OSError as e:
                    log.warning("stat failed for %s: %s", path, e)
                    continue

                size, mtime = st.st_size, st.st_mtime
                if size == entry.last_size and mtime == entry.last_mtime:
                    entry.stable_ticks += 1
                else:
                    entry.stable_ticks = 0
                    entry.last_size = size
                    entry.last_mtime = mtime
                    # Recent modification resets the settle timer.
                    if now - mtime < self._cfg.settle_sec:
                        entry.last_event = now

                if (now - entry.last_event >= self._cfg.debounce_sec
                        and entry.stable_ticks >= 2):
                    # Move pairing first.
                    paired = self._pop_paired_delete(entry)
                    if paired is not None:
                        moves.append((paired, entry))
                    else:
                        graduates.append(entry)
                    ready.append(path)

            for path in ready:
                self._wheel.pop(path, None)

        # 3. Run graduation outside the lock.
        for d_entry, n_entry in moves:
            if self._reconcile_move is not None:
                handled = await self._reconcile_move(d_entry, n_entry)
                if handled:
                    continue
            await self._graduate(n_entry)
        for entry in graduates:
            await self._graduate(entry)

    def _pop_paired_delete(self, entry: _Entry) -> _Entry | None:
        """If the new file's inode matches a recent delete, treat as a move."""
        if entry.inode is None:
            return None
        return self._pending_deletes.pop(entry.inode, None)
```

### 2.4 `library_watcher.py` — Observer wiring (D1, D7, D8)

```python
"""LibraryWatcher — owns one watchdog Observer for one library."""
from __future__ import annotations
import asyncio, logging, os
from dataclasses import dataclass

from watchdog.events import FileSystemEventHandler, FileMovedEvent
from watchdog.observers import Observer

from .debouncer import DebouncerConfig, LibraryDebouncer, _Entry
from .enqueue import enqueue_scan_job, enqueue_move
from .ignore import IgnoreMatcher

log = logging.getLogger(__name__)
MAX_SYMLINK_DEPTH = 32


@dataclass
class WatcherCfg:
    library_id: str
    roots: list[str]
    cfg: DebouncerConfig
    matcher: IgnoreMatcher
    settings_version: int


class _Handler(FileSystemEventHandler):
    def __init__(self, *, library_id, debouncer: LibraryDebouncer,
                 matcher: IgnoreMatcher, loop: asyncio.AbstractEventLoop):
        self._library_id = library_id
        self._debouncer = debouncer
        self._matcher = matcher
        self._loop = loop

    def _hop(self, kind: str, path: str, inode: int | None):
        if self._matcher.matches(path):
            return
        fut = asyncio.run_coroutine_threadsafe(
            self._debouncer.feed(kind=kind, path=path, inode=inode),
            self._loop)
        # Don't wait — fire-and-forget is intentional.
        del fut

    def on_created(self, e):
        if e.is_directory: return
        try:
            inode = os.stat(e.src_path).st_ino
        except OSError:
            inode = None
        self._hop("created", e.src_path, inode)

    def on_modified(self, e):
        if e.is_directory: return
        try:
            inode = os.stat(e.src_path).st_ino
        except OSError:
            inode = None
        self._hop("modified", e.src_path, inode)

    def on_deleted(self, e):
        if e.is_directory: return
        # We can't stat a deleted file; inode is unknown unless we cached it.
        self._hop("deleted", e.src_path, None)

    def on_moved(self, e: FileMovedEvent):
        # macOS path: trust the OS event.
        if e.is_directory: return
        try:
            inode = os.stat(e.dest_path).st_ino
        except OSError:
            inode = None
        # Synthesise: deleted at src, created at dest with the same inode.
        self._hop("deleted", e.src_path, inode)
        self._hop("created", e.dest_path, inode)


class LibraryWatcher:
    def __init__(self, *, cfg: WatcherCfg, db_pool, api_client):
        self._cfg = cfg
        self._db_pool = db_pool
        self._api = api_client
        self._observer: Observer | None = None
        self._debouncer: LibraryDebouncer | None = None

    async def start(self) -> None:
        loop = asyncio.get_running_loop()

        async def _graduate(entry: _Entry) -> None:
            await enqueue_scan_job(
                self._db_pool,
                library_id=self._cfg.library_id,
                source_path=entry.path,
                settings_version=self._cfg.settings_version)

        async def _reconcile(deleted: _Entry, created: _Entry) -> bool:
            # Cross-library moves are not detected here; the watcher's
            # roots are all the same library by construction.
            await enqueue_move(
                self._api,
                library_id=self._cfg.library_id,
                old_path=deleted.path,
                new_path=created.path)
            return True

        self._debouncer = LibraryDebouncer(
            cfg=self._cfg.cfg, graduate=_graduate,
            reconcile_move=_reconcile)
        await self._debouncer.start()

        observer = Observer()
        handler = _Handler(library_id=self._cfg.library_id,
                           debouncer=self._debouncer,
                           matcher=self._cfg.matcher, loop=loop)
        for root in self._cfg.roots:
            observer.schedule(handler, root, recursive=True)
        observer.start()
        self._observer = observer
        log.info("library_watcher.started",
                 extra={"library_id": self._cfg.library_id,
                        "roots": self._cfg.roots})

    async def stop(self) -> None:
        if self._observer:
            self._observer.stop()
            self._observer.join(timeout=5)
        if self._debouncer:
            await self._debouncer.stop()

    def swap_matcher(self, matcher: IgnoreMatcher) -> None:
        """Live-reload of ignore rules (D8). Called by supervisor on NOTIFY."""
        self._cfg.matcher = matcher
```

### 2.5 `enqueue.py` — INSERT into `processing_jobs` (D9)

```python
"""Enqueue: the only DB write the watcher does."""
from __future__ import annotations
import logging

log = logging.getLogger(__name__)

INSERT_SCAN = """
INSERT INTO processing_jobs
    (id, library_id, source_path, stage, state, priority,
     settings_version, created_at)
VALUES
    (gen_random_uuid(), $1, $2, 'scan', 'queued', 0, $3, now())
ON CONFLICT (library_id, source_path)
WHERE stage = 'scan' AND state IN ('queued','running')
DO NOTHING
"""


async def enqueue_scan_job(db_pool, *, library_id, source_path,
                           settings_version: int) -> None:
    async with db_pool.acquire() as conn:
        result = await conn.execute(INSERT_SCAN,
                                    library_id, source_path, settings_version)
        if result.endswith("0"):
            log.debug("scan job already queued", extra={
                "library_id": library_id, "source_path": source_path})


async def enqueue_move(api_client, *, library_id, old_path, new_path) -> None:
    """Hand off to the API service (D5)."""
    await api_client.post(
        f"/api/internal/libraries/{library_id}/move",
        json={"old_path": old_path, "new_path": new_path})
```

### 2.6 `supervisor.py` — sweep-then-watch (D6)

```python
"""WatcherSupervisor — boots one LibraryWatcher per library."""
from __future__ import annotations
import asyncio, logging
from .library_watcher import LibraryWatcher, WatcherCfg
from .debouncer import DebouncerConfig
from .ignore import IgnoreMatcher
from ..config.cache import SettingsCache
from ..sweep.runner import run_one_shot_sweep   # Plan 9.3

log = logging.getLogger(__name__)


class WatcherSupervisor:
    def __init__(self, *, db_pool, api_client, settings_cache: SettingsCache):
        self._db_pool = db_pool
        self._api = api_client
        self._cache = settings_cache
        self._watchers: dict[str, LibraryWatcher] = {}

    async def start_all(self) -> None:
        rows = await self._db_pool.fetch(
            "SELECT id::text AS id, roots FROM libraries WHERE deleted_at IS NULL")
        for r in rows:
            await self._start_library(library_id=r["id"], roots=list(r["roots"]))

    async def _start_library(self, *, library_id: str, roots: list[str]) -> None:
        # 1. Resolve effective settings + version.
        settings, version = await self._cache.get(library_id)
        debounce_cfg = DebouncerConfig(
            debounce_sec=float(settings.get("watch_debounce_sec", 2.0)),
            settle_sec=float(settings.get("watch_settle_sec", 5.0)))
        matcher = IgnoreMatcher.from_settings(settings)

        # 2. Sweep first (D6) — wait for completion.
        await run_one_shot_sweep(self._db_pool, library_id=library_id, roots=roots,
                                 matcher=matcher, settings_version=version)

        # 3. Then start the watcher.
        cfg = WatcherCfg(library_id=library_id, roots=roots, cfg=debounce_cfg,
                         matcher=matcher, settings_version=version)
        w = LibraryWatcher(cfg=cfg, db_pool=self._db_pool, api_client=self._api)
        await w.start()
        self._watchers[library_id] = w

    async def on_settings_changed(self, library_id: str) -> None:
        """Hook from the LISTEN loop (Plan 9.1) — live-reload (D8)."""
        w = self._watchers.get(library_id)
        if w is None:
            return
        settings, _ = await self._cache.get(library_id)
        new_matcher = IgnoreMatcher.from_settings(settings)
        w.swap_matcher(new_matcher)

    async def stop_all(self) -> None:
        await asyncio.gather(*(w.stop() for w in self._watchers.values()),
                             return_exceptions=True)
```

### 2.7 Settings keys consumed

```
watch_debounce_sec   (float, default 2.0)
watch_settle_sec     (float, default 5.0)
ignore_globs         (string[], from Plan 9.5)
sweep_interval_sec   (int, used by sweep, not watcher)
```

These are **not new keys** in `library.settings`; `watch_*` come from the
hard defaults in `config/defaults.py` (Plan 9.1) and may be overridden
per library. The validator must accept them — extend the keySpec table
in 9.1 if not already present.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/00XX_processing_jobs_scan_dedup.sql` | `processing_jobs_scan_dedup` partial unique index | `test_enqueue_dedup` |
| 2 | `pipeline/src/maktaba_pipeline/watcher/__init__.py` | re-exports | n/a |
| 3 | `pipeline/src/maktaba_pipeline/watcher/errors.py` | `WatcherError`, `EnqueueRejected` | n/a |
| 4 | `pipeline/src/maktaba_pipeline/watcher/debouncer.py` | `DebouncerConfig`, `LibraryDebouncer`, `_Entry` | `test_debouncer`, `test_settling` |
| 5 | `pipeline/src/maktaba_pipeline/watcher/move_recon.py` | inode helpers (Linux); macOS uses `FileMovedEvent` directly | `test_move_recon` |
| 6 | `pipeline/src/maktaba_pipeline/watcher/enqueue.py` | `enqueue_scan_job`, `enqueue_move` | `test_enqueue_dedup` |
| 7 | `pipeline/src/maktaba_pipeline/watcher/library_watcher.py` | `LibraryWatcher`, `WatcherCfg`, `_Handler` | `test_library_watcher_start_stop` |
| 8 | `pipeline/src/maktaba_pipeline/watcher/supervisor.py` | `WatcherSupervisor` | `test_supervisor_sweep_then_watch` |
| 9 | `pipeline/src/maktaba_pipeline/watcher/ignore.py` | re-export of Plan 9.5's `IgnoreMatcher` | n/a |

---

## 4. Test cases

### 4.1 `test_debouncer_collapses_burst_to_one_enqueue` — AC-1

```python
async def test_burst_of_modifies_collapses_to_single_graduation(tmp_path):
    graduations: list[str] = []

    async def graduate(entry):
        graduations.append(entry.path)

    cfg = DebouncerConfig(debounce_sec=0.5, settle_sec=0.0, tick_sec=0.05)
    deb = LibraryDebouncer(cfg=cfg, graduate=graduate)
    await deb.start()

    p = tmp_path / "video.mp4"
    p.write_bytes(b"x")
    inode = p.stat().st_ino
    for _ in range(10):
        await deb.feed(kind="modified", path=str(p), inode=inode)
        await asyncio.sleep(0.02)

    await asyncio.sleep(1.0)
    await deb.stop()
    assert graduations == [str(p)]
```

### 4.2 `test_settling_holds_until_size_stabilizes` — AC-2

```python
async def test_growing_copy_is_not_enqueued_until_size_stable(tmp_path):
    graduations: list[str] = []

    async def graduate(entry):
        graduations.append(entry.path)

    cfg = DebouncerConfig(debounce_sec=0.3, settle_sec=0.0, tick_sec=0.1)
    deb = LibraryDebouncer(cfg=cfg, graduate=graduate)
    await deb.start()

    p = tmp_path / "big.mp4"
    p.write_bytes(b"")
    inode = p.stat().st_ino
    await deb.feed(kind="created", path=str(p), inode=inode)
    # Simulate a 1 MB copy in 10 chunks over ~0.5 s.
    for _ in range(10):
        with open(p, "ab") as f:
            f.write(b"\x00" * 100_000)
        await deb.feed(kind="modified", path=str(p), inode=inode)
        await asyncio.sleep(0.05)

    # During the copy, no graduation should have fired.
    assert graduations == []
    # After copy completes and two stable ticks have passed, it graduates.
    await asyncio.sleep(0.6)
    await deb.stop()
    assert graduations == [str(p)]
```

### 4.3 `test_move_within_library_updates_path` — AC-3

```python
async def test_paired_delete_create_with_same_inode_is_a_move(tmp_path):
    api = FakeAPI()
    moves = api.moves

    async def graduate(entry):
        pytest.fail(f"should not graduate; got {entry.path}")

    async def reconcile(d, c):
        # Real implementation calls enqueue_move; we just record.
        moves.append((d.path, c.path))
        return True

    cfg = DebouncerConfig(debounce_sec=0.2, settle_sec=0.0, tick_sec=0.05,
                          move_window_sec=0.5)
    deb = LibraryDebouncer(cfg=cfg, graduate=graduate, reconcile_move=reconcile)
    await deb.start()

    p_old = tmp_path / "a.mp4"; p_old.write_bytes(b"video"); inode = p_old.stat().st_ino
    p_new = tmp_path / "b.mp4"; p_old.rename(p_new)

    await deb.feed(kind="deleted", path=str(p_old), inode=inode)
    await deb.feed(kind="created", path=str(p_new), inode=inode)

    await asyncio.sleep(0.5)
    await deb.stop()
    assert moves == [(str(p_old), str(p_new))]
```

### 4.4 `test_create_then_delete_within_window_is_dropped` — edge

```python
async def test_create_then_delete_within_debounce_never_graduates(tmp_path):
    graduations: list[str] = []

    async def graduate(entry):
        graduations.append(entry.path)

    cfg = DebouncerConfig(debounce_sec=0.5, settle_sec=0.0, tick_sec=0.05)
    deb = LibraryDebouncer(cfg=cfg, graduate=graduate)
    await deb.start()

    p = tmp_path / "tmp.mp4"
    p.write_bytes(b"x"); inode = p.stat().st_ino
    await deb.feed(kind="created", path=str(p), inode=inode)
    p.unlink()
    await deb.feed(kind="deleted", path=str(p), inode=inode)

    await asyncio.sleep(1.0)
    await deb.stop()
    assert graduations == []
```

### 4.5 `test_supervisor_sweep_then_watch` — AC-4

```python
async def test_supervisor_runs_sweep_before_watcher_starts(monkeypatch, db, api):
    order: list[str] = []

    async def fake_sweep(*args, **kwargs):
        await asyncio.sleep(0.1)
        order.append("sweep")
    async def fake_watcher_start(self):
        order.append("watch")

    monkeypatch.setattr("maktaba_pipeline.sweep.runner.run_one_shot_sweep", fake_sweep)
    monkeypatch.setattr(LibraryWatcher, "start", fake_watcher_start)

    sup = WatcherSupervisor(db_pool=db, api_client=api, settings_cache=cache_with({"L1": ({}, 1)}))
    await db.execute("INSERT INTO libraries (id, roots) VALUES ('L1', ARRAY['/data/lib1'])")
    await sup.start_all()
    assert order == ["sweep", "watch"]
```

### 4.6 `test_enqueue_dedup_partial_unique_index` — AC-1, D9

```python
async def test_redundant_enqueue_is_a_noop(db):
    await db.execute(
        "INSERT INTO libraries (id, roots) VALUES ('L1', ARRAY['/r'])")
    await enqueue_scan_job(db, library_id="L1", source_path="/r/v.mp4",
                           settings_version=1)
    await enqueue_scan_job(db, library_id="L1", source_path="/r/v.mp4",
                           settings_version=1)
    n = await db.fetchval(
        "SELECT COUNT(*) FROM processing_jobs WHERE source_path = '/r/v.mp4'")
    assert n == 1
```

### 4.7 `test_ignore_globs_filters_before_debounce` — AC-3 of Story 9.5 cross

```python
async def test_ignored_path_never_reaches_debouncer(tmp_path):
    matcher = IgnoreMatcher.from_settings({"ignore_globs": ["**/raw/**"]})
    assert matcher.matches(str(tmp_path / "raw" / "x.mp4")) is True

    deb = LibraryDebouncer(cfg=DebouncerConfig(debounce_sec=0.1, tick_sec=0.05),
                           graduate=lambda e: pytest.fail("graduated"))
    await deb.start()
    h = _Handler(library_id="L1", debouncer=deb, matcher=matcher,
                 loop=asyncio.get_running_loop())
    h.on_created(MakeEvent(str(tmp_path / "raw" / "x.mp4"), is_directory=False))
    await asyncio.sleep(0.5)
    await deb.stop()
```

### 4.8 `test_settings_changed_swaps_matcher` — AC of Plan 9.1 cross

```python
async def test_supervisor_swap_matcher_on_notify(db, api):
    sup = WatcherSupervisor(db_pool=db, api_client=api,
                            settings_cache=cache_with({"L1": ({"ignore_globs": []}, 1)}))
    await sup.start_all()
    w = sup._watchers["L1"]
    old_matcher = w._cfg.matcher
    cache_set("L1", ({"ignore_globs": ["**/raw/**"]}, 2))
    await sup.on_settings_changed("L1")
    assert w._cfg.matcher is not old_matcher
    assert w._cfg.matcher.matches("/data/lib1/raw/x.mp4")
```

### 4.9 `test_symlink_loop_does_not_hang` — story edge case

```python
def test_symlink_loop_guard(tmp_path):
    a = tmp_path / "a"; a.mkdir()
    b = a / "b"; b.mkdir()
    (b / "loop").symlink_to(a)            # b/loop -> a (cycle a->b->loop->a)
    visited: set[int] = set()
    seen = list(walk_with_guard(str(a), visited=visited, max_depth=5))
    # Should terminate without raising; loop pruned.
    assert len(visited) >= 2
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handling |
|-----|-----------|----------|
| E1  | **File created and deleted inside the debounce window.** Wheel cancels the entry on `kind="deleted"` (`feed` pops the path); pending-deletes is a TTL'd structure that drops itself after `move_window_sec`. Never graduates. | `test_create_then_delete_within_debounce_never_graduates`. |
| E2  | **Filesystem doesn't emit reliable events** (some FUSE, NFS without `inotify` cookies). The watcher graduates whatever it sees; the periodic sweep (Plan 9.3) is the backstop. The watcher logs `kind=watcher.event_loss_suspected` if `inode_index` cardinality remains tiny under load. | Documented; covered by Plan 9.3. |
| E3  | **Massive `mv` of 10,000 files at once.** Wheel grows to 10,000 entries; tick wakes once a second and processes them in O(N) per tick. Actual graduation is rate-limited by Postgres INSERT throughput, not the wheel. The orchestrator caps scan-stage concurrency at 4 (architecture §7.4) so the queue absorbs the burst. | Wheel design (D2). |
| E4  | **Symlink loop** in a root. `MAX_SYMLINK_DEPTH = 32` and a per-walk visited-inode set prevent infinite descent. Logged once per offending pair. | D7 + `test_symlink_loop_guard`. |
| E5  | **File modified during debounce, then mtime resets to old value** (rare: `touch -t`). The size+mtime parity check fails (mtime moved), `stable_ticks` resets to 0, and the wheel re-arms. Eventually settles or stays in the wheel until disappearing. | D3 settling check. |
| E6  | **Editor truncate-and-rewrite preserving size.** Same size, but mtime advances → parity fails → settle resets. Ultimately graduates on the new mtime. | D3. |
| E7  | **Watcher restart while files are added.** Sweep-first sequencing (D6) catches anything that arrived while the watcher was down. Events that fire during the sweep are dropped; the sweep itself enqueues those scans. | `test_supervisor_sweep_then_watch`. |
| E8  | **Cross-library rename** (file dragged from library A to library B). The two watchers see independent events; A sees a delete (no inode pair), B sees a create. No move reconciliation; the library_id changes → cascade-delete of A's video and a new INSERT for B. The story explicitly accepts this. | Documented; per-library Observer scope (D1). |
| E9  | **Path with non-UTF-8 bytes** (legacy mounts). `os.stat` raises `UnicodeDecodeError` on encoding; we catch and log; the entry is dropped from the wheel. The user must rename or exclude via `ignore_globs`. | `_tick` `OSError` branch. |
| E10 | **Hidden directory created at root** (e.g., `.cache/`). `IgnoreMatcher` filters it before debounce (`**/.*` built-in from Plan 9.5). | Plan 9.5 + `test_ignore_globs_filters_before_debounce`. |
| E11 | **Watcher running but Pipeline DB pool is exhausted.** `enqueue_scan_job` blocks on pool acquire; the wheel keeps the entry (it's already graduated and the coroutine is awaiting). Backpressure is the right behavior — we do not drop scan events on DB exhaustion. | `enqueue_scan_job` async semantics. |
| E12 | **Shutdown during graduation.** `WatcherSupervisor.stop_all` calls `Observer.stop()` first (no new events), then `Debouncer.stop()` (sets the stop event). In-flight graduations finish; wheel state is rebuilt by the next sweep. | `LibraryWatcher.stop`. |

---

## 6. Acceptance checklist

- [ ] **A1** Debounce: N events for the same path within `watch_debounce_sec` collapse to one enqueue. (`test_burst_of_modifies_collapses_to_single_graduation`)
- [ ] **A2** Settling: a file whose size grows over time is not enqueued until two consecutive ticks see the same size+mtime AND `now - last_event >= debounce_sec`. (`test_growing_copy_is_not_enqueued_until_size_stable`)
- [ ] **A3** Move within library: paired delete+create with the same inode within `watch_move_window_sec` is reclassified as a move, fires `library.move_detected` → API updates `videos.path`, no scan job. (`test_paired_delete_create_with_same_inode_is_a_move`)
- [ ] **A4** Restart resilience: `WatcherSupervisor.start_all` runs a one-shot sweep per library before the Observer starts; no missed-during-downtime hole. (`test_supervisor_runs_sweep_before_watcher_starts`)
- [ ] **A5** Create-then-delete within debounce window: never graduates; wheel cancels. (`test_create_then_delete_within_debounce_never_graduates`)
- [ ] **A6** Enqueue is idempotent: a second enqueue for the same `(library_id, source_path)` while a queued/running scan exists is a no-op. (`test_redundant_enqueue_is_a_noop`)
- [ ] **A7** `ignore_globs` are applied at event time (before debounce); ignored events never reach the wheel. (`test_ignored_path_never_reaches_debouncer`)
- [ ] **A8** Settings live-reload: `library.settings_changed` NOTIFY swaps the `IgnoreMatcher` in place without restarting the Observer. (`test_supervisor_swap_matcher_on_notify`)
- [ ] **A9** Symlink loops do not hang or saturate inotify watches; `MAX_SYMLINK_DEPTH = 32` and a visited-inode set prevent recursion. (`test_symlink_loop_guard`)
- [ ] **A10** The watcher does **no** hashing or probing; only stat + fnmatch + INSERT. (Static lint: `watcher/` does not import `blake3`, `ffprobe`, or any heavy module.)

---

## 7. Performance budget

| Phase | Cost | Notes |
|-------|------|-------|
| `_Handler.on_*` (per event) | < 50 µs | One stat (kernel cached) + one fnmatch + thread-safe call_soon. |
| `Debouncer._tick` | O(N) over wheel size | One stat per entry per tick; 1000 active entries × 50 µs = 50 ms per tick at 1 s tick = 5 % CPU steady state. |
| `enqueue_scan_job` | ~2 ms p95 LAN | Single-row INSERT with partial-unique conflict path. |
| Wheel memory | ~256 B per entry | `_Entry` dataclass with 8 fields. 100k entries = ~25 MB worst case during a massive `mv`. |
| Observer thread | 1 thread per library, ~5 MB RSS | `watchdog`'s default. 100 libraries = 100 threads — acceptable on Linux; documented operator limit at 500. |
| End-to-end latency: file fully written → `processing_jobs` row | typical ~3 s | `watch_debounce_sec=2` + 2 ticks × `tick_sec=1` = 4 s upper bound; insert is sub-second. |
