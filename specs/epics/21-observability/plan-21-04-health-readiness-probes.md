# Implementation Plan — Story 21.4 Health & Readiness Probes

> Companion to [story-21-04-health-readiness-probes.md](story-21-04-health-readiness-probes.md).
> `/healthz` (always 200 if alive) and `/readyz` (deps green) per service plus
> `/api/system/health` aggregator on the API.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Bind | Admin port (api 9100, streaming 9101, pipeline 9102 — same as metrics, distinct mux). |
| Liveness | Only checks the process itself. |
| Readiness | Bounded checks: DB conn, gRPC peer reachability, cache warmed flag. |
| Aggregator | API queries other services' `/readyz` over the internal network with a 1 s timeout. |
| Auth | Default off (admin port localhost-only). |

## 1. Project layout

```
shared/health/
├── go/
│   ├── healthz.go
│   ├── readyz.go
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

## 2. Liveness handler

```go
// shared/health/go/healthz.go
type Live struct{ ready atomic.Bool }
func (l *Live) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte(`{"status":"ok"}`))
}
```

Never reads DB; never blocks; ~100 ns budget.

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

```go
// shared/health/go/readyz.go
type Ready struct{ checks []Check; warmingUntil time.Time }

func (r *Ready) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    ctx, cancel := context.WithTimeout(req.Context(), 800*time.Millisecond)
    defer cancel()
    out := map[string]string{}
    bad := false
    if time.Now().Before(r.warmingUntil) {
        out["overall"] = "warming"
        bad = true
    }
    for _, c := range r.checks {
        if err := c.Run(ctx); err != nil {
            out[c.Name()] = err.Error()
            bad = true
        } else {
            out[c.Name()] = "ok"
        }
    }
    if bad {
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

func (h *Aggregator) deriveStatus(s SystemHealth) string {
    bad := 0
    for _, v := range s.Services { if v.Status != "ok" { bad++ } }
    switch {
    case bad == 0 && s.DiskFreeBytes > 1<<30: return "ok"
    case bad == len(s.Services):              return "down"
    default:                                   return "degraded"
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
WatchdogSec=30
NotifyAccess=main
# sd_notify("READY=1") only after /readyz first returns 200
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
