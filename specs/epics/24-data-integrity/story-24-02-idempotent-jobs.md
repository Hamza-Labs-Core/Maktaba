# Story 24.2 — Idempotent and resumable jobs

Every pipeline stage can be re-run safely. A crash during transcription
leaves no partial subtitle file; a resume picks up exactly where it
left off.

## Acceptance criteria

- AC1. Each stage's job has a stable idempotency key:
  `(content_hash, stage, backend, model, config_hash)`. Re-claiming
  a job with the same key skips work that's already complete.
- AC2. Transcription commits each STT segment in its own DB
  transaction along with `processing_jobs.last_segment_end_sec` and
  a heartbeat (architecture §7.6). Resume reads
  `last_segment_end_sec` and feeds it to the STT engine as start
  offset.
- AC3. Sidecar outputs are regenerated from DB state, not from
  whatever was on disk; the on-disk subtitle is a *projection* and
  can be deleted and rebuilt.
- AC4. Bulk re-process commands accept a `--from-stage` to start at
  any point in the DAG; downstream stages re-run; upstream is
  unchanged.

## Test cases

- TC1. Resume from segment N: kill mid-transcribe at minute 30 of a
  60-min clip; resume from same job; total wall-clock is ~30
  minutes, output matches a clean-run reference within tolerance.
- TC2. Idempotent claim: enqueue the same `(content_hash, stage)`
  twice; only one job runs; the other returns the existing result.
- TC3. Rebuild sidecars: delete `.maktaba/` directory; trigger
  `maktaba-pipeline reprocess --from-stage subtitle_gen`;
  artifacts return.

## Edge cases

- EC1. STT engine non-determinism — segment boundaries may shift on
  resume; the test asserts segment text similarity ≥ 95 %, not
  byte-equality.
- EC2. Backend changed mid-job (config bumped) — the resume
  detects the `config_hash` mismatch and re-runs from start; no
  silent splicing across backends.
- EC3. Crash exactly at the segment-commit boundary — the next
  worker re-runs the segment; the output table's `(job_id,
  segment_idx)` unique constraint deduplicates.
