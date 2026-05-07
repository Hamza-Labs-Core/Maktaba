# Story 6.7 — Concurrency model & per-host caps

## Description

Workers must declare what they can run, and the host must not be
oversubscribed.

## Acceptance criteria

- A worker process loads its concurrency map from
  `pipeline.toml [workers].concurrency` (architecture §11.4); defaults
  match §7.4 (`scan=4`, `probe=4`, `extract=2`, `transcribe=1`,
  `subtitle_gen=2`, `index=4`, `thumbnail=2`). The `subtitle_gen`
  default is added to align with the canonical stage enum
  ([Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md)).
- Each stage has an in-process `asyncio.Semaphore` whose size is the
  declared concurrency; a worker's claim loop attempts a semaphore
  `acquire(timeout=0)` before claiming a job for that stage and
  releases after the job reaches a terminal-or-paused state.
- For GPU-bound stages, an additional process-global lock keyed by
  `device_id` (default `"cuda:0"` or `"mlx:0"`) serializes work on the
  same physical device, even across stages (e.g., `transcribe` and
  diarization both lock the GPU).
- A worker's `--stages` flag scopes which stages it claims; running
  multiple workers with disjoint stage sets is the recommended way to
  scale beyond the per-process caps.

## Test cases

- `test_concurrency_cap_respected` — 5 extract jobs queued, cap 2 →
  exactly 2 in `running` at any sample.
- `test_subtitle_gen_default_concurrency` — `subtitle_gen` cap = 2; 3
  jobs queued → 2 run concurrently.
- `test_gpu_lock_serializes_transcribe` — two `transcribe` jobs queued,
  one GPU → second waits in `pending` (or its semaphore is acquired
  but its claim is delayed by the device lock); never two
  simultaneously running on the same device.
- `test_disjoint_stage_workers_scale` — two workers with `--stages
  transcribe` and `--stages index` respectively → both can run
  concurrently with no contention.

## Edge cases

- **Multi-GPU host.** Devices are enumerated at worker startup; the
  GPU lock is per-device; transcribe concurrency = number of devices.
- **A worker process loses access to its GPU mid-job** (driver crash).
  The job fails with a retryable error; the device is marked
  unhealthy for `device_recheck_sec` (default 5 min).
