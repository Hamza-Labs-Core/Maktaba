# Story 19.6 — Storage scaling and large library handling

Identity is `content_hash`; renames and moves do not re-process. The
scanner must handle a 30 TB tree in bounded memory.

## Acceptance criteria

- AC1. Cold scan of a 30 TB tree (≈ 50 k files) completes in ≤ 30
  minutes on the reference profile, bounded RSS ≤ 800 MiB peak.
- AC2. `content_hash` is BLAKE3 of the first + last 4 MiB plus file
  size; correctness verified against a known fixture and adversarial
  inputs (zero-byte file, exactly-8-MiB file, sparse file).
- AC3. Rename / move of 10 % of the library triggers no re-transcribe,
  no re-index, no thumbnail regen.
- AC4. The watcher debounces FS events at 2 s; a copy-then-rename
  sequence (atomic mv) emits one job, not two.

## Test cases

- TC1. Cold scan: synthesize 50 k empty-but-uniquely-hashable files;
  assert wall-clock and RSS budgets.
- TC2. Identity stability: rename every file to a new path; verify
  `videos.content_hash` rows are unchanged and no jobs are enqueued.
- TC3. Pathological content: two files with identical first + last 4
  MiB but different middle bytes — content_hash differs because
  size-or-mid sentinel is included; documented and tested.

## Edge cases

- EC1. A still-being-written file: the scanner skips files whose
  `mtime` is < 30 s in the past (configurable) to avoid hashing partial
  uploads.
- EC2. SMB mount latency spike: hash computation has a 60 s per-file
  timeout; the file is requeued.
- EC3. File deleted mid-scan: graceful skip with debug log, no error.
