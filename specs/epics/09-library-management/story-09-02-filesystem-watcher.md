# Story 9.2 — Filesystem watcher

Per-library `watchdog` observer in the Pipeline Service. The single
hardest piece is *not picking up files mid-write*.

**AC-1 — Debounced event emission.**
- **Given** a `watchdog` event for a path,
- **When** the watcher receives it,
- **Then** the event is queued; if no further event for the same path
  arrives within `watch_debounce_sec` (default 2 s) and the file's size
  has been stable for that interval, an `enqueue-scan` job is created.

**AC-2 — Settling check.**
- **Given** a copy in progress (size grows by N bytes/s),
- **When** the watcher queries size at debounce-tick time,
- **Then** the file is *not* enqueued until two consecutive ticks see the
  same size. Files modified within the last `watch_settle_sec` (default
  5 s) are re-checked rather than enqueued.

**AC-3 — Move detection within a library.**
- **Given** a file moved within the same library root (and the OS emits
  paired `deleted` + `created` events with the same inode on Linux, or a
  `moved` event on macOS),
- **When** the watcher processes the pair,
- **Then** the existing `videos.path` is updated; no scan job is
  enqueued; no derived data is recomputed.

**AC-4 — Watcher restart resilience.**
- **Given** the Pipeline restarts while files were added during downtime,
- **When** the watcher boots,
- **Then** a one-shot full sweep (Story 9.3) catches up, and the watcher
  begins emitting events for further changes. No "missed-during-downtime"
  hole.

**Test cases:**
- Unit: debounce queue collapses N events for the same path within the
  window into one enqueue.
- Integration: simulate a 100 MiB copy completing over 3 s — the file is
  enqueued exactly once, after copy completes.
- Integration: rename across roots within one library — single update
  to `videos.path`; no scan.
- Integration: rename across libraries — treated as delete+add (because
  `library_id` changes); old derived rows are cascaded by FK.

**Edge cases:**
- File created and deleted within the debounce window — never enqueued;
  the watcher cancels the pending tick.
- A file system that doesn't emit reliable events (some FUSE mounts) —
  the periodic full sweep (Story 9.3) is the backstop. Document the
  failure mode in operations.
- Massive `mv` of 10,000 files at once — the watcher coalesces by
  parent dir; the orchestrator picks them up over time, capped by the
  scan stage's concurrency (4 by §7.4).
- Symlink loops in a root — followed by `watchdog`'s `recursive` mode but
  must be guarded; we use a per-scan visited-inode set to prevent
  infinite recursion.
