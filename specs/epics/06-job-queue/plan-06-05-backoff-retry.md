# Implementation Plan — Story 6.5 Backoff and Retry

> Companion to [story-06-05-backoff-retry.md](story-06-05-backoff-retry.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language | Python (Pipeline) for the `mark_failed_or_retry` transition; Go (API) for the `POST /api/jobs/{id}/retry` reset endpoint. |
| Files (Python) | `pipeline/src/maktaba_pipeline/db/jobs_state.py` — adds `mark_failed_or_retry(job_id, *, error, retryable)`; `pipeline/src/maktaba_pipeline/pipeline/backoff.py` — pure backoff math. |
| Files (Go) | `api/internal/jobs/retry.go` (handler), `shared/db/queries/jobs_retry.sql`. |
| Schema dependency | Story 6.1's `processing_jobs` (uses `attempts`, `max_attempts`, `not_before`, `error`). |
| Out of scope | The classification of which exceptions are retryable vs not — that's owned by each stage handler (`StageError(retryable=...)` raised by Epic 1-5). This story owns the queue-side response. |

## 1. Architecture diagram

```
                ┌────────────────────────────────────┐
                │ Stage handler raises StageError or │
                │ returns FailureResult               │
                └──────────────┬─────────────────────┘
                               │ {kind, message, traceback?, retryable}
                               ▼
            ┌──────────────────────────────────────────────┐
            │ mark_failed_or_retry(job_id, error, retryable)│
            │                                              │
            │   1. Read row's attempts / max_attempts       │
            │      (the just-incremented count from claim)  │
            │                                              │
            │   2. Decide:                                  │
            │      retryable && attempts < max_attempts     │
            │        → state='pending',                     │
            │          not_before = now() + backoff(att),   │
            │          claimed_by = NULL                    │
            │        → outcome='retry'                      │
            │                                              │
            │      else                                     │
            │        → state='failed',                      │
            │          finished_at = now()                  │
            │        → outcome='failed'                     │
            │                                              │
            │   3. Write structured error JSON              │
            │   4. INFO log + counter increment             │
            └──────────────────────────────────────────────┘

           ┌──────────────────────────────────────────────┐
           │ Backoff curve:                                │
           │   base   = 60 s                               │
           │   jitter = ±25%                               │
           │   delay  = min(base * 2^(attempts - 1), 3600) │
           │           * uniform(0.75, 1.25)               │
           │                                              │
           │   1st retry  → ~60 s   (45-75)                │
           │   2nd        → ~120 s  (90-150)               │
           │   3rd        → ~240 s  (180-300)              │
           │   4th        → ~480 s  (360-600)              │
           │   ...                                         │
           │   capped at  → ~3600 s (2700-4500)            │
           └──────────────────────────────────────────────┘

           ┌──────────────────────────────────────────────┐
           │ POST /api/jobs/{id}/retry                     │
           │   UPDATE … SET state='pending',               │
           │                attempts=0,                    │
           │                not_before=NULL,               │
           │                error=NULL                     │
           │            WHERE state='failed'                │
           │   → fires NOTIFY jobs.new (via Story 6.1's    │
           │     trigger) so claim loops wake immediately  │
           └──────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/backoff.py` | Pure `compute_backoff(attempts) -> seconds`; deterministic if `random_seed` injected. |
| `pipeline/tests/pipeline/test_backoff.py` | Property tests for the curve and cap. |
| `pipeline/src/maktaba_pipeline/db/jobs_state.py` | Adds `mark_failed_or_retry`, alongside `mark_done`, `mark_paused`, `mark_cancelled`. |
| `pipeline/tests/db/test_jobs_state_failure.py` | Tests for the retry/fail decision and SQL UPDATE. |
| `api/internal/jobs/retry.go` | `POST /api/jobs/{id}/retry` handler. |
| `api/internal/jobs/retry_test.go` | Handler test. |
| `shared/db/queries/jobs_retry.sql` | sqlc input for `RetryFailedJob`. |

### 2.2 Type definitions

```python
# pipeline/src/maktaba_pipeline/pipeline/backoff.py
from __future__ import annotations
import random


BASE_SEC = 60.0
CAP_SEC = 3600.0
JITTER_FRAC = 0.25


def compute_backoff(
    attempts: int,
    *,
    base: float = BASE_SEC,
    cap: float = CAP_SEC,
    jitter: float = JITTER_FRAC,
    rng: random.Random | None = None,
) -> float:
    """Backoff seconds for the given attempt count (1-indexed: 1st failure → 60s).

    delay = min(base * 2 ** (attempts - 1), cap) * uniform(1-jitter, 1+jitter)

    `attempts` here is the counter AFTER the failed attempt (the value the
    claim incremented). For the first failure attempts == 1 → ~60 s.
    """
    if attempts < 1:
        raise ValueError("attempts must be >= 1")
    raw = min(base * (2 ** (attempts - 1)), cap)
    r = rng or random
    factor = 1.0 + (r.random() * 2 - 1) * jitter
    return raw * factor
```

```python
# pipeline/src/maktaba_pipeline/db/jobs_state.py
from dataclasses import dataclass


@dataclass(frozen=True, slots=True)
class StageError:
    kind: str               # "transient_io", "stt_decode", "ffmpeg", "oom", ...
    message: str
    traceback: str | None = None
    retryable: bool = True  # Stage's judgement; OOM should be False, network blip True


@dataclass(frozen=True, slots=True)
class FailureOutcome:
    state: str              # 'pending' (retry queued) or 'failed' (terminal)
    not_before: datetime | None  # set when state='pending'


async def mark_failed_or_retry(
    db,
    *,
    job_id: int,
    error: StageError,
    rng: random.Random | None = None,
) -> FailureOutcome:
    """Decide retry vs terminal fail, write the row, return the outcome.

    Single SQL statement that uses a CTE to read attempts/max_attempts and
    then UPDATEs the row in the same round-trip.
    """
    ...
```

## 3. SQL — failure transition

`pipeline/src/maktaba_pipeline/db/jobs_state.py` (the failure UPDATE):

```python
import json
from datetime import datetime
from .jobs import JobState

_FAIL_OR_RETRY_SQL_PG = """
WITH cur AS (
    SELECT id, attempts, max_attempts FROM processing_jobs
     WHERE id = $1
       AND state IN ('claimed', 'running', 'resuming')
)
UPDATE processing_jobs pj
   SET state         = CASE
                         WHEN $3::bool AND cur.attempts < cur.max_attempts
                           THEN 'pending'
                         ELSE 'failed'
                       END,
       not_before    = CASE
                         WHEN $3::bool AND cur.attempts < cur.max_attempts
                           THEN now() + ($4::float || ' seconds')::interval
                         ELSE NULL
                       END,
       claimed_by    = NULL,
       error         = $2::text,
       finished_at   = CASE
                         WHEN $3::bool AND cur.attempts < cur.max_attempts
                           THEN NULL
                         ELSE now()
                       END
  FROM cur
 WHERE pj.id = cur.id
RETURNING pj.state, pj.not_before
"""


async def mark_failed_or_retry(db, *, job_id, error, rng=None):
    row_check = await db.fetchrow(
        "SELECT attempts, max_attempts FROM processing_jobs WHERE id = $1",
        job_id,
    )
    if row_check is None:
        # The row was reaped or cancelled while we were running. No-op.
        return FailureOutcome(state="cancelled", not_before=None)

    err_json = json.dumps({
        "kind":      error.kind,
        "message":   error.message,
        "traceback": error.traceback,
        "retryable": error.retryable,
    })

    # Compute backoff out of band so the SQL stays static; the value is only
    # used if the CASE picks the retry branch.
    backoff_sec = compute_backoff(row_check["attempts"], rng=rng) \
                  if error.retryable and row_check["attempts"] < row_check["max_attempts"] \
                  else 0.0

    out = await db.fetchrow(
        _FAIL_OR_RETRY_SQL_PG, job_id, err_json,
        error.retryable, backoff_sec,
    )
    if out is None:
        return FailureOutcome(state="cancelled", not_before=None)
    return FailureOutcome(state=out["state"], not_before=out["not_before"])
```

The SQLite path uses `julianday('now') + $sec/86400.0` instead of the
interval cast; otherwise identical structure.

### 3.1 Retry-endpoint SQL

`shared/db/queries/jobs_retry.sql`:

```sql
-- name: RetryFailedJob :one
-- Reset a failed job. Allowed only when state='failed' so the operator
-- can't accidentally retry a running job. The notify trigger from
-- Story 6.1 fires `jobs.new` because we essentially reincarnate the row
-- as a fresh pending one.
UPDATE processing_jobs
   SET state         = 'pending',
       attempts      = 0,
       not_before    = NULL,
       error         = NULL,
       finished_at   = NULL,
       claimed_by    = NULL,
       claimed_at    = NULL
 WHERE id = $1
   AND state = 'failed'
RETURNING id, state;
```

We do not fire `jobs.new` from this path — the trigger is on INSERT,
not UPDATE. We add an explicit `pg_notify` inside the handler to wake
claim loops immediately:

```go
// api/internal/jobs/retry.go (excerpt)
row, err := qtx.RetryFailedJob(ctx, id)
// ... handle ErrNoRows → 409 ...
if err := pgNotify(ctx, tx, "jobs.new", map[string]any{
    "id": id, "video_id": row.VideoID, "stage": row.Stage,
    "priority": row.Priority,
}); err != nil { /* ... */ }
```

The reused `pgNotify` helper from Story 6.4's plan keeps this DRY.

## 4. Backoff curve — concrete numbers

| `attempts` after claim | Raw delay (s) | Jittered range (s) |
|---|---|---|
| 1 | 60 | 45–75 |
| 2 | 120 | 90–150 |
| 3 | 240 | 180–300 |
| 4 | 480 | 360–600 |
| 5 | 960 | 720–1200 |
| 6 | 1920 | 1440–2400 |
| 7 | 3600 (capped) | 2700–4500 |
| 8+ | 3600 (capped) | 2700–4500 |

`max_attempts` defaults to 3, so in practice the curve is exercised
at most twice (1st retry ~60 s, 2nd retry ~120 s, 3rd attempt is
terminal). The cap exists because operators can override
`max_attempts` in `pipeline.toml`.

## 5. Test plan

### 5.1 Backoff unit tests (`pipeline/tests/pipeline/test_backoff.py`)

| Test | What it pins |
|---|---|
| `test_backoff_first_attempt_is_about_60s` | `compute_backoff(1)` ∈ [45, 75]. |
| `test_backoff_doubles_until_cap` | seq `[1..10]` produces increasing values until 3600 ± jitter, then plateau. |
| `test_backoff_jitter_within_25pct` | 1000 samples at attempts=3; min ≥ 0.75×raw, max ≤ 1.25×raw, mean within 1% of raw. |
| `test_backoff_deterministic_with_seeded_rng` | Same seed → same value (reproducible tests). |
| `test_backoff_invalid_attempts_raises` | `compute_backoff(0)` raises; `compute_backoff(-1)` raises. |
| `test_backoff_cap_respected` | `compute_backoff(20)` ∈ [2700, 4500]. |

### 5.2 Failure transition tests (`pipeline/tests/db/test_jobs_state_failure.py`)

| Test | What it pins |
|---|---|
| `test_first_failure_retries` | Insert running row attempts=1, max_attempts=3; mark_failed_or_retry(retryable=True) → state='pending', not_before ≈ now+60s, error JSON populated. |
| `test_max_attempts_terminal_fail` | Row attempts=3, max_attempts=3; mark_failed_or_retry(retryable=True) → state='failed', not_before=NULL, finished_at=now. |
| `test_non_retryable_skips_retries` | Row attempts=1, max_attempts=5; mark_failed_or_retry(retryable=False) → state='failed' immediately. |
| `test_retryable_after_max_still_terminal` | Row attempts=4, max_attempts=3 (over by one); mark_failed_or_retry(retryable=True) → state='failed'. |
| `test_error_json_shape` | After failure, `error` column contains `{kind, message, traceback, retryable}` deserializable JSON. |
| `test_retry_clears_claimed_by` | Row had `claimed_by='worker-1'`; after retry transition, `claimed_by IS NULL`. |
| `test_retry_endpoint_resets_state` | Insert failed row; POST /retry → row state='pending', attempts=0, not_before=NULL, error=NULL; jobs.new notify fires. |
| `test_retry_endpoint_409_on_non_failed` | POST /retry on a `running` row → 409 with body explaining the state mismatch. |
| `test_retry_endpoint_404_on_missing` | POST /retry on unknown id → 404. |
| `test_max_attempts_one_no_retries` | Row max_attempts=1, attempts=1; mark_failed_or_retry(retryable=True) → state='failed' (one failure exhausts the budget). |

### 5.3 Property test — backoff under repeated failure

```python
@pytest.mark.asyncio
async def test_repeated_failures_climb_then_terminal(db, video):
    job = await enqueue_and_claim(db, video, max_attempts=4)

    delays = []
    for _ in range(4):
        outcome = await mark_failed_or_retry(
            db, job_id=job.id,
            error=StageError(kind="net", message="boom", retryable=True),
        )
        if outcome.state == "failed":
            break
        # Simulate the not_before passing and a re-claim.
        delays.append((outcome.not_before - datetime.utcnow()).total_seconds())
        await db.execute(
            "UPDATE processing_jobs SET not_before = now() WHERE id = $1",
            job.id,
        )
        job = await claim_one(db, worker_id="t",
                              supported_stages=(Stage.PROBE,))

    assert outcome.state == "failed"
    # Delays roughly double each step (within jitter).
    assert delays[1] >= delays[0] * 1.4
    assert delays[2] >= delays[1] * 1.4
    assert delays[3] <= 3600 * 1.25
```

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| `max_attempts = 1` | First failure → terminal. The retry/fail decision computes `attempts < max_attempts` as `1 < 1 = false` → fail. | `test_max_attempts_one_no_retries` |
| `max_attempts = 1`, retryable=False | Same as above; the retryable check short-circuits to `failed` regardless. | Implicit |
| Retry's `not_before` lands in a maintenance window | Out of scope; the row stays pending with a future `not_before`. The claim loop's `not_before <= now()` filter automatically holds it. No special handling needed. | Story 6.2's `test_claim_skips_not_before_in_future` |
| Stage handler raises an exception that doesn't subclass `StageError` | The runner catches `Exception`, classifies as `StageError(kind="unhandled", retryable=True)` with the exception type as `kind` and the traceback in `traceback`. The retry path runs normally. | Test in `test_runner.py`: `test_unhandled_exception_classified_as_retryable` |
| Stage explicitly raises `StageError(retryable=False)` | The retry decision sees `retryable=False` and goes straight to `failed`, regardless of attempts. | `test_non_retryable_skips_retries` |
| Failure happens after force-pause has already flipped state to `paused` | The CTE's `state IN ('claimed','running','resuming')` predicate excludes `paused` → UPDATE no-ops; we return `state='cancelled'` (a synthetic outcome that means "the row is already in a non-failure state, do not interpret this as a hang"). The runner logs at INFO and exits the stage. | `test_failure_after_force_pause_is_noop` |
| Concurrent `POST /retry` and a worker still claiming the failed row | The retry SQL has `state='failed'` predicate; if a separate worker has somehow re-claimed (which it can't — `failed` is excluded from the claim WHERE), the retry no-ops. The handler returns 409. | Cannot happen in practice; defensive predicate. |
| Network blip during the failure UPDATE itself | Connection pool retries the statement once via the standard asyncpg retry helper; if both attempts fail, the row stays in its current state and the reaper (Story 6.6) will sweep it after `stale_claim_sec`. | Inherited from the pool layer. |
| Concurrent retry endpoint calls | Idempotent at the SQL level: the second UPDATE's `state='failed'` predicate fails (state is now `pending` after the first call), returns no rows, handler returns 409 with "already retrying". | `test_retry_idempotent_returns_409_on_second` |
| Operator changes `max_attempts` mid-flight | The next failure reads the *current* `max_attempts` value. A row whose `attempts` already exceeds the new cap fails on the next failure; rows under the new cap continue to retry normally. | Documented in `mark_failed_or_retry` docstring. |

## 7. Performance analysis

The CTE-based UPDATE is one round-trip; no extra SELECT + UPDATE roundtrips.
On a 10K-row table with the failure path warm:

| Step | Cost |
|---|---|
| `mark_failed_or_retry` (one CTE UPDATE) | ~0.4 ms |
| Backoff math (Python) | ~5 µs |

The retry endpoint is one indexed UPDATE plus one notify: < 1 ms.

## 8. Dependencies

No new deps. Backoff math is pure-Python; `random.Random` is stdlib.

## 9. Acceptance checklist

**Code**
- [ ] `compute_backoff` matches the published curve (60, 120, 240, …, capped at 3600) ± 25% jitter.
- [ ] `mark_failed_or_retry` is one DB round-trip via a CTE-based UPDATE.
- [ ] `error` column stores structured JSON `{kind, message, traceback, retryable}`.
- [ ] `POST /api/jobs/{id}/retry` resets `state`, `attempts`, `not_before`, `error`, `claimed_by` on a `failed` row.

**Behaviour (story acceptance criteria)**
- [ ] AC: `test_retry_with_backoff` — first failure, `not_before ≈ now + 60s`.
- [ ] AC: `test_max_attempts_terminal_fail` — three failures → fourth state is `failed`, no further retries.
- [ ] AC: `test_non_retryable_skips_retries` — `retryable=False` first failure → `failed`.
- [ ] AC: `test_retry_endpoint_resets_state` — failed → pending after POST /retry.

**Performance**
- [ ] Failure transition p95 < 1 ms warm.

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.5.
- [ ] `pipeline/observability.py`: counter `maktaba_job_attempts_total{stage, outcome}` increments on each transition (used by Story 6.9).
