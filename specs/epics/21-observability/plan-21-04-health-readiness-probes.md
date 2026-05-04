# Implementation Plan — Story 21.4 Health & Readiness Probes

> Companion to [story-21-04-health-readiness-probes.md](story-21-04-health-readiness-probes.md).
> `/healthz` (always 200 if alive) and `/readyz` (deps green) per service plus
> `/api/system/health` aggregator on the API.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Bind | Admin port (api 9100, streaming 9101, pipeline 9102) — **this plan owns `shared/admin/mux.go`** and binds the listener exactly once per service. plan-21-02 registers `/metrics` on the same mux. |
| Liveness | Process self-check: serves an in-memory query (e.g., reads an `atomic.Int64` request counter and returns it) so a wedged event loop fails the probe per story EC1; **not** a hard-coded 200. |
| Readiness | Bounded checks run in parallel via `errgroup` with a 200 ms per-check timeout under an 800 ms total budget; checks: DB conn, gRPC peer reachability, cache warmed flag. |
| Watchdog | systemd `WatchdogSec=30` is paired with a Go-side `daemon.SdNotify("WATCHDOG=1")` heartbeat at half-period (`15s`) using `coreos/go-systemd/v22/daemon`. `READY=1` is sent only after `/readyz` first returns 200. |
| Aggregator | API queries other services' `/readyz` over the internal network with a 1 s timeout, in parallel via `errgroup`. |
| Auth | Admin port is **localhost-only** by default. When `expose_metrics_publicly: true` (plan-21-02) makes the port reachable, all admin routes (`/metrics`, `/readyz`) require the bearer token; `/healthz` is **exempt** from auth so external orchestrators (Docker, systemd, k8s) can probe without a credential. |

## 1. Project layout

```
shared/admin/
├── mux.go                   # admin-port mux owner (this plan)
├── auth.go                  # bearer token (shared with /metrics under plan-21-02)
└── mux_test.go

shared/health/
├── go/
│   ├── healthz.go
│   ├── readyz.go
│   ├── watchdog.go          # daemon.SdNotify(...) loop
│   ├── checks.go            # interface + helpers
│   └── handler_test.go
└── py/
    ├── healthz.py
    ├── readyz.py
    └── tests/

api/internal/system/
├── health_aggregator.go     # /api/system/health
└── aggregator_test.go
```

### 1.1 Admin-port mux ownership

This plan owns `shared/admin/mux.go`, the single `*http.ServeMux` bound
to the admin port (`127.0.0.1:9100/9101/9102`, one per service):

```go
// shared/admin/mux.go
package admin

import "net/http"

type Mux struct{ mux *http.ServeMux }

func New() *Mux { return &Mux{mux: http.NewServeMux()} }

func (m *Mux) Handle(pattern string, h http.Handler) { m.mux.Handle(pattern, h) }
func (m *Mux) HandleFunc(pattern string, h http.HandlerFunc) { m.mux.HandleFunc(pattern, h) }
func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

// Serve binds bind once per process. plan-21-02 registers /metrics
// against this same mux, so the admin port hosts /healthz, /readyz,
// and /metrics on a single TCP listener.
func (m *Mux) Serve(bind string) error { return http.ListenAndServe(bind, m) }
```

Cross-references:
- plan-21-02 calls `metrics.Register(adminMux, …)` to attach `/metrics`.
- plan-22-03 documents the Caddyfile / systemd notes for exposing the
  admin port (default behavior: do NOT expose; localhost-only).

## 2. Liveness handler

Story EC1 requires liveness to actually verify the process is responsive
rather than always returning 200. The handler executes a tiny in-memory
query that exercises the request path: reads an atomic counter, performs
a bounded `select` against an internal channel, and returns the snapshot.
A wedged or deadlocked event loop fails the probe (the orchestrator
restarts), while a slow database does **not** affect liveness — that's
`/readyz`'s job.

```go
// shared/health/go/healthz.go
type Live struct {
    requestsServed atomic.Int64
    pingCh         chan struct{}     // size 1; never blocked because consumer in same goroutine
    started        atomic.Int64      // unix nano when process initialized
}

func (l *Live) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Bounded in-memory roundtrip — proves the goroutine scheduler and
    // the HTTP path are alive. 50 ms cap; if exceeded, return 503 so the
    // orchestrator restarts the wedged process.
    ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
    defer cancel()

    select {
    case l.pingCh <- struct{}{}:
        <-l.pingCh
    case <-ctx.Done():
        w.WriteHeader(http.StatusServiceUnavailable)
        _, _ = w.Write([]byte(`{"status":"wedged"}`))
        return
    }

    n := l.requestsServed.Add(1)
    uptime := time.Duration(time.Now().UnixNano() - l.started.Load())
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, `{"status":"ok","served":%d,"uptime_s":%d}`, n, int64(uptime.Seconds()))
}
```

`/healthz` does not read the DB and does not call any dependency; the
~100 µs budget covers the in-memory roundtrip even on a busy host.

### 2.1 systemd watchdog

When the unit declares `WatchdogSec=30` (see §6 below) systemd expects
periodic `WATCHDOG=1` notifications. Wire this from Go using
`github.com/coreos/go-systemd/v22/daemon`:

```go
// shared/health/go/watchdog.go
import "github.com/coreos/go-systemd/v22/daemon"

func StartWatchdog(ctx context.Context) {
    interval, err := daemon.SdWatchdogEnabled(false)
    if err != nil || interval == 0 { return }   // not running under systemd
    tick := time.NewTicker(interval / 2)         // half-period (15 s if WatchdogSec=30)
    go func() {
        defer tick.Stop()
        for {
            select {
            case <-ctx.Done(): return
            case <-tick.C:
                _, _ = daemon.SdNotify(false, daemon.SdNotifyWatchdog)
            }
        }
    }()
}

// Called once /readyz first returns 200.
func NotifyReady() { _, _ = daemon.SdNotify(false, daemon.SdNotifyReady) }
```

## 3. Readiness checks

```go
// shared/health/go/checks.go
type Check interface {
    Name() string
    Run(ctx context.Context) error
}

type DBPing struct{ DB *sql.DB }
func (c *DBPing) Name() string { return "db" }
func (c *DBPing) Run(ctx context.Context) error { return c.DB.PingContext(ctx) }

type GRPCPing struct{ Name_ string; Conn *grpc.ClientConn }
func (c *GRPCPing) Name() string { return c.Name_ }
func (c *GRPCPing) Run(ctx context.Context) error {
    s := c.Conn.GetState()
    if s == connectivity.Ready { return nil }
    if !c.Conn.WaitForStateChange(ctx, s) { return errors.New("grpc not ready") }
    if c.Conn.GetState() != connectivity.Ready { return errors.New("grpc not ready after wait") }
    return nil
}

type CacheWarm struct{ Cache cache.Cache; MinHits float64 }
func (c *CacheWarm) Run(ctx context.Context) error {
    s := c.Cache.Stats()
    total := s.Hits + s.Misses
    if total < 100 { return errors.New("warming") }
    if float64(s.Hits)/float64(total) < c.MinHits { return errors.New("cache below warm threshold") }
    return nil
}
```

Checks run in parallel via `golang.org/x/sync/errgroup` so total
latency = max(per-check latency) rather than the sum:

```go
// shared/health/go/readyz.go
import "golang.org/x/sync/errgroup"

type Ready struct{ checks []Check; warmingUntil time.Time }

func (r *Ready) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    ctx, cancel := context.WithTimeout(req.Context(), 800*time.Millisecond)
    defer cancel()

    out := map[string]string{}
    var mu sync.Mutex
    bad := atomic.Bool{}

    if time.Now().Before(r.warmingUntil) {
        out["overall"] = "warming"
        bad.Store(true)
    }

    g, gctx := errgroup.WithContext(ctx)
    for _, c := range r.checks {
        c := c
        g.Go(func() error {
            cctx, ccancel := context.WithTimeout(gctx, 200*time.Millisecond)
            defer ccancel()
            err := c.Run(cctx)
            mu.Lock()
            if err != nil { out[c.Name()] = err.Error(); bad.Store(true) } else { out[c.Name()] = "ok" }
            mu.Unlock()
            return nil // never short-circuit; we want every check's status
        })
    }
    _ = g.Wait()

    if bad.Load() {
        w.WriteHeader(http.StatusServiceUnavailable)
    } else {
        w.WriteHeader(http.StatusOK)
        out["overall"] = "ok"
    }
    _ = json.NewEncoder(w).Encode(out)
}
```

`warmingUntil = startTime + 30s` per AC TC3.

## 4. API aggregator

```go
// api/internal/system/health_aggregator.go
type Service struct {
    Name string
    URL  string                       // e.g. http://streaming-1:9101/readyz
}

type SystemHealth struct {
    Status         string                  `json:"status"`           // ok, degraded, down
    Services       map[string]Snapshot     `json:"services"`
    DiskFreeBytes  uint64                  `json:"disk_free_bytes"`
    QueueDepth     int                     `json:"queue_depth"`
    // BudgetUSDLeft is sourced from the transcribe budget surface owned
    // by Story 19.7 (`budget.RemainingThisMonth`). If Story 19.7 is not
    // yet shipped, the aggregator omits the field (renders as 0) and
    // does not include it in `deriveStatus` decisions.
    BudgetUSDLeft  decimal.Decimal         `json:"transcribe_budget_usd_left"`
}

func (h *Aggregator) Serve(w http.ResponseWriter, r *http.Request) {
    res := SystemHealth{Services: map[string]Snapshot{}}
    g, gctx := errgroup.WithContext(r.Context())
    var mu sync.Mutex
    for _, s := range h.services {
        s := s
        g.Go(func() error {
            ctx, cancel := context.WithTimeout(gctx, time.Second)
            defer cancel()
            snap, _ := h.probe(ctx, s.URL)
            mu.Lock(); res.Services[s.Name] = snap; mu.Unlock()
            return nil
        })
    }
    _ = g.Wait()
    res.DiskFreeBytes = h.disk.Free()
    res.QueueDepth, _ = h.queue.Depth(r.Context())
    res.BudgetUSDLeft, _ = h.budget.RemainingThisMonth(r.Context())
    res.Status = h.deriveStatus(res)
    _ = json.NewEncoder(w).Encode(res)
}

// deriveStatus implements story EC2:
//   - ok:       every service ok AND disk_free > 1 GiB
//   - degraded: at least one service down, but at least one ok (UI banner;
//               playback / import / search routes that target the
//               affected service degrade gracefully)
//   - down:     every probed service down (or no services configured)
//
// Disk-low alone (services ok, disk_free <= 1 GiB) maps to `degraded` so
// the operator is alerted before transcodes start failing.
func (h *Aggregator) deriveStatus(s SystemHealth) string {
    bad, ok := 0, 0
    for _, v := range s.Services {
        if v.Status == "ok" { ok++ } else { bad++ }
    }
    diskLow := s.DiskFreeBytes <= 1<<30
    switch {
    case bad == 0 && !diskLow:
        return "ok"
    case ok == 0 && len(s.Services) > 0:
        return "down"
    default:
        return "degraded"
    }
}
```

`Snapshot` includes per-check status copied from each service's `/readyz` body, so the UI shows precisely which dependency is failing.

## 5. SQLite-mode probe matrix (EC3)

```go
// readiness setup
checks := []Check{}
if cfg.DB.Driver == "postgres" {
    checks = append(checks, &DBPing{DB: db})
} else { // sqlite
    checks = append(checks, &SQLitePing{DB: db})
}
if cfg.Pipeline.Enabled {
    checks = append(checks, &GRPCPing{Name_: "pipeline", Conn: pipelineConn})
}
```

`docs/runbooks/probe-matrix.md` documents which checks are expected per mode.

## 6. Probes wired into orchestrator

`deploy/compose/dev.yml`:

```yaml
api:
  healthcheck:
    test: ["CMD", "curl", "-fsS", "http://localhost:9100/healthz"]
    interval: 5s
    timeout: 2s
    start_period: 30s
streaming:
  healthcheck: { test: ["CMD","curl","-fsS","http://localhost:9101/healthz"], interval: 5s }
```

systemd unit:

```ini
ExecStartPre=/bin/sh -c 'true'
ExecStart=/usr/local/bin/maktaba-api
Type=notify
WatchdogSec=30
NotifyAccess=main
# Go process pairs:
#   - daemon.SdNotify(SdNotifyReady)    once /readyz first returns 200
#   - daemon.SdNotify(SdNotifyWatchdog) every WatchdogSec/2 (15 s)
# See shared/health/go/watchdog.go (§2.1).
```

## 7. Test cases

### TC1 — Liveness vs readiness
`docker compose stop postgres`. Verify `curl localhost:9100/healthz` returns 200 within 100 ms; `curl localhost:9100/readyz` returns 503 with body listing `db: connection refused`.

### TC2 — Aggregator
Stop pipeline. `GET /api/system/health` returns:

```json
{
  "status": "degraded",
  "services": {
    "pipeline": { "status": "down", "checks": { "pipeline": "grpc_unavailable" } },
    "streaming": { "status": "ok", "checks": { ... } }
  }
}
```

### TC3 — Cold start warming
Boot service. Within first 30 s, `/readyz` returns 503 with `overall=warming`. After 30 s, becomes 200 (assuming deps up).

### EC1 — DB primary failover
Run failover script. `/readyz` returns 503 then 200 within ~30 s as pgxpool reconnects.

### EC2 — Streaming fully down
Stop both streaming replicas. Aggregator response shows `services.streaming.status=down`. UI displays "playback offline" banner; search and library remain functional.

### EC3 — SQLite mode
Boot with `db.driver=sqlite`. `/readyz` checks: `sqlite_ping` only. No `pipeline` check if `[pipeline].enabled=false`.

## 8. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 DB failover | story | Pool reconnect; readiness flips. |
| EC2 streaming all down | story | Aggregator surfaces; UI degrades cleanly. |
| EC3 SQLite | story | Per-mode check matrix. |
| Probe latency contention | impl | 800 ms cumulative budget; per-check 200 ms. |
| Cascading restarts | impl | `/healthz` always 200 → orchestrator only restarts on TCP fail; readiness alone never restarts. |

## 9. Configuration

```yaml
health:
  bind_admin: 127.0.0.1:9100
  warm_period: 30s
  ready:
    db_check: true
    grpc_peers:
      - name: pipeline
        addr: pipeline:9090
      - name: streaming
        addr: streaming:9090
    cache_warm:
      - name: probe
        min_hit_rate: 0.99
```

## 10. Dependencies

- Story 21.2 (admin port shared with metrics).
- Story 21.7 (queue depth in aggregator).
- Story 19.5 (DB failover scenarios).
- Story 19.7 (transcribe budget remaining).
- Epic 22 devops (orchestrator wiring).
