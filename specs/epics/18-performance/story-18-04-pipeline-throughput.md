# Story 18.4 — Pipeline throughput targets

Establish the throughput floor for transcription, indexing, and
thumbnailing, and assert them per stage.

## Acceptance criteria

- AC1. On the Mac M2 reference profile with `whisper-mlx large-v3`:
  transcription throughput ≥ 4× realtime (i.e., 1 hour of audio in ≤ 15
  minutes) for clean Arabic speech.
- AC2. Indexing (FTS + Chroma upsert) at ≥ 50 segments/s sustained on the
  reference profile.
- AC3. Thumbnail + sprite generation for a 60-minute video completes in
  ≤ 90 s on the reference profile.
- AC4. The pipeline's `pipeline_stage_duration_seconds` histogram exposes
  per-stage p50/p95 and is queried by the perf test harness.

## Test cases

- TC1. Per-stage benchmark: feed each stage a sealed fixture (20 min of
  Arabic audio, a 90 min film, a 5-track mkv) and assert the throughput
  floor.
- TC2. End-to-end: a single 60-minute Arabic lecture goes
  DISCOVERED → READY in ≤ 20 minutes wall-clock with one worker pool.
- TC3. Backpressure: enqueue 10 hours of audio with `concurrency.transcribe
  = 1`; the queue drains in ≤ 2.5 hours and no worker exceeds its
  configured timeout.

## Edge cases

- EC1. Mixed-language audio (Arabic with English code-switching) —
  throughput target is allowed to drop by 20 %; below that fails.
- EC2. Very short clip (< 30 s) — fixed per-job overhead means realtime
  multiple is meaningless; assert wall-clock < 60 s instead.
- EC3. Failing GPU (MLX init error) — falls back to `faster-whisper` CPU
  with a warning and a relaxed budget; test asserts the fallback path
  completes, not the throughput.
