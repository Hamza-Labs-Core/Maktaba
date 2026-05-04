# Implementation Plan — Story 18.1 Latency Budgets

> Companion to [story-18-01-latency-budgets.md](story-18-01-latency-budgets.md).
> Codify p50/p95/p99 budgets in `shared/perf_budgets.yaml` and load them into
> the perf harness so regressions fail CI.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Source of truth | `shared/perf_budgets.yaml`. Loaded by Go and Python harnesses. |
| Harness | `tests/perf/` (Go runner) drives REST + WS budgets. Python helper for pipeline-only stages. |
| Hardware tags | `mac-m2-8gb`, `linux-amd64-16gb`. Detected at runtime via `runtime.GOOS+GOARCH+memtotal`. |
| CI target | `make perf` — runs against the seeded `dev` compose stack against fixture `tests/fixtures/perf-1k/`. |
| Out of scope | Search-specific budgets (Story 18.2 owns search numbers); pipeline throughput (18.4); client TTI (18.6). |

## 1. Project layout

```
shared/
└── perf_budgets.yaml                 # canonical
tests/
└── perf/
    ├── budgets.go                    # loader + validator
    ├── budgets_test.go
    ├── runner.go                     # measures p50/p95/p99
    ├── endpoints/
    │   ├── api_libraries.go
    │   ├── api_videos.go
    │   ├── api_search.go
    │   ├── streaming_manifest.go
    │   ├── streaming_segment.go
    │   └── ws_progress.go
    ├── profile.go                    # detect hardware profile
    └── report.go                     # breach reporting
Makefile                              # `make perf` target
```

## 2. Budget file format

```yaml
# shared/perf_budgets.yaml
version: 1
profiles:
  - tag: mac-m2-8gb
    cpu: apple-m2
    mem_gb: 8
  - tag: linux-amd64-16gb
    cpu: x86_64
    mem_gb: 16

endpoints:
  - name: api.libraries.list
    method: GET
    path: /api/libraries
    profile: mac-m2-8gb
    cache: warm
    p50_ms: 30
    p95_ms: 80
    p99_ms: 150

  - name: api.videos.get
    method: GET
    path: /api/videos/{id}
    profile: mac-m2-8gb
    cache: warm
    p50_ms: 25
    p95_ms: 60
    p99_ms: 120

  - name: api.search.warm
    method: POST
    path: /api/search
    profile: mac-m2-8gb
    cache: warm
    p50_ms: 250
    p95_ms: 500
    p99_ms: 800

  - name: api.search.cold
    method: POST
    path: /api/search
    profile: mac-m2-8gb
    cache: cold
    p95_ms: 1500
    p99_ms: 2000

  - name: streaming.manifest.first_byte
    method: GET
    path: /stream/{id}/master.m3u8
    profile: mac-m2-8gb
    cache: warm
    p50_ms: 50
    p95_ms: 120

  - name: streaming.segment.first_byte.warm
    method: GET
    path: /stream/{id}/{rendition}/{seg}.ts
    profile: mac-m2-8gb
    cache: warm
    p50_ms: 40
    p95_ms: 100

  - name: streaming.segment.first_byte.cold
    method: GET
    path: /stream/{id}/{rendition}/{seg}.ts
    profile: mac-m2-8gb
    cache: cold
    p50_ms: 2500
    p95_ms: 6000

  - name: ws.job_progress.notify_to_client
    profile: mac-m2-8gb
    cache: warm
    p95_ms: 250

  - name: web.player.first_frame.warm
    profile: mac-m2-8gb
    cache: warm
    p95_ms: 1500

  - name: web.player.first_frame.cold
    profile: mac-m2-8gb
    cache: cold
    p95_ms: 3500

# Throughput budgets (events / records per second). Owned by stories 18.4/18.5
# under their respective stages but the canonical numbers live here so CI
# enforces a single source of truth.
throughputs:
  - name: pipeline.transcribe.realtime_factor
    profile: mac-m2-8gb
    min_value: 1.0     # 1× realtime CPU floor (faster-whisper fallback)
    ci_pr: false       # nightly only — too slow for PR gate

# Resource envelopes (peak RSS, sustained CPU%, FD ceilings). Owned by
# story 18.5 — see §5 of plan-18-05 for population.
envelopes:
  - name: pipeline.per_model_rss_mib
    profile: mac-m2-8gb
    max_value: 4500
    ci_pr: false
```

## 3. Loader and validator

```go
// tests/perf/budgets.go
package perf

import (
    "fmt"
    "os"
    "gopkg.in/yaml.v3"
)

type Budget struct {
    Name    string `yaml:"name"`
    Method  string `yaml:"method,omitempty"` // omit for non-HTTP entries (WS, web vitals)
    Path    string `yaml:"path,omitempty"`   // omit for non-HTTP entries
    Profile string `yaml:"profile"`
    Cache   string `yaml:"cache"`     // "warm" | "cold"
    P50ms   int    `yaml:"p50_ms,omitempty"` // 0 = skipped (no p50 budget)
    P95ms   int    `yaml:"p95_ms,omitempty"`
    P99ms   int    `yaml:"p99_ms,omitempty"`
    CIPR    bool   `yaml:"ci_pr"`     // true => enforced on every PR; false => nightly only
}

// Throughput is min-value oriented (records/s, frames/s, realtime factor).
type Throughput struct {
    Name     string  `yaml:"name"`
    Profile  string  `yaml:"profile"`
    MinValue float64 `yaml:"min_value"`
    CIPR     bool    `yaml:"ci_pr"`
}

// Envelope is max-value oriented (peak RSS MiB, sustained CPU %, FD count).
type Envelope struct {
    Name     string  `yaml:"name"`
    Profile  string  `yaml:"profile"`
    MaxValue float64 `yaml:"max_value"`
    CIPR     bool    `yaml:"ci_pr"`
}

type BudgetFile struct {
    Version     int          `yaml:"version"`
    Profiles    []Profile    `yaml:"profiles"`
    Endpoints   []Budget     `yaml:"endpoints"`
    Throughputs []Throughput `yaml:"throughputs"`
    Envelopes   []Envelope   `yaml:"envelopes"`
}

func Load(path string) (*BudgetFile, error) {
    raw, err := os.ReadFile(path)
    if err != nil { return nil, err }
    var f BudgetFile
    if err := yaml.Unmarshal(raw, &f); err != nil { return nil, err }
    if err := f.Validate(); err != nil { return nil, err }
    return &f, nil
}

func (f *BudgetFile) Validate() error {
    seen := map[string]bool{}
    for i, b := range f.Endpoints {
        if b.Name == "" {
            return fmt.Errorf("endpoint[%d]: missing name", i)
        }
        key := b.Name + "/" + b.Profile + "/" + b.Cache
        if seen[key] {
            return fmt.Errorf("duplicate budget: %s", key)
        }
        seen[key] = true
        if b.P50ms < 0 || b.P95ms < 0 || b.P99ms < 0 {
            return fmt.Errorf("%s: negative ms", b.Name)
        }
        // A zero/absent percentile is treated as "skipped". Ordering checks
        // only fire when both adjacent percentiles are populated (>0).
        if b.P50ms > 0 && b.P95ms > 0 && b.P95ms < b.P50ms {
            return fmt.Errorf("%s: p95 < p50", b.Name)
        }
        if b.P95ms > 0 && b.P99ms > 0 && b.P99ms < b.P95ms {
            return fmt.Errorf("%s: p99 < p95", b.Name)
        }
        if b.Cache != "warm" && b.Cache != "cold" {
            return fmt.Errorf("%s: cache must be warm|cold", b.Name)
        }
    }
    return nil
}
```

## 4. Runner

5 trials per endpoint after 1 warm-up, take median for fail/pass; report 3σ outliers but don't fail unless median breaches.

```go
// tests/perf/runner.go
type Sample struct{ DurMs float64 }

type Result struct {
    Budget Budget
    Trials []Sample
    P50    float64
    P95    float64
    P99    float64
    Breach bool
    OutliersOver3Sigma int
}

func RunBudget(ctx context.Context, b Budget, exec func(ctx context.Context) (time.Duration, error)) Result {
    const trials = 5
    samples := make([]Sample, 0, trials)
    _, _ = exec(ctx) // warm-up
    for i := 0; i < trials; i++ {
        d, err := exec(ctx)
        if err != nil { return Result{Budget: b, Breach: true} }
        samples = append(samples, Sample{DurMs: float64(d)/1e6})
    }
    p50, p95, p99 := percentiles(samples, 50, 95, 99)
    breach := false
    if b.P50ms > 0 && p50 > float64(b.P50ms) { breach = true }
    if b.P95ms > 0 && p95 > float64(b.P95ms) { breach = true }
    if b.P99ms > 0 && p99 > float64(b.P99ms) { breach = true }
    return Result{Budget: b, Trials: samples, P50: p50, P95: p95, P99: p99, Breach: breach}
}
```

## 5. Hardware profile detection

```go
// tests/perf/profile.go
func DetectProfile() (string, error) {
    arch := runtime.GOARCH
    os := runtime.GOOS
    mem := totalMemoryGB()
    switch {
    case os == "darwin" && arch == "arm64" && mem >= 7 && mem <= 9:
        return "mac-m2-8gb", nil
    case os == "linux" && arch == "amd64" && mem >= 14 && mem <= 17:
        return "linux-amd64-16gb", nil
    }
    return "", fmt.Errorf("no profile for %s/%s/%dGB; add a profile tag and re-run", os, arch, mem)
}
```

## 6. Make target

```makefile
# Makefile
.PHONY: perf perf-warm perf-cold

perf: perf-warm perf-cold

perf-warm:
	@scripts/seed-fixture.sh tests/fixtures/perf-1k
	@scripts/warm-caches.sh
	@go test -tags=perf -timeout=10m ./tests/perf -run TestBudgets -cache=warm

perf-cold:
	@scripts/seed-fixture.sh tests/fixtures/perf-1k
	@scripts/cold-caches.sh
	@go test -tags=perf -timeout=20m ./tests/perf -run TestBudgets -cache=cold
```

## 7. Breach reporting

```text
FAIL: perf budgets (mac-m2-8gb, warm) — 2 breach(es)

  api.search.warm
    p50  measured 312 ms  budget 250 ms  Δ +62 ms (+24.8 %)
    p95  measured 671 ms  budget 500 ms  Δ +171 ms (+34.2 %)
  streaming.segment.first_byte.warm
    p95  measured 134 ms  budget 100 ms  Δ +34 ms (+34.0 %)

7/9 budgets passed.
```

## 8. Cache state handling

`scripts/warm-caches.sh` issues a synthetic warm-up: walks the fixture videos, requests manifests, top-100 search queries, prefetch JWKS. `scripts/cold-caches.sh` flushes via the canonical whole-cache admin endpoint owned by Story 18.8 — `POST /admin/cache/{name}/flush` — before the run. (Per-key eviction `POST /admin/cache/segments/evict?hash=…&rendition=…&seg=…` lives in plan-18-03 and is not used here.)

If `--cache=warm` is passed but cache hit-rate < 50 %, the run aborts with **"Warm suite running on cold instance — refusing to record results."**

## 9. Edge cases

| Case | Handling |
|---|---|
| Outlier > 3σ | Reported in stderr, doesn't fail unless median breaches (jitter from CI runner). |
| Hardware profile mismatch | Loader fails fast: "add `linux-arm64-32gb` to perf_budgets.yaml". |
| Endpoint listed but unreachable | HTTP error counts as breach with reason `unreachable`. |
| Cold suite hits warm cache by accident | Hit-rate metric > 5 % during run aborts with "expected cold, found warm cache". |
| Path-template substitution | Path tokens `{id}` resolved from a per-endpoint fixture map (`api.videos.get → fixture.videos[0].id`). |

## 10. Test cases

### TC1 — Loader rejects malformed
- `tests/perf/budgets_test.go` covers: missing name, negative ms, p99 < p95, unknown cache tag, duplicate (name, profile, cache) tuples.

### TC2 — Integration green
- Seed `perf-1k` fixture, warm caches, run all warm budgets, assert all pass.

### TC3 — Regression detection
- Inject a `time.Sleep(200ms)` in the `videos.GetByID` middleware via build tag `perftest_inject_videos_slow`.
- Re-run; assert process exit code != 0 and stderr contains `api.videos.get` and `Δ +200 ms`.

## 11. Test fixtures

- `tests/fixtures/perf-1k/`: 1,000 video records, 10,000 segments, ~5 GB media (synthetic colorbar mp4s with stable hashes).
- Seeded via `scripts/seed-fixture.sh` from `seed-perf-1k.sql` + media generator.

## 12. CI integration

- `make perf` runs in nightly job on a self-hosted M2 mini runner tagged `mac-m2-8gb`.
- A linux runner tagged `linux-amd64-16gb` runs the same suite for parity.
- PR-gate subset: only entries with `ci_pr: true` run on every PR; the rest are nightly-only.
- Reports artifact: `tests/perf/results.json` published per run.
- A Grafana panel charts each budget's measured p95 over time (non-blocking, just for trend).

## 13. Dependencies

- Story 18.8 (cache flush admin endpoint) — required for cold suite.
- Story 21.2 (metrics surface) — `db_query_count_total`, cache hit metrics queried by harness.
- Story 20.2 (perf fixtures `perf-1k`).
