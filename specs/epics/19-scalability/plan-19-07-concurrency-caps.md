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
func (t *Transcode) ReloadFromConfig(target int64) {
    cur := t.cap.Load()
    if target == cur { return }
    // Build a new semaphore at the new size; in-flight finish on old one.
    // Simpler: reissue tickets via a switchable container.
    t.cap.Store(target)
    if target > cur {
        for i := int64(0); i < target-cur; i++ { t.sem.Release(1) }
    } else {
        // Don't forcibly preempt; simply stop admitting once below cap.
        // Acquire path always TryAcquire; reduced cap takes effect at next free ticket.
        for i := int64(0); i < cur-target; i++ {
            // best-effort drain by acquiring tickets we won't return
            go func() { _ = t.sem.Acquire(context.Background(), 1) }()
        }
    }
}
```

EC1 mapping: running jobs finish; new claims see new cap immediately.

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
class BudgetExceeded(Exception): pass

def claim_with_budget(conn, job: Job) -> Job | None:
    if job.backend == "local":
        return job                                           # EC3 bypass
    cost = estimate_cost(job)                                # USD
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
        if row and row[1] + cost > row[0]:
            cur.execute("""
                UPDATE processing_jobs
                   SET state='pending',
                       scheduled_at = date_trunc('month', now()) + interval '1 month'
                 WHERE id = %s
            """, (job.id,))
            raise BudgetExceeded(job.id)
        cur.execute("UPDATE library_budgets SET used_usd = used_usd + %s WHERE library_id = %s",
                    (cost, job.library_id))
    return job
```

In-progress jobs are not preempted; `claim_with_budget` only blocks _new_ claims.

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
    cpu_semaphore_size: max(1, num_cores - 2)
```

## 10. Metrics

| Metric | Type | Notes |
|---|---|---|
| `transcode_cap` | gauge | current cap |
| `transcode_in_use` | gauge | |
| `transcode_overflow_total{action="downgrade","retry"}` | counter | |
| `pipeline_budget_blocked_total` | counter | |
| `library_budget_used_usd` | gauge | per library |

## 11. Dependencies

- Story 19.4 (GPU advisory lock).
- Story 21.6 (audit log emits cap & budget changes).
- Architecture §11.3 (canonical default reference).
- Story 21.4 (`/api/system/health`).
