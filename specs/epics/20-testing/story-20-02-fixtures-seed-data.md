# Story 20.2 — Fixtures and seed data

Tests must run against shared, reproducible fixtures that are small,
royalty-free, and committed to the repo.

## Acceptance criteria

- AC1. `shared/fixtures/samples/` contains:
  - 1 short Arabic lecture (~ 60 s, royalty-free or self-recorded).
  - 1 short English clip (~ 60 s).
  - 1 mixed-language clip.
  - 1 multi-track mkv (2 audio, 2 subtitle tracks).
  - 1 4 K HDR sample (download-on-demand, not committed).
  - Each with a known `content_hash`, expected probe output, and (where
    applicable) expected transcript golden file.
- AC2. Total committed fixture size ≤ 50 MiB; larger samples (4 K HDR)
  are downloaded by `make fixtures` from a documented mirror with
  checksum verification.
- AC3. A `seeded_db` fixture (Postgres dump) populates 1 k videos and
  10 k segments for performance / capacity tests; load time ≤ 5 s.
- AC4. Fixtures carry a `LICENSE` file documenting source and rights
  for each sample.

## Test cases

- TC1. Determinism: the probe stage on each fixture produces an
  identical JSON byte-for-byte across 10 runs.
- TC2. Size guard: `make fixtures-check` fails CI if any committed file
  > 5 MiB (per file) or total > 50 MiB.
- TC3. Re-download: a corrupted 4 K sample (wrong checksum) is
  re-downloaded automatically; persistent failure aborts with a clear
  message.

## Edge cases

- EC1. Sample with no audio track — a transcribe job on it must skip
  cleanly with `state = SKIPPED_NO_AUDIO`.
- EC2. Fixture with a corrupted moov atom — probe must fail with a
  classified error, not a panic.
- EC3. RTL filename with directional characters — must round-trip
  through scan / probe / index / search.
