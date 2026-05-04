# Implementation Plan — Story 19.6 Storage Scaling & Large Library Handling

> Companion to [story-19-06-storage-scaling.md](story-19-06-storage-scaling.md).
> 30 TB cold scan ≤ 30 min, BLAKE3 content_hash from edges + size, no
> re-process on rename, debounced watcher.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Hash | `blake3(first_4MiB ‖ size_le8 ‖ last_4MiB)`. |
| Scanner | Python (`pipeline/maktaba_pipeline/scan/`); concurrent workers; bounded memory. |
| Watcher | `watchdog` library; debounce window 2 s. |
| Identity move | `videos.content_hash UNIQUE`; `videos.path` is mutable. Rename = UPDATE path. |

## 1. Project layout

```
pipeline/maktaba_pipeline/scan/
├── hash.py                 # content_hash
├── walker.py               # bounded-memory tree walk
├── scanner.py              # orchestrator
├── watcher.py              # FS event debouncer
├── tests/
│   ├── test_hash.py
│   ├── test_walker.py
│   ├── test_scanner_perf.py
│   ├── test_watcher_debounce.py
│   └── fixtures/
└── ...
```

## 2. content_hash

The hash construction order — `head ‖ size_le8 ‖ tail` — is part of the
**identity contract** (matches Story 19.6 AC2). Anything that depends on
`videos.content_hash` (cache keys, segment paths, ChromaDB collection ids)
relies on this exact byte order. Reordering or reformatting the size field
is a breaking change and requires a migration.

```python
# scan/hash.py
import blake3, struct

EDGE_BYTES = 4 * 1024 * 1024

def content_hash(path: Path) -> str:
    """blake3(head ‖ size_le8 ‖ tail) — see Story 19.6 AC2.

    The order is fixed:
      1. read up to 4 MiB from offset 0 → `head`
      2. pack `size` as little-endian uint64 → `size_le8`
      3. read up to 4 MiB ending at EOF → `tail`
      4. update the hasher in the order: head, size_le8, tail
    """
    h = blake3.blake3()
    size = path.stat().st_size

    with path.open("rb") as f:
        head = f.read(EDGE_BYTES)
        if size > 2 * EDGE_BYTES:
            f.seek(size - EDGE_BYTES)
            tail = f.read(EDGE_BYTES)
        elif size > EDGE_BYTES:
            f.seek(EDGE_BYTES)
            tail = f.read(size - EDGE_BYTES)
        else:
            tail = b""
        # Contract order: head, size_le8, tail.
        h.update(head)
        h.update(struct.pack("<Q", size))
        h.update(tail)
    return h.hexdigest()
```

EC handling:
- Zero-byte file → head=`""`, tail=`""`, size=0; hash deterministic.
- Exactly 8 MiB → head=4 MiB, then read remaining 4 MiB as tail; size sandwiched between.
- Sparse files → reads see zeros for holes; that's fine for identity.

TC3 verification: two files, identical edges, sizes differ → hash differs because of size sandwich.

## 3. Bounded-memory walker

```python
# scan/walker.py
def walk(root: Path, *, follow_symlinks: bool = False) -> Iterator[Path]:
    """Iterative DFS using a deque; bounded RSS regardless of tree depth."""
    stack = collections.deque([root])
    while stack:
        cur = stack.pop()
        try:
            with os.scandir(cur) as it:
                for entry in it:
                    try:
                        if entry.is_dir(follow_symlinks=follow_symlinks):
                            stack.append(Path(entry.path))
                        elif entry.is_file(follow_symlinks=follow_symlinks):
                            yield Path(entry.path)
                    except OSError:
                        continue                                # EC3 deleted mid-scan
        except (PermissionError, FileNotFoundError):
            continue
```

`scandir` returns one dir's children at a time; never holds the full tree.

## 4. Scanner orchestrator

```python
# scan/scanner.py
@dataclass
class ScanConfig:
    roots: list[Path]
    concurrency: int = 8
    skip_younger_than_s: int = 30
    per_file_timeout_s: int = 60
    debounce_s: float = 2.0

async def scan(cfg: ScanConfig, db, on_video) -> ScanReport:
    sem = asyncio.Semaphore(cfg.concurrency)
    metrics = ScanMetrics()
    cutoff = time.time() - cfg.skip_younger_than_s

    async def handle(path: Path):
        async with sem:
            try:
                st = await asyncio.to_thread(path.stat)
                if st.st_mtime > cutoff: return                # EC1 still-writing
                hash_ = await asyncio.wait_for(
                    asyncio.to_thread(content_hash, path),
                    timeout=cfg.per_file_timeout_s)             # EC2 SMB hang
            except (asyncio.TimeoutError, OSError):
                metrics.requeued += 1
                return await db.requeue(path)
            await db.upsert_video(hash_, path, st)              # path mutable; hash unique
            on_video(path, hash_)

    tasks = []
    for root in cfg.roots:
        for p in walker.walk(root):
            if p.suffix.lower() in VIDEO_EXTS:
                tasks.append(asyncio.create_task(handle(p)))
                if len(tasks) >= 1024:
                    await asyncio.gather(*tasks); tasks.clear()
    await asyncio.gather(*tasks)
    return metrics.report()
```

`db.upsert_video`:

```sql
-- Canonical column names (arch §8.5):
--   library_id  UUID NOT NULL
--   filename    TEXT NOT NULL          (basename derived from path)
--   size_bytes  BIGINT NOT NULL        (NOT `size`)
--   mtime       TIMESTAMPTZ
INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (content_hash) DO UPDATE
   SET path = EXCLUDED.path,
       filename = EXCLUDED.filename,
       mtime = EXCLUDED.mtime
   WHERE videos.path IS DISTINCT FROM EXCLUDED.path
```

`library_id` is required (NOT NULL) — the scanner must resolve which library
a path belongs to before insert. Returns `(inserted | path_updated | unchanged)`.
Only `inserted` enqueues processing jobs.

## 5. Watcher with debounce

```python
# scan/watcher.py
#
# IMPORTANT: `watchdog` invokes `on_any_event` on its own observer thread —
# NOT on the asyncio event loop. `asyncio.get_running_loop()` raises
# `RuntimeError` when called from a non-loop thread, so the loop must be
# captured on the loop thread (e.g. at `start()` time) and posted to via
# `asyncio.run_coroutine_threadsafe(...)` from the watchdog thread.
class DebouncedHandler(FileSystemEventHandler):
    def __init__(self, scanner, loop: asyncio.AbstractEventLoop, debounce_s: float = 2.0):
        # `loop` MUST be captured on the asyncio thread by the caller and
        # passed in here; we never call get_running_loop() from the watchdog
        # thread.
        self._pending: dict[Path, asyncio.Handle] = {}
        self._loop = loop
        self.scanner = scanner
        self.debounce = debounce_s

    def on_any_event(self, event):
        # Runs on the watchdog observer thread.
        if event.is_directory: return
        path = Path(event.src_path)
        if event.event_type == "moved":
            path = Path(event.dest_path)

        def schedule():
            # Runs on the loop thread.
            if old := self._pending.pop(path, None): old.cancel()
            if event.event_type == "moved":
                if old := self._pending.pop(Path(event.src_path), None): old.cancel()
            self._pending[path] = self._loop.call_later(
                self.debounce,
                lambda p=path: asyncio.ensure_future(self._flush(p), loop=self._loop),
            )

        # Cross-thread: post `schedule` onto the captured loop.
        self._loop.call_soon_threadsafe(schedule)

    async def _flush(self, path: Path):
        self._pending.pop(path, None)
        if path.exists():
            await self.scanner.handle_one(path)
```

Caller wiring:

```python
async def main():
    loop = asyncio.get_running_loop()                      # captured on loop thread
    handler = DebouncedHandler(scanner, loop, debounce_s=2.0)
    observer = Observer()
    observer.schedule(handler, str(root), recursive=True)
    observer.start()                                       # spins up watchdog thread
```

AC4: `.tmp.partial → .mp4` rename emits one job. The debouncer collapses the create+move because the timer for `.tmp.partial` is cancelled when the move event fires for `.mp4` and the `.partial` is no longer present at flush time.

## 6. Performance budget

The AC1 30-min wall-clock budget covers the **full** scan: directory
enumeration, hashing, and DB upsert — not just hashing.

| Stage | Target | Implementation |
|---|---|---|
| `scandir` walk | 50 k entries in ≤ 30 s | iterative DFS, no recursion. |
| `content_hash` | ≤ 30 ms/file on local SSD | 8 MiB total read. |
| DB upsert (per file) | ≤ 5 ms/file (warm pool) | single statement, indexed `content_hash`; batched in groups of 100. |
| End-to-end 30 TB / 50 k files | ≤ 30 min | concurrency=8 hashers + a dedicated DB writer task. |
| Peak RSS | ≤ 800 MiB | bounded by `concurrency × (8 MiB read buffer + asyncio task overhead)`. |

Throughput math:

  - Walk: 50 k entries via `scandir` ≈ 30 s.
  - Hash: 50 k files × 30 ms / 8 workers ≈ 187 s.
  - Upsert: 50 k rows × 5 ms / 4 batched conns ≈ 62 s.
  - Sequential floor: ~280 s; with overlap (walk feeds hashers, hashers feed
    upsert) the wall-clock target is ≤ 30 min with comfortable headroom for
    slow paths and SMB hiccups.

## 7. Test cases

### TC1 — Cold scan budget
`tests/scan/test_scanner_perf.py`:

```python
@pytest.mark.perf
def test_cold_scan_30tb_50k_files(tmp_path):
    synthesize_50k_uniquely_hashable_files(tmp_path, total_logical_bytes=30 * TB)
    t0 = time.perf_counter()
    rss_max = 0
    rep = asyncio.run(scan(cfg(roots=[tmp_path]), db, lambda *_: None))
    elapsed = time.perf_counter() - t0
    assert elapsed <= 30 * 60
    assert rep.peak_rss_bytes <= 800 * MIB
    assert rep.inserted == 50_000
```

`synthesize_50k_uniquely_hashable_files` writes 4 MiB head + 4 MiB tail with sequence-based content; the rest is `truncate -s` (sparse). Logical sizes vary across the 50k files to produce varied content_hashes.

### TC2 — Identity stability under rename
Seed 1,000 files. Run scan. Rename every file to a new path. Run scan again. Assert: `videos.content_hash` set unchanged; `processing_jobs` row count unchanged.

### TC3 — Pathological content
Two files: same first/last 4 MiB (file 1 size=10 MiB, file 2 size=20 MiB; middle bytes irrelevant). Hash file 1, hash file 2. Assert hashes differ.

### EC1 — Still-writing skip
File `partial.mp4` with `mtime = now()`. Scan; assert: not enqueued. Set `mtime = now() - 60s`; rescan; enqueued.

### EC2 — Per-file timeout
Mock path with `read()` that sleeps 90 s. Scan; assert: file requeued with reason `hash_timeout`; metric `scan_hash_timeout_total` incremented.

### EC3 — Deleted mid-scan
Walker yields path; before handle runs, `os.unlink(path)`. Assert: handle catches `FileNotFoundError`, debug-logged, no error counter incremented.

### EC4 — Watcher debounce
`touch f.tmp.partial; mv f.tmp.partial f.mp4`. Assert: only one `handle_one` invocation, after 2 s.

## 8. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 still-writing | story | `mtime` cutoff (configurable). |
| EC2 SMB hang | story | `asyncio.wait_for(..., timeout=60s)` + requeue. |
| EC3 deleted mid-scan | story | `try/except OSError` graceful skip. |
| Symlink loops | impl | `follow_symlinks=False` default. |
| Path encoding (NFC vs NFD) on macOS | impl | Normalise to NFC before DB write; tested with Arabic filenames. |

## 9. Configuration

```yaml
scan:
  roots: ["/srv/media/maktaba"]
  concurrency: 8
  skip_younger_than_s: 30
  per_file_timeout_s: 60
  watcher:
    debounce_s: 2.0
    fallback_poll_interval_s: 30
  video_exts: [.mp4, .mkv, .mov, .avi, .webm, .ts, .m4v]
```

## 10. Metrics

| Metric | Type | Notes |
|---|---|---|
| `scan_files_inspected_total` | counter | |
| `scan_files_inserted_total` | counter | new content_hash. |
| `scan_files_path_updated_total` | counter | rename detected. |
| `scan_hash_timeout_total` | counter | EC2. |
| `scan_peak_rss_bytes` | gauge | per scan. |
| `scan_duration_seconds` | histogram | per scan. |
| `watcher_mode{mode="watch","poll"}` | gauge | |

## 11. Dependencies

- `blake3` Python library.
- `watchdog` library.
- Story 19.1 (capacity floor).
- Epic 1 scanner / Epic 9 library management.
