# Story 1.1 — Bootstrap a library and walk its roots

## Description

A user creates a library and points it at one or more root directories.
The scanner walks each root once and inserts a `videos` row for every
candidate file it finds. This story owns the initial recursive walk; the
live filesystem watcher is Story 1.3.

## Acceptance criteria

- **Given** a library with roots `[/mnt/media/lectures]` and a tree
  containing 1,000 `.mp4`/`.mkv` files,
  **when** the user invokes `POST /api/libraries/{id}/scan` (or
  `maktaba-pipeline scan --library lectures`),
  **then** within one wall-clock pass `videos` contains 1,000 rows linked
  to that library, each with `state = 'discovered'`, a populated
  `content_hash`, `path`, `filename`, `size_bytes`, and `mtime`, and a
  `processing_jobs` row of `(stage='probe', state='pending')` per video.
- **Given** the same scan,
  **when** it runs to completion,
  **then** the API receives `videos.new` `LISTEN/NOTIFY` events such that
  the count of WebSocket fanout messages on `/ws/library/{id}` equals the
  number of inserted rows.
- **Given** the supported-extension list `[.mp4, .mkv, .mov, .webm, .avi,
  .ts, .m4v]`,
  **when** the walker encounters files outside that list,
  **then** they are ignored (no `videos` row, no log noise above DEBUG).

## Test cases

- `test_scan_inserts_row_per_video` — fixture tree with N supported files
  → expect `len(rows) == N`, all in `discovered`.
- `test_scan_ignores_non_video_extensions` — fixture mixes `.txt`, `.jpg`,
  `.mp4` → only the `.mp4` row exists.
- `test_scan_emits_notify_per_insert` — listen on `videos.new`; assert
  one notification per insert.
- `test_scan_enqueues_probe_job` — after scan, every `videos` row has a
  matching `processing_jobs` row of `(video_id, stage='probe',
  state='pending')`.
- `test_scan_creates_no_jobs_when_library_disabled` — library with
  `settings.disabled = true` is walked but produces no jobs.

## Edge cases

- **Symlink loops.** Use `os.walk(followlinks=False)` by default; libraries
  may opt-in to `follow_symlinks = true`, in which case a `set` of
  `(st_dev, st_ino)` rejects revisits.
- **Permission-denied directories.** Logged once at WARN with the path,
  then skipped; the scan does not abort.
- **Zero-byte files.** Skipped (no hash possible) and logged at DEBUG.
- **Files smaller than 8 MiB** (less than 4 MiB head + 4 MiB tail used by
  the hash). The hash falls back to "entire file"; correctness preserved.
- **Library with zero roots.** The scan completes immediately with a
  WARN log; no rows touched.
