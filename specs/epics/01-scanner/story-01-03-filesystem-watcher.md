# Story 1.3 — Watch for live filesystem changes

## Description

After the initial scan, the user dropping a file into the library should
become a `videos` row within seconds, without a full rewalk. This story
owns the live filesystem watcher: debouncing partial writes, treating
renames as path updates, and soft-deleting on disappearance.

## Acceptance criteria

- **Given** a library with `watch = true` (default),
  **when** the user copies `lecture.mkv` into a watched root,
  **then** within `2 × debounce_sec + 1` seconds (default debounce 2 s)
  there is exactly one new `videos` row and one `processing_jobs(probe)`
  row.
- **Given** an in-progress copy that has not finished writing (`mtime`
  still advancing),
  **when** the watcher receives an event,
  **then** the file is **not** ingested until its size has been stable
  for one debounce interval.
- **Given** a file rename within the library,
  **when** the watcher receives the move event,
  **then** the matching `videos` row's `path` is updated; no new row is
  created and no pipeline stage re-runs.
- **Given** a file deleted from disk,
  **when** the watcher receives the delete event,
  **then** the `videos` row is **soft-deleted** by transitioning to the
  `MISSING` state (Story 1.6); derived data (transcripts, index entries)
  is not destroyed by transient unmounts.

## Test cases

- `test_watcher_picks_up_new_file` — write a fixture into the watched
  root → row appears within 5 s.
- `test_watcher_debounces_partial_writes` — open a file, write 1 MiB
  every 200 ms for 5 s, then close → exactly one ingestion event,
  triggered after the final write settles.
- `test_watcher_handles_rename` — rename a file on disk → the same
  `videos.id` is retained, only `path` updates.
- `test_watcher_handles_delete` — delete the file → row state becomes
  `MISSING`; transcript rows are not deleted.
- `test_watcher_recovers_from_event_storm` — copy 10,000 files in a
  burst → all are eventually ingested with no exceptions; backpressure
  prevents OOM (the queue between watcher and scanner is bounded).

## Edge cases

- **Network filesystems (NFS, SMB) without inotify fidelity.** The watcher
  falls back to a periodic re-walk (default every 6 h, configurable per
  library); this is on by default for any root whose `statvfs` reports
  a non-local fstype.
- **Atomic mv from outside the watched root.** Generates a single
  `created` event; treated like a fresh file unless its hash matches an
  existing `MISSING` row, in which case it's a rediscovery
  (`MISSING → DISCOVERED`, see Story 1.6).
- **`.maktaba/` sidecar directories under the root.** Always ignored by
  both the initial walk and the watcher.
- **`*.part`, `*.crdownload`, `*.tmp` files.** Ignored by extension; the
  watcher waits for the rename to a final extension.
- **Time-of-check to time-of-use.** The hash is computed only after the
  file size has been stable for the debounce interval, eliminating the
  race where we hash a partially-written file.
