# Story 19.7 — Concurrency caps and quotas

Per-host CPU/GPU/concurrency caps protect the box from being overrun.
Quotas are observable and tunable at runtime.

## Acceptance criteria

- AC1. The `transcode.max_concurrent` default is computed as
  `max(1, num_cores / 4)` and is the canonical default referenced from
  every other doc. `streaming.toml` example files show the value as a
  commented line (e.g., `# max_concurrent = auto  # default: num_cores / 4`)
  and rely on the auto-derivation unless an explicit override is set.
  Architecture §11.3 must reference this story for the canonical default.
  New sessions above the cap fall back to direct play with a quality cap,
  or queue with a "starting soon" UI hint.
- AC2. Pipeline `concurrency.transcribe` defaults to 1 per GPU device,
  enforced by an advisory lock; CPU-bound stages use a host-wide
  semaphore.
- AC3. Per-library budget cap (`max_usd_per_month`) for paid STT
  backends is enforced at job-claim time; over-budget jobs return to
  `pending` with `not_before = next month` (per architecture §10.4).
- AC4. All caps are visible in `/api/system/health` and exported as
  metrics.

## Test cases

- TC1. Transcode cap: open `max_concurrent + 2` transcoded sessions; the
  last 2 either downgrade to direct play or receive `503` with `Retry-
  After`.
- TC2. GPU lock: enqueue 4 transcribe jobs with 1 GPU; queue depth
  observed to be 3, throughput unchanged.
- TC3. Budget cap: simulate USD usage at 95 % cap; the next API-backed
  transcribe job is held; an in-progress job is not preempted.
- TC4. Auto default: on a 16-core host, `transcode.max_concurrent`
  reports as `4` in `/api/system/health` without a config override; on
  a 4-core host it reports `1` (floor).

## Edge cases

- EC1. Cap reduced at runtime below current concurrency — running jobs
  finish; new claims respect the new cap immediately.
- EC2. Hot reload of the budget cap mid-month — the new cap is honored
  without restart.
- EC3. Free-tier STT (local Whisper) — no budget cap applies; tested
  that the cap path is bypassed for `backend.type = local`.
