# Story 24.8 — Identity stability across operations

`content_hash` is the system-wide identity. Every flow that touches a
file must preserve identity correctly.

## Acceptance criteria

- AC1. `content_hash = BLAKE3(first 4 MiB || last 4 MiB || u64_le(size))`
  with documented tie-breaker for files smaller than 8 MiB (the
  whole file is hashed once).
- AC2. Identity is computed once on first scan and stored on the
  `videos` row; subsequent scans reuse the stored hash if `(path,
  size, mtime)` is unchanged.
- AC3. Move / rename within a tracked root preserves the hash and
  updates the path; copy creates a new path → same hash → already-
  ready row served immediately (no re-process). This semantics
  presumes the chosen `videos.content_hash` uniqueness scope (per
  the architecture-§8.1 / Epic 1.5 resolution) is global; per-
  library uniqueness would change this AC and is tracked in the
  cross-doc review.
- AC4. Identity regression suite covers small files, sparse files,
  files exactly at the boundary, and files modified-in-place
  (mtime change but bytes equal).

## Test cases

- TC1. Move stability: rename 1,000 random files; assert no
  re-process is enqueued and all `content_hash` rows are unchanged.
- TC2. Copy stability: copy 100 files to a new location; the new
  rows reuse existing transcripts and indexes.
- TC3. Modify-in-place: edit a single byte in a video; the new
  `content_hash` differs; a re-process is enqueued; old-hash row
  is preserved (history) until GC.

## Edge cases

- EC1. File exactly 8 MiB — `first 4 MiB || last 4 MiB` overlap is
  the whole file; documented and tested as identical to "hash the
  whole file once."
- EC2. File smaller than 4 MiB — same handling; whole-file hash.
- EC3. Sparse file with hole at end — `last 4 MiB` includes the
  hole bytes; consistent with how the OS reports them; documented.
