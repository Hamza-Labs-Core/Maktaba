# Story 9.4 — Content-hash dedup

Identity is BLAKE3 over first 4 MiB + last 4 MiB + size (§3.1, §1.5). A
new file whose hash already exists is treated as a copy/move/rename.

**AC-1 — Hash computation.**
- **Given** a file ≥ 8 MiB,
- **When** the hasher runs,
- **Then** the hash is computed over `[0..4MiB) + [size-4MiB..size) +
  size_bytes_le`. Files smaller than 8 MiB are hashed in full. Before
  reading any bytes, the path is canonicalized and asserted to live
  inside one of the registered library roots; off-root paths cause the
  hasher to refuse with `path-out-of-root`.

**AC-2 — Hash uniqueness (global per architecture §8.1).**
- **Given** two files with the same hash,
- **When** both are seen by the scanner,
- **Then** only the first inserts a `videos` row; the second updates
  `path` to the most-recently-seen path (the older path is discarded;
  if both files coexist on disk, the catalog points to one and the
  other is recorded in `audit_log (category='library',
  event='duplicate-detected', payload={path, original_video_id})`).
  This relies on the architecture-level decision to keep
  `videos.content_hash UNIQUE` globally; if the project later adopts
  per-library uniqueness, this AC will need to be revised.

**AC-3 — Performance.**
- **Given** a 30 GiB file on local SSD,
- **When** hashed,
- **Then** the operation completes in under 100 ms (8 MiB read + small
  CPU). Network filesystems must respect a `hash_timeout_sec` (default
  30 s) and skip-with-error if exceeded.

**Test cases:**
- Unit: identical files in different folders produce the same hash.
- Unit: byte-for-byte different files in the [4 MiB..size-4 MiB) range
  produce the *same* hash (this is a known property; the test documents
  it so reviewers don't think it's a bug).
- Integration: hash a 50 GiB file in a CI fixture (synthesized) under
  the time budget.
- Security: an attempt to hash a path outside any registered root is
  rejected with `path-out-of-root`.

**Edge cases:**
- Files exactly 8 MiB — hashed in full (tiny optimization).
- Truncated read on EOF in the last-4-MiB window — the partial bytes are
  hashed; this is consistent for the same file across reads.
- Hash collision — astronomically unlikely with BLAKE3; if observed,
  the catalog preserves the first-seen entry and logs a `hash_collision`
  metric. The user can force a re-process to verify.
