# Story 9.3 — Periodic full sweep

A sparse periodic walk (default every 6 h per §3.1) that catches anything
the live watcher missed: NFS event drops, mount remounts, files moved in
while Pipeline was down.

**AC-1 — Diff against catalog.**
- **Given** a library root and the current `videos` catalog,
- **When** the sweep runs,
- **Then** for each file: if `(path, size, mtime)` matches an existing
  row, skip; if `path` is new but a row with the same `content_hash`
  exists at a different path, treat as a move (update `path`); else
  enqueue a `scan` job. A file present in the catalog but missing on
  disk transitions to `state=MISSING` (per the FSM update — see also
  Pipeline Story 1.5).

**AC-2 — Sweep is single-flight.**
- **Given** a sweep is in progress,
- **When** the next tick fires before completion,
- **Then** the new tick is dropped (logged at info). No two concurrent
  sweeps.

**AC-3 — Configurable interval.**
- **Given** a library with `sweep_interval_sec` set,
- **When** the scheduler runs,
- **Then** the per-library interval overrides the default. `0` disables
  periodic sweeps (manual scan only).

**AC-4 — Sweep telemetry.**
- **Given** any sweep,
- **When** complete,
- **Then** a row is written to `library_sweeps` (schema in
  [README.md](README.md)). Surfaced via Story 9.7's stats and the
  `library_stats_cache.last_sweep` summary.

**Test cases:**
- Integration: 100k-file fixture (mostly already-indexed) completes in
  under 30 s on a local SSD (size+mtime cheap path).
- Integration: a deleted file is detected and the matching row is
  marked `state='MISSING'` (not deleted; user must purge).

**Edge cases:**
- A file whose size+mtime matches but BLAKE3 has changed (rare, unless
  a tool rewrites preserving mtime) — the size+mtime fast path may miss
  this. The user can force a hash-rescan via `POST /api/libraries/{id}/scan?rehash=true`.
- A NAS mount that takes 30 s to wake up — the sweep blocks; the
  watcher buffers events.
- Two libraries with overlapping roots (rejected at create per Story
  7.3 AC-2 + Story 9.16) — guarantees this story doesn't see the same
  file twice.
