# Implementation Plan — Story 19.7 Concurrency Caps & Quotas

> Companion to [story-19-07-concurrency-caps.md](story-19-07-concurrency-caps.md).
> Per-host CPU/GPU caps; library-budget USD cap; auto-derived defaults; runtime tunable.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Default `transcode.max_concurrent` | `max(1, num_cores / 4)`, computed at boot, surfaced in `/api/system/health`. |
| Pipeline GPU cap | `pg_advisory_xact_lock(host_id, device_id)` — see Story 19.4. |
| CPU semaphore | per-host bounded `asyncio.Semaphore` (Python) / weighted `golang.org/x/sync/semaphore` (Go). |
| Budget cap | `library_budgets(library_id, max_usd_per_month, used_usd_this_month, period_start)` checked at job-claim time. |
| Hot reload | Caps re-read from `system_config` table every 30 s; bypass restart. |

> **Architecture §11.3 doc-edit ownership.** Arch §11.3's sample TOML still
> hardcodes `max_concurrent = 4`; Story 19.7 AC1 says `max(1, num_cores/4)`.
> The doc-edit pointing arch §11.3 at Story 19.7 is **owned by this plan**
> (see §2 below). Plan-22-04 does not own architecture text; it owns the
> migration that introduces `system_config`. If Story 22.4 grows a doc-edit
> backlog, this pointer can move there with explicit hand-off, but until
> then the canonical owner is plan-19-07.

## 1. Project layout

```
streaming/internal/concurrency/
├── transcode_cap.go
├── auto_default.go
├── enforcer.go
└── cap_test.go

pipeline/maktaba_pipeline/concurrency/
├── caps.py
├── budget.py
└── tests/

api/internal/system/
├── health.go                # exposes caps + current usage
└── caps_reload.go           # 30s poller

shared/db/migrations/
└── 00xx_library_budgets.sql

api/internal/admin/
└── caps.go                  # PATCH cap at runtime
```

## 2. Auto default

```go
// streaming/internal/concurrency/auto_default.go
func AutoMaxConcurrent() int {
    n := runtime.NumCPU()
    if n < 4 { return 1 }
    return n / 4
}
```

Surfaced in `/api/system/health`:

```json
{
  "caps": {
    "transcode": {
      "max_concurrent": 4,
      "source": "auto",         // or "config"
      "current_in_use": 2
    },
    "transcribe": { "max_concurrent": 1, "source": "config" },
    "library_budgets": [
      { "library_id": "...", "max_usd_per_month": 50.00, "used_usd": 12.34, "period_resets_at": "2026-06-01T00:00:00Z" }
    ]
  }
}
```

`streaming.toml` example:

```toml
[transcode]
# max_concurrent = auto  # default: num_cores / 4 (Story 19.7)
# uncomment to override
# max_concurrent = 8
```

## 3. Transcode enforcer

```go
// streaming/internal/concurrency/enforcer.go
type Transcode struct {
    sem         *semaphore.Weighted
    cap         atomic.Int64
    inUse       atomic.Int64
}

func New() *Transcode {
    t := &Transcode{cap: atomic.Int64{}}
    t.cap.Store(int64(AutoMaxConcurrent()))
    t.sem = semaphore.NewWeighted(t.cap.Load())
    return t
}

func (t *Transcode) Acquire(ctx context.Context, fallbackDirectPlay bool) (release func(), downgraded bool, err error) {
    if !t.sem.TryAcquire(1) {
        if fallbackDirectPlay { return func(){}, true, nil }
        // Otherwise return 503 with Retry-After
        return nil, false, ErrCapExceeded
    }
    t.inUse.Add(1)
    return func() { t.sem.Release(1); t.inUse.Add(-1) }, false, nil
}
```

OpenSession path:

```go
release, dp, err := t.transcodes.Acquire(ctx, true)
if err != nil {
    w.Header().Set("Retry-After", "30")
    http.Error(w, "transcode_cap", 503); return
}
defer release()
if dp { session.Mode = "direct_play"; session.QualityCap = "720p" }
```

## 4. Hot reload

```go
// streaming/internal/concurrency/reload.go
//
// Hot reload: track each "drain goroutine" so a subsequent policy change can
// cancel its acquire and release the ticket back to the new sem. Without
// cancellation, the goroutine sits forever holding `cur-target` tickets even
// if a later reload raises the cap.

type Transcode struct {
    sem      *semaphore.Weighted
    cap      atomic.Int64
    inUse    atomic.Int64

    mu       sync.Mutex
    drainCtx context.Context           // last reload's context (for cancellation)
    drainCxl context.CancelFunc
}

func (t *Transcode) ReloadFromConfig(target int64) {
    cur := t.cap.Load()
    if target == cur { return }

    t.mu.Lock()
    defer t.mu.Unlock()

    // Cancel any prior drain goroutines — their tickets become moot under the
    // new policy and they must not continue holding capacity.
    if t.drainCxl != nil { t.drainCxl() }
    t.drainCtx, t.drainCxl = context.WithCancel(context.Background())
    ctx := t.drainCtx

    t.cap.Store(target)

    if target > cur {
        for i := int64(0); i < target-cur; i++ { t.sem.Release(1) }
        return
    }

    // Drain by acquiring (target<cur) tickets we won't release until cancelled.
    delta := cur - target
    for i := int64(0); i < delta; i++ {
        go func() {
            // Acquire is cancellable via the captured ctx; if a later reload
            // cancels us, release the ticket back to the pool.
            if err := t.sem.Acquire(ctx, 1); err != nil { return }
            <-ctx.Done()
            t.sem.Release(1)
        }()
    }
}
```

EC1 mapping: running jobs finish; new claims see new cap immediately. If
a later reload changes the cap again, the drain goroutines from the
previous reload are cancelled and release their held tickets.

## 5. Pipeline budget cap

```sql
-- 00xx_library_budgets.sql
CREATE TABLE library_budgets (
    library_id          UUID PRIMARY KEY REFERENCES libraries(id),
    max_usd_per_month   NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    used_usd            NUMERIC(10,2) NOT NULL DEFAULT 0.00,
    period_start        TIMESTAMPTZ NOT NULL DEFAULT date_trunc('month', now())
);
```

```python
# pipeline/maktaba_pipeline/concurrency/budget.py
#
# Two-phase accounting:
#
#   1. claim_with_budget() — at claim time, GATE only. Read the row under
#      `FOR UPDATE`, reset the period if needed, and *check* whether
#      `used_usd + estimate <= max_usd_per_month`. If not, push the job to
#      next month and raise. Do NOT increment `used_usd` yet — the job
#      may retry/fail and we'd double-count on the next claim.
#
#   2. settle_budget()  — at job completion (success or terminal failure
#      that consumed external API calls), increment `used_usd` by the
#      *actual* cost. On retry-without-cost (e.g. heartbeat reap before
#      any API call), settle_budget() is not called. On partial-spend
#      failure, the worker passes the partial cost.
class BudgetExceeded(Exception): pass

def claim_with_budget(conn, job: Job) -> Job | None:
    """GATE only — does not increment used_usd."""
    if job.backend == "local":
        return job                                           # EC3 bypass
    cost_estimate = estimate_cost(job)                       # USD
    with conn.transaction(), conn.cursor() as cur:
        cur.execute("""
            SELECT max_usd_per_month, used_usd, period_start
              FROM library_budgets
             WHERE library_id = %s
             FOR UPDATE
        """, (job.library_id,))
        row = cur.fetchone()
        if row and row[2] < date_trunc_month(now()):
            cur.execute("UPDATE library_budgets SET used_usd=0, period_start=date_trunc('month', now()) WHERE library_id=%s",
                        (job.library_id,))
            row = (row[0], 0, now())
        if row and row[1] + cost_estimate > row[0]:
            cur.execute("""
                UPDATE processing_jobs
                   SET state='pending',
                       not_before = date_trunc('month', now()) + interval '1 month'
                 WHERE id = %s
            """, (job.id,))
            raise BudgetExceeded(job.id)
    # NOTE: no UPDATE library_budgets here — settle_budget() does that on
    # successful (or terminally-charged) completion.
    return job


def settle_budget(conn, job: Job, actual_cost_usd: Decimal) -> None:
    """Increment used_usd at completion. Idempotent: marks job.budget_settled=true."""
    if actual_cost_usd <= 0 or job.backend == "local":
        return
    with conn.transaction(), conn.cursor() as cur:
        # Idempotency: only settle once per job.
        cur.execute("SELECT budget_settled FROM processing_jobs WHERE id=%s FOR UPDATE", (job.id,))
        already = cur.fetchone()
        if already and already[0]:
            return
        cur.execute("UPDATE library_budgets SET used_usd = used_usd + %s WHERE library_id = %s",
                    (actual_cost_usd, job.library_id))
        cur.execute("UPDATE processing_jobs SET budget_settled = true WHERE id=%s", (job.id,))
```

In-progress jobs are not preempted; `claim_with_budget` only blocks _new_
claims. `settle_budget()` is called from the worker's success path (and from
the failure path if external spend was committed). Retries that did not
spend do not double-count because `used_usd` is only ever incremented at
settlement.

## 6. Hot reload for budget

`system_config` is polled every 30 s. The budget table read during claim is the source of truth and reflects edits immediately.

```go
// api/internal/admin/caps.go
func (h *Handler) PatchBudget(w http.ResponseWriter, r *http.Request) {
    var req struct{ MaxUSD decimal.Decimal `json:"max_usd_per_month"` }
    _ = json.NewDecoder(r.Body).Decode(&req)
    libID := chi.URLParam(r, "library_id")
    _, err := h.db.ExecContext(r.Context(),
        `UPDATE library_budgets SET max_usd_per_month = $1 WHERE library_id = $2`,
        req.MaxUSD, libID)
    if err != nil { http.Error(w, err.Error(), 500); return }
    audit.Emit(r.Context(), "budget.patch", auditFields(libID, req.MaxUSD))
    w.WriteHeader(204)
}
```

## 7. Test cases

### TC1 — Transcode cap
Set `max_concurrent=4`. Open 6 transcoded sessions: first 4 succeed, last 2:
- Either downgrade (`session.mode="direct_play"`, `quality_cap="720p"`) when `fallback_direct_play=true`.
- Or `503 Retry-After: 30` when `false`.

### TC2 — GPU lock
4 transcribe jobs, 1 GPU. Worker claims them one at a time (advisory lock serializes). Throughput per job unchanged; queue depth = 3 during run.

### TC3 — Budget cap
Library has `max_usd_per_month=$10`, `used_usd=$9.50`. Job estimate=$1. `claim_with_budget` raises `BudgetExceeded`; job's `scheduled_at` set to next month start. In-progress job from before is unaffected.

### TC4 — Auto default
Force `runtime.NumCPU()=16` (test override). `AutoMaxConcurrent()` returns 4. `/api/system/health` reports `transcode.max_concurrent=4, source=auto`.
Force `runtime.NumCPU()=4`. Returns 1 (floor).

### EC1 — Cap reduce mid-flight
Set `max_concurrent=8`, 8 sessions running. Patch to 2. Running sessions complete normally. New OpenSession requests queue/downgrade until in-flight count ≤ 2.

### EC2 — Hot reload budget
Mid-month, patch `max_usd_per_month` from $50 to $10. Next claim sees new cap; no restart.

### EC3 — Local backend bypass
Job with `backend.type='local'` (Whisper local). `claim_with_budget` returns job immediately; no budget row touched.

## 8. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 cap reduced live | story | Drain via acquire-without-release for the delta; in-flight unaffected. |
| EC2 hot reload budget | story | Single source of truth = DB row. |
| EC3 local STT bypass | story | Explicit early-return in `claim_with_budget`. |
| Cap = 0 | impl | Documented as "all transcoded sessions downgrade or 503". |
| Concurrent cap edits | impl | `FOR UPDATE` on `library_budgets` row; serialised. |

## 9. Configuration

```yaml
streaming:
  transcode:
    max_concurrent: auto
    fallback_direct_play: true
    direct_play_quality_cap: 720p
pipeline:
  concurrency:
    transcribe: 1
    # Plan-introduced default (Story 19.7). Reserves 2 cores for the
    # OS/streaming/API processes so pipeline CPU work doesn't starve the
    # request path. Add to architecture §11 cross-reference table when
    # next syncing arch with this plan.
    cpu_semaphore_size: max(1, num_cores - 2)
```

## 10. Metrics

| Metric | Type | Notes |
|---|---|---|
| `transcode_cap` | gauge | current cap |
| `transcode_in_use` | gauge | |
| `transcode_overflow_total{action="downgrade","retry"}` | counter | |
| `pipeline_budget_blocked_total{library_id}` | counter | label by `library_id` so operators can tell which library tripped its cap. |
| `library_budget_used_usd` | gauge | per library |

## 11. Dependencies

- Story 19.4 (GPU advisory lock).
- Story 21.6 (audit log emits cap & budget changes).
- Architecture §11.3 (canonical default reference).
- Story 21.4 (`/api/system/health`).
