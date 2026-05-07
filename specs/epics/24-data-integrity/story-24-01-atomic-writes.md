# Story 24.1 — Atomic writes for sidecar artifacts

Every generated artifact lives next to the source file (`.maktaba/`) and
must never appear on disk in a half-written state.

## Acceptance criteria

- AC1. Subtitle (`.srt`, `.vtt`), segment JSON, thumbnail, sprite, and
  poster outputs are written to a temp path under the same
  filesystem and atomically renamed into the final location only on
  successful completion.
- AC2. The atomic-rename invariant holds across crash points: a kill
  -9 mid-write leaves no partial output and a stale temp file the
  reaper sweeps within 24 h.
- AC3. Atomic-write helpers are centralized in a single utility
  (`media.atomic_write`) used by every generator; bypassing it fails
  a CI lint.
- AC4. On filesystems that don't support `rename(2)` atomicity (some
  network shares), the writer falls back to a `(write, fsync,
  rename, fsync_dir)` sequence with a documented warning.

## Test cases

- TC1. Crash mid-write: kill the worker mid-subtitle write; on
  restart, the final output is missing or complete, never partial.
- TC2. Rename atomicity: race a write against a concurrent reader;
  the reader sees old content or new content, never partial bytes.
- TC3. Sweep: a stale temp older than 24 h is removed by the reaper;
  no error if it was already cleaned up.

## Edge cases

- EC1. Out-of-space mid-write — fail the write, leave no partial
  output, error reported with `category=disk_full`.
- EC2. Network-share rename non-atomic on Windows SMB — documented
  fallback path and per-target tested.
- EC3. Source file deleted after temp write but before rename — the
  rename succeeds, leaving an orphan sidecar; the next scan
  reconciles by removing orphans.
