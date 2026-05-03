# Story 8.14 — Cache layout and LRU GC

Per §4.8: bounded on-disk cache, default 50 GiB combined across remux,
posters, sprites, thumbs (HLS per-session is excluded from the cap and
purged on close per story 8.9).

**AC-1 — Layout.**
- **Given** the cache root,
- **When** files are written,
- **Then** they live at the §4.8 paths exactly, with two-char hash
  shards to avoid wide directories.

**AC-2 — LRU eviction.**
- **Given** the cache exceeds `max_gib`,
- **When** the GC runs (every 5 min),
- **Then** files are deleted in least-recently-accessed order until
  usage is below `max_gib * 0.9` (10% headroom). atime is read from
  `os.Stat`; on filesystems without atime tracking (`noatime`), the
  GC falls back to mtime + a per-file access counter kept in a small
  bbolt sidecar.

**AC-3 — Per-tier soft caps.**
- **Given** the cache is approaching its cap and 80% of bytes are remux
  files,
- **When** GC runs,
- **Then** remux is preferentially evicted (it's regenerable in seconds);
  posters and sprites have a soft floor of 1 GiB before they're evicted
  (regeneration requires re-running the Pipeline).

**AC-4 — Manual GC.**
- **Given** the operator runs `maktaba-streaming gc`,
- **When** invoked,
- **Then** GC runs immediately and prints `{evicted_files, freed_gib,
  duration_ms}` to stdout.

**Test cases:**
- Integration: fill cache to 60 GiB with 30 GiB cap → GC reduces to 27
  GiB.
- Integration: priority — remux at 8 GiB, posters at 0.5 GiB, cap 5 GiB
  → posters survive, remux evicted.
- Integration: GC respects in-flight reads (file is unlinked but the
  open FD continues to work).

**Edge cases:**
- Cap lowered below current usage at runtime — GC catches up in the
  next tick; aggressive single-pass GC is gated on a flag to avoid IO
  storms.
- Concurrent writes during GC — file is written under a `.tmp` name and
  rename'd atomically; GC ignores `.tmp` files younger than 1 min.
- ENOSPC on cache write — returns 507 `Insufficient Storage`; emits a
  metric; force-runs GC immediately.
