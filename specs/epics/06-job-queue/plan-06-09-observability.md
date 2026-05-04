# Implementation Plan — Story 6.9 Observability Hooks

> Companion to [story-06-09-observability.md](story-06-09-observability.md).
> The story states *what* and *why*; this plan states *how*.
> Logs follow the `structlog` JSON shape from
> [architecture.md §11/§12.5](../../architecture.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language split | Go (API) for the HTTP `/api/queue/stats` and Prometheus `/metrics` endpoints. Python (Pipeline) for the structured logs and the metric-emission helpers (the same metric names appear in both surfaces). |
| Files (Go) | `api/internal/queue/stats.go` (the stats endpoint), `api/internal/queue/metrics.go` (Prometheus collectors), `shared/db/queries/queue_stats.sql` (sqlc input). |
| Files (Python) | `pipeline/src/maktaba_pipeline/observability.py` — `log_event`, counter/histogram helpers using `prometheus_client`. |
| Out of scope | Dashboards (Grafana JSON shipped alongside, not in this story); alert rules; distributed tracing (deferred). |

## 1. Architecture diagram

```
                 ┌───────────────────────────────────────┐
                 │ Pipeline workers                      │
                 │  ─ tick_progress, mark_*, claim ─►    │
                 │     log_event(structlog)              │
                 │     metric.inc / observe               │
                 │  ─ prometheus_client exposes 9101      │
                 └────────────────┬──────────────────────┘
                                  │
                                  │ /metrics (text/plain; Prom format)
                                  │
                 ┌────────────────▼──────────────────────┐
                 │ API service (Go)                      │
                 │                                       │
                 │  GET /api/queue/stats                 │
                 │    ─► one SQL query group:            │
                 │         by_stage_state, eta, p50_rtf  │
                 │    ─► JSON response per story spec    │
                 │                                       │
                 │  GET /metrics  (Prometheus)           │
                 │    ─► gather collectors:              │
                 │       maktaba_jobs_total              │
                 │       maktaba_job_attempts_total      │
                 │       maktaba_job_duration_seconds    │
                 │       maktaba_job_realtime_factor     │
                 │    ─► proxies the Pipeline collectors │
                 │       OR returns its own (DB-backed)  │
                 │       union — see §3.2                │
                 └───────────────────────────────────────┘
```

The metric set is owned in two places (Pipeline emits the per-event
counters; API computes the gauge from a DB query) — both registered
under the same metric name with disjoint label sets so a Prometheus
scrape sees one metric series per `(stage, state)` pair.

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/queue/stats.go` | `GET /api/queue/stats` handler. |
| `api/internal/queue/stats_test.go` | Handler test against fixture jobs. |
| `api/internal/queue/metrics.go` | Prometheus collectors for the API process; uses the prometheus Go client. |
| `api/internal/queue/metrics_test.go` | Scrape-format assertions. |
| `shared/db/queries/queue_stats.sql` | sqlc input for `QueueStatsByStageAndState`, `QueueETATotals`, `QueueRealtimeFactorP50`. |
| `pipeline/src/maktaba_pipeline/observability.py` | `log_event`, `metric_*` helpers, the canonical metric-name constants. |
| `pipeline/tests/observability/test_log_events.py` | Asserts log shape on state transitions. |
| `pipeline/tests/observability/test_metrics.py` | Asserts counter and histogram emission on transitions. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/db/jobs_state.py` | Each `mark_done`, `mark_paused`, `mark_failed_or_retry`, `mark_cancelled` calls `log_event(...)` and bumps the relevant counter. |
| `pipeline/src/maktaba_pipeline/db/jobs_progress.py` | `tick_progress` updates `maktaba_job_realtime_factor` summary. |
| `api/internal/server/router.go` | Wire `/api/queue/stats` and `/metrics`. |
| `pipeline/pyproject.toml` | Add `prometheus-client>=0.20`. |
| `api/go.mod` | Add `github.com/prometheus/client_golang/prometheus`. |

### 2.3 The `/api/queue/stats` JSON contract

```json
{
  "by_stage": {
    "scan":         {"pending": 0, "running": 0, "paused": 0, "failed": 0, "done": 1834},
    "probe":        {"pending": 12, "running": 1, "paused": 0, "failed": 0, "done": 1822},
    "extract":      {"pending": 8, "running": 2, "paused": 1, "failed": 0, "done": 1813},
    "transcribe":   {"pending": 12, "running": 1, "paused": 3, "failed": 0, "done": 184},
    "subtitle_gen": {"pending": 0, "running": 0, "paused": 0, "failed": 0, "done": 184},
    "index":        {"pending": 184, "running": 0, "paused": 0, "failed": 0, "done": 0},
    "thumbnail":    {"pending": 0, "running": 0, "paused": 0, "failed": 0, "done": 184}
  },
  "by_state": {
    "pending": 216, "running": 4, "paused": 4, "failed": 0,
    "done": 6021, "claimed": 0, "resuming": 0, "cancelled": 0
  },
  "eta_total_sec": 31738.4,
  "realtime_factor_p50": 0.31
}
```

The `by_stage` map's keys are exactly the canonical stage list
(7 entries always); the `by_state` map's keys are the 8 states. Empty
counts are returned as `0`, never omitted — UI rendering doesn't need
to handle "key missing means 0."

### 2.4 SQL — the stats queries

`shared/db/queries/queue_stats.sql`:

```sql
-- name: QueueStatsByStageAndState :many
SELECT stage, state, count(*)::bigint AS n
  FROM processing_jobs
 GROUP BY stage, state;

-- name: QueueETATotals :one
SELECT
    COALESCE(SUM(estimated_remaining_sec) FILTER (
        WHERE state IN ('pending', 'running', 'resuming', 'paused')
    ), 0)::float AS eta_total_sec
  FROM processing_jobs;

-- name: QueueRealtimeFactorP50 :one
SELECT percentile_disc(0.5) WITHIN GROUP (
            ORDER BY realtime_factor
        )::float AS p50
  FROM processing_jobs
 WHERE realtime_factor IS NOT NULL
   AND state IN ('running', 'resuming', 'done');

-- name: JobsTotalGaugeRows :many
-- Used by the API's Prometheus collector to emit
-- maktaba_jobs_total{stage,state} as a gauge.
SELECT stage, state, count(*)::bigint AS n
  FROM processing_jobs
 GROUP BY stage, state;
```

`percentile_disc` is exact (no interpolation); for SQLite, the API
falls back to a Python-side computation of the median over the rows.

### 2.5 The Go handler

`api/internal/queue/stats.go`:

```go
package queue

import (
    "context"
    "encoding/json"
    "log/slog"
    "net/http"

    "maktaba/api/internal/db"
)

// Canonical key sets — never trust the DB to produce a row for every (stage,state)
// combination.
var stages = []string{
    "scan", "probe", "extract", "transcribe",
    "subtitle_gen", "index", "thumbnail",
}
var states = []string{
    "pending", "claimed", "running", "resuming",
    "paused", "done", "failed", "cancelled",
}

type StatsHandler struct {
    Q   *db.Queries
    Log *slog.Logger
}

type stageBreakdown struct {
    Pending int64 `json:"pending"`
    Running int64 `json:"running"`
    Paused  int64 `json:"paused"`
    Failed  int64 `json:"failed"`
    Done    int64 `json:"done"`
}

type statsResponse struct {
    ByStage           map[string]stageBreakdown `json:"by_stage"`
    ByState           map[string]int64          `json:"by_state"`
    ETATotalSec       float64                   `json:"eta_total_sec"`
    RealtimeFactorP50 float64                   `json:"realtime_factor_p50"`
}

func (h *StatsHandler) Get(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    rows, err := h.Q.QueueStatsByStageAndState(ctx)
    if err != nil {
        httpInternal(w, h.Log, err)
        return
    }
    eta, err := h.Q.QueueETATotals(ctx)
    if err != nil {
        httpInternal(w, h.Log, err)
        return
    }
    rtf, err := h.Q.QueueRealtimeFactorP50(ctx)
    if err != nil {
        httpInternal(w, h.Log, err)
        return
    }

    resp := statsResponse{
        ByStage: emptyByStage(),
        ByState: emptyByState(),
        ETATotalSec: eta.EtaTotalSec,
        RealtimeFactorP50: rtf.P50.Float64,
    }
    for _, row := range rows {
        // by_stage only carries the five UI-facing states.
        b := resp.ByStage[row.Stage]
        switch row.State {
        case "pending":
            b.Pending = row.N
        case "running":
            b.Running = row.N
        case "paused":
            b.Paused = row.N
        case "failed":
            b.Failed = row.N
        case "done":
            b.Done = row.N
        }
        resp.ByStage[row.Stage] = b

        resp.ByState[row.State] += row.N
    }
    httpJSON(w, http.StatusOK, resp)
}

func emptyByStage() map[string]stageBreakdown {
    out := make(map[string]stageBreakdown, len(stages))
    for _, s := range stages {
        out[s] = stageBreakdown{}
    }
    return out
}

func emptyByState() map[string]int64 {
    out := make(map[string]int64, len(states))
    for _, s := range states {
        out[s] = 0
    }
    return out
}
```

### 2.6 Prometheus collectors — Go side

`api/internal/queue/metrics.go`:

```go
package queue

import (
    "context"
    "log/slog"

    "github.com/prometheus/client_golang/prometheus"

    "maktaba/api/internal/db"
)

// jobsTotalCollector emits maktaba_jobs_total{stage,state} as a gauge by
// querying the DB on each scrape. Keeps the gauge fresh without needing
// the Pipeline to push every state change through the API process.
type jobsTotalCollector struct {
    q   *db.Queries
    log *slog.Logger

    desc *prometheus.Desc
}

func NewJobsTotalCollector(q *db.Queries, log *slog.Logger) *jobsTotalCollector {
    return &jobsTotalCollector{
        q:   q,
        log: log,
        desc: prometheus.NewDesc(
            "maktaba_jobs_total",
            "Number of processing jobs by stage and state.",
            []string{"stage", "state"}, nil,
        ),
    }
}

func (c *jobsTotalCollector) Describe(ch chan<- *prometheus.Desc) {
    ch <- c.desc
}

func (c *jobsTotalCollector) Collect(ch chan<- prometheus.Metric) {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    rows, err := c.q.JobsTotalGaugeRows(ctx)
    if err != nil {
        c.log.Warn("metrics_query_failed", "err", err)
        return
    }
    seen := make(map[[2]string]bool)
    for _, r := range rows {
        ch <- prometheus.MustNewConstMetric(
            c.desc, prometheus.GaugeValue, float64(r.N), r.Stage, r.State,
        )
        seen[[2]string{r.Stage, r.State}] = true
    }
    // Emit zero for missing pairs so PromQL alerts on "absent" don't fire
    // on a healthy-but-empty state.
    for _, s := range stages {
        for _, st := range states {
            if !seen[[2]string{s, st}] {
                ch <- prometheus.MustNewConstMetric(
                    c.desc, prometheus.GaugeValue, 0, s, st,
                )
            }
        }
    }
}
```

The router:

```go
reg := prometheus.NewRegistry()
reg.MustRegister(NewJobsTotalCollector(q, log))
// Counters / histograms come from the Pipeline service; the API also
// includes its own request-level metrics here (out of scope for this story).

r.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
```

### 2.7 Pipeline-side observability

`pipeline/src/maktaba_pipeline/observability.py`:

```python
"""Structured logs + Prometheus collectors for the queue layer."""
from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import structlog
from prometheus_client import Counter, Histogram, Summary, REGISTRY


# ---- Metric definitions (single source of truth) ------------------------

JOBS_ENQUEUED = Counter(
    "maktaba_jobs_enqueued_total",
    "Number of enqueue calls by stage and outcome.",
    ["stage", "outcome"],
)

JOB_ATTEMPTS = Counter(
    "maktaba_job_attempts_total",
    "Number of job attempt outcomes by stage.",
    ["stage", "outcome"],            # outcome: claimed | done | failed | retry | paused | cancelled
)

JOB_DURATION = Histogram(
    "maktaba_job_duration_seconds",
    "Wall-clock duration of a job from claim to terminal-or-paused.",
    ["stage", "outcome"],
    buckets=(0.5, 1, 5, 30, 60, 300, 600, 1800, 3600, 7200, 14400, 28800),
)

JOB_RTF = Summary(
    "maktaba_job_realtime_factor",
    "Audio-seconds per wall-second processed.",
    ["stage"],
)


# ---- Log-event helper --------------------------------------------------

_log = structlog.get_logger(__name__)


def log_event(event: str, **kw: Any) -> None:
    """Emit a structured log line. The keys job_id, video_id, stage,
    state, attempts MUST be present on every state-changing event;
    callers pass them in.
    """
    _log.info(event, **kw)
```

Each `mark_*` helper (Story 6.4 / 6.5 / runner.py) calls
`log_event` + `JOB_ATTEMPTS.labels(...).inc()` + `JOB_DURATION.labels(...).observe(...)`
in the same place. Centralizing names here avoids drift.

### 2.8 The `/metrics` endpoint on the Pipeline side

The Pipeline service runs a tiny `prometheus_client` HTTP server on
`pipeline.toml [observability].metrics_port = 9101` (default).
Prometheus scrapes the API's `/metrics` (request metrics + DB-backed
gauges) and the Pipeline's `/metrics` (counters + histograms +
summaries) as two distinct targets; the metric names align so PromQL
queries can `sum by (stage)` across both.

`pipeline/src/maktaba_pipeline/observability.py` (entrypoint):

```python
def start_metrics_server(port: int = 9101) -> None:
    from prometheus_client import start_http_server
    start_http_server(port)
    log_event("metrics_server_started", port=port)
```

## 3. Test plan

### 3.1 Stats endpoint (`api/internal/queue/stats_test.go`)

| Test | What it pins |
|---|---|
| `TestStats_AggregatesAcrossStagesAndStates` | Insert fixture: 12 transcribe pending, 1 transcribe running, 3 transcribe paused, 184 transcribe done; GET /api/queue/stats → matches the example response. |
| `TestStats_EmptyQueueReturnsZeros` | Truncate processing_jobs; GET → all by_stage entries present with zero counts; eta_total_sec=0; realtime_factor_p50=0. |
| `TestStats_AllStagesPresentEvenIfZero` | Insert one transcribe job; response includes scan/probe/extract/subtitle_gen/index/thumbnail entries with zero counts. |
| `TestStats_ETAOnlyCountsLiveStates` | Insert one done row with estimated_remaining_sec=999; one running with 50; eta_total_sec=50. |
| `TestStats_RealtimeFactorP50` | Insert running rows with rtf=[0.1,0.2,0.3,0.4,0.5]; p50=0.3. |

### 3.2 Metrics endpoint (`api/internal/queue/metrics_test.go`)

| Test | What it pins |
|---|---|
| `TestMetrics_IncludesMaktabaJobsTotal` | Scrape /metrics; assert `maktaba_jobs_total{stage="transcribe",state="running"} <count>` line present for every (stage, state) pair (56 lines). |
| `TestMetrics_GaugeReflectsCurrentCount` | Insert 5 running transcribe rows; scrape; assert `maktaba_jobs_total{stage="transcribe",state="running"} 5`. |
| `TestMetrics_FormatIsPrometheusText` | Content-Type starts with `text/plain; version=0.0.4`. |

### 3.3 Pipeline-side metrics (`pipeline/tests/observability/test_metrics.py`)

| Test | What it pins |
|---|---|
| `test_enqueue_increments_counter` | Call `enqueue` → `maktaba_jobs_enqueued_total{stage="probe",outcome="inserted"}` increments by 1. |
| `test_mark_done_observes_duration` | Run a stage (synthetic) for 0.2 s; call `mark_done`; histogram has one observation in the (0.5) bucket. |
| `test_tick_progress_updates_rtf_summary` | Two `tick_progress` calls with rtf=0.2 and 0.4 → `maktaba_job_realtime_factor{stage="transcribe"}` count=2, sum=0.6. |
| `test_metrics_endpoint_serves_prom_format` | Boot `start_metrics_server(0)`; HTTP GET / → 200, contains `maktaba_jobs_enqueued_total`. |
| `test_all_documented_metrics_emitted` | Walk through a full job lifecycle (enqueue → claim → progress → done); scrape; all four metric names from the story spec are present. |

### 3.4 Log-event tests (`pipeline/tests/observability/test_log_events.py`)

| Test | What it pins |
|---|---|
| `test_log_event_for_state_transition` | Run a job through pending → claimed → running → paused → running → done; capture logs; assert events `transition_to_running`, `paused_for_user`, `transition_to_done` are present, each carrying `{job_id, video_id, stage, state, attempts}`. |
| `test_log_event_includes_required_fields` | For each state transition log, assert the required keys are present and non-null (except `attempts` may be 0). |
| `test_failure_logs_at_warn_with_backoff_window` | Stage raises retryable error; log line `job_failed_will_retry` at WARN; carries `retry_in_sec` (the backoff). |
| `test_failure_log_debounced_by_not_before` | Same stage fails 3 times in 5 s; the second and third log lines are at DEBUG (not WARN) because `not_before` indicates the row is in backoff. |

## 4. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Empty queue | Stats endpoint returns zeros for every stage/state; metrics still emit (so "no jobs running for X minutes" alerts work). | `TestStats_EmptyQueueReturnsZeros`, `TestMetrics_GaugeReflectsCurrentCount` (zero-row variant) |
| Long-failing job creating noisy retry logs | Logged at WARN with backoff window on first failure; subsequent retries while `not_before > now` log at DEBUG. The `not_before` field is the debounce key. | `test_failure_log_debounced_by_not_before` |
| New stage added | The `stages` constant in both Go and Python emits a placeholder zero entry; PromQL queries still return values. The constants are derived from `Stage` enum (Story 6.1) — adding a stage there propagates here. | `test_default_caps_match_arch_7_4` (Story 6.7) catches divergence. |
| `realtime_factor` all NULL (no transcribe runs yet) | `percentile_disc` returns NULL; the Go handler maps NULL → 0.0 in JSON. The summary metric emits no observations until first tick. | `TestStats_RealtimeFactorP50` (zero-data variant) |
| SQLite has no `percentile_disc` | The Go handler detects SQLite via the driver, falls back to `SELECT realtime_factor FROM ... ORDER BY realtime_factor LIMIT 1 OFFSET (n/2)` for the median. | `TestStats_RealtimeFactorP50_SQLite` (dialect-parametrized) |
| Metric label cardinality | Stage × state = 7 × 8 = 56 series for `maktaba_jobs_total`. Plus (stage, outcome) = 7 × 6 ≈ 42 for `maktaba_job_attempts_total`. Well within Prometheus's comfort zone. | Documented; no test. |
| Scrape during a long DB query | The collector has a 2 s timeout; on timeout, it logs WARN and returns no metrics for that scrape. The stats endpoint has a 5 s deadline. | `TestMetrics_TimeoutDoesNotPanic` |
| Concurrent scrape and pipeline counter writes | `prometheus_client.Counter.inc` is atomic; no synchronization needed. | Inherited from the lib. |
| Counter cardinality explosion (operator tags videos with library_id) | The stage/state/outcome label set is the FULL allowed cardinality; we do NOT carry `video_id` or `library_id` on metrics. Per-video tracking belongs in logs and the DB, not metrics. | Documented in `observability.py` module docstring. |

## 5. Performance analysis

### 5.1 Stats endpoint

Three queries: `GROUP BY stage, state` (uses no specific index; the
table is small enough — bounded by lifetime job count, ~10s of K
typical), one `SUM(estimated_remaining_sec) FILTER`, one
`percentile_disc`. All complete in < 50 ms warm. The endpoint is
suitable for 1 Hz polling from the UI.

### 5.2 Metrics scrape

`Collect` runs one query (`JobsTotalGaugeRows`); same `GROUP BY` cost
~30 ms. Prometheus's default scrape interval (15 s) gives ample
headroom.

## 6. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `prometheus-client` (Python) | ≥ 0.20 | The de-facto Python Prometheus library. |
| `github.com/prometheus/client_golang/prometheus` | already present | The de-facto Go Prometheus library. |
| `structlog` | already pinned | JSON-format logs. |
| `sqlc` | dev-only | Generates the queue-stats query bindings. |

## 7. Acceptance checklist

**Endpoints**
- [ ] `GET /api/queue/stats` returns the JSON shape from the story spec (all stages and all states present even if zero).
- [ ] `GET /metrics` includes `maktaba_jobs_total{stage,state}` for every (stage, state) pair.
- [ ] `GET /metrics` (Pipeline side, default port 9101) emits `maktaba_jobs_enqueued_total`, `maktaba_job_attempts_total`, `maktaba_job_duration_seconds`, `maktaba_job_realtime_factor`.

**Logs**
- [ ] Every state-changing event (`transition_to_*`, `paused_*`, `cancelled`, `failed`, `enqueued`, `reaped`) emits a structlog line with `{job_id, video_id, stage, state, attempts}`.
- [ ] Retry storms debounce to DEBUG after the first WARN.

**Behaviour (story acceptance criteria)**
- [ ] AC: `test_stats_aggregates_correctly` — fixture jobs match expected response.
- [ ] AC: `test_metrics_include_all_required_keys` — every metric and label present.
- [ ] AC: `test_log_event_for_state_transition` — full lifecycle covered.

**Performance**
- [ ] `/api/queue/stats` p95 < 100 ms warm against a 10K-row table.
- [ ] `/metrics` scrape p95 < 100 ms warm.

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.9.
- [ ] A runbook entry in `specs/architecture.md` (or a follow-up `specs/runbooks/queue.md`) lists the expected metric names and example PromQL queries.
