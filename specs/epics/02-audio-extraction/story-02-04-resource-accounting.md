# Story 2.4 — Audio resource accounting

## Description

Extraction is disk-bound and competes with streaming. The pipeline must
not flood the box with parallel ffmpegs.

## Acceptance criteria

- **Given** the default config `concurrency.extract = 2`,
  **when** more than two `extract` jobs are eligible,
  **then** at most two run simultaneously per worker process; the rest
  remain in `pending`.
- **Given** a high-priority user-initiated extract (priority 50) and
  saturated extract slots,
  **when** the user-initiated job becomes pending,
  **then** the next slot to free runs the priority-50 job first
  (priority is the primary order in the claim loop, see
  [Epic 6 Story 6.2](../06-job-queue/story-06-02-claim-loop.md)).
- **Given** the streaming service is currently transcoding,
  **when** the worker checks resource pressure (optional, off by default
  in v1),
  **then** if `cpu.load_avg_5m > N × cores`, the next claim is delayed
  by `not_before = now() + 30s`. (Toggled by
  `pipeline.cpu_throttle_enabled`.)

## Test cases

- `test_concurrency_cap_enforced` — enqueue 5 extract jobs with cap 2
  → at most 2 running at any instant; total wall time ≈ 3 batches.
- `test_priority_overrides_fifo` — enqueue 3 jobs at priority 100, then
  one at 50 → the priority-50 runs as soon as a slot frees.
- `test_cpu_throttle_delays_claim` — simulated load avg above
  threshold, throttling on → next claim's `not_before` is bumped.

## Edge cases

- **A worker dies holding an extract slot.** The reaper
  ([Epic 6 Story 6.6](../06-job-queue/story-06-06-reaper.md)) flips the
  job to `paused`; the freed slot is automatically reusable.
- **A library spans multiple disks.** The cap is per-process, not
  per-disk; users with this topology can scale by running multiple
  worker processes (architecture §10.3 horizontal scale-out).
