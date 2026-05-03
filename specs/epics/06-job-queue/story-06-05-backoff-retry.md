# Story 6.5 — Backoff and retry

## Description

Transient failures should retry; permanent ones should stop wasting CPU.

## Acceptance criteria

- A failed job whose `attempts < max_attempts` is **not** failed
  permanently. The worker sets `state = 'pending'`, `not_before =
  now() + backoff(attempts)`, and writes the failure to `error` as
  structured JSON `{kind, message, traceback?, retryable: true}`.
- Backoff is `min(60 × 2^(attempts-1), 3600) ± 25%` jitter — i.e.,
  60 s, 120 s, 240 s, …, capped at 1 h.
- A failed job whose `attempts >= max_attempts` becomes `failed`
  (terminal). The error is preserved.
- A non-retryable error (signal: `error.retryable = false` from the
  stage) goes straight to `failed`, irrespective of attempts.
- `POST /api/jobs/{id}/retry` resets a `failed` job: `state = 'pending'`,
  `attempts = 0`, `not_before = NULL`, `error = NULL`.

## Test cases

- `test_retry_with_backoff` — first attempt fails → state `pending`,
  `not_before` ≈ `now() + 60s`.
- `test_max_attempts_terminal_fail` — three consecutive failures →
  fourth state is `failed`, no further retries.
- `test_non_retryable_skips_retries` — stage raises with
  `retryable=False` on first attempt → job state `failed` immediately.
- `test_retry_endpoint_resets_state` — failed job; call retry → row's
  attempts back to 0, state pending.

## Edge cases

- **`max_attempts = 1`** (no retries).  Behaves identically to a
  non-retryable first failure.
- **A retry's `not_before` lands during a configured maintenance
  window.** No special handling; the row remains pending and is
  claimed when the window passes.
