# Story 1.2 — Content-addressable identity (BLAKE3)

## Description

Every file gets a stable identity that is independent of name and path so
that renaming or moving a file does not retrigger the entire pipeline.

> **Uniqueness scope (resolves REVIEW §1.1.a).** `content_hash` is unique
> *per library*, not globally. The schema constraint is
> `UNIQUE (library_id, content_hash)`. Within a single library, two files
> with identical bytes share one `videos` row; across libraries, each
> library gets its own row even when the bytes are identical. The
> trade-off — duplicate transcription work for cross-library duplicates
> — is accepted because per-library uniqueness keeps "delete a library"
> semantics clean (no cross-library data orphaning) and avoids a global
> join on the search/serving path. See [Story 1.5](story-01-05-schema-decisions.md)
> for the migration that lands this constraint.

## Acceptance criteria

- **Given** a file `F`,
  **when** the scanner hashes it,
  **then** the produced `content_hash` is the lowercase hex of
  `BLAKE3(first_4_MiB || last_4_MiB || size_le_u64)`, where the size is
  appended as a little-endian unsigned 64-bit integer.
- **Given** two files with identical bytes (and therefore identical
  hashes) **in the same library**,
  **when** both are scanned,
  **then** only the first creates a row; the second logs at INFO
  (`duplicate_content_hash`) and is associated with the existing row via
  an `additional_paths` JSON list on `videos.metadata`.
- **Given** two files with identical bytes **in different libraries**,
  **when** both are scanned,
  **then** each library gets its own `videos` row keyed on
  `(library_id, content_hash)`. No cross-library de-duplication is
  attempted; downstream stages process both rows independently.
- **Given** a 30 GB file,
  **when** it is hashed,
  **then** at most 8 MiB of its bytes are read off disk for hashing, and
  the wall-clock cost is bounded by two seeks plus 8 MiB of sequential
  read.

## Test cases

- `test_hash_is_deterministic` — hash a fixture twice → identical.
- `test_hash_handles_small_file` — file < 8 MiB → hash is full-content
  BLAKE3, not head+tail.
- `test_hash_changes_on_size_change` — append a byte to a fixture →
  hash differs (size is part of the input).
- `test_hash_invariant_under_path_change` — move the fixture; rerun
  scanner → same hash; the existing row is reused, no new insert.
- `test_hash_io_budget` — patch the file with 30 GB sparse layout; assert
  `read()` invocations consume ≤ 8 MiB total.
- `test_hash_collision_within_library_logs_and_links` — two distinct paths
  in the **same library** with byte-for-byte identical content → one row,
  second path appears in `metadata.additional_paths`.
- `test_hash_collision_across_libraries_creates_two_rows` — same bytes
  ingested into two libraries → exactly two `videos` rows, each with the
  same `content_hash` but distinct `library_id`; the unique constraint
  `(library_id, content_hash)` allows this.

## Edge cases

- **Identical file copied to two libraries.** Two rows; each library's
  pipeline runs independently. Documented duplicate work; not an error.
- **Sparse files / holes.** BLAKE3 reads through holes as zeros — accepted;
  the size suffix prevents two sparse files of different sizes from
  colliding.
- **Network filesystem reports wrong size.** Hash is recomputed on the
  next scan if `size_bytes != stat.st_size`; the row is updated, not
  duplicated.
