# Story 9.6 — Manual scan trigger and scan progress

`POST /api/libraries/{id}/scan` is the user-initiated entry point
(§9.1). This story defines the job's progress reporting and the
`?rehash=true` mode.

**AC-1 — Default mode.**
- **Given** a manual scan request,
- **When** processed,
- **Then** a `scan` job is enqueued at priority 50 (Epic 7 Story 7.3
  AC-5), the worker walks the roots, applies the size+mtime fast path of
  Story 9.3, and only computes BLAKE3 for new/changed files.

**AC-2 — Rehash mode.**
- **Given** `?rehash=true`,
- **When** processed,
- **Then** every file is re-hashed regardless of size+mtime, and a
  `videos` row whose hash no longer matches the file is split into a
  new row + the old row marked `state='SUPERSEDED'`. Used after a
  filesystem corruption or a tool that rewrote files in place.

**AC-3 — Progress reporting.**
- **Given** a scan in flight,
- **When** the worker updates progress,
- **Then** `processing_jobs.processed_seconds` is repurposed to mean
  "files scanned"; `total_duration_seconds` to mean "files to scan"
  (estimated via a fast `find` count first). The §7.10 WS event shape is
  preserved.

**Test cases:**
- Integration: a scan over 1000 files reports progress at 1 Hz to the WS.
- Integration: `?rehash=true` against a corrupted file detects the hash
  mismatch and supersedes correctly.

**Edge cases:**
- Scan started while watcher events are in-flight — both processes update
  the same `videos` table; an `INSERT … ON CONFLICT (content_hash) DO
  UPDATE SET path = EXCLUDED.path` handles the race deterministically.
- Scan canceled via Epic 7 Story 7.12 — the in-progress walk stops at
  the next file boundary; partial progress is preserved (rows already
  inserted remain).
