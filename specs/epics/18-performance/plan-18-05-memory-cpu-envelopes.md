# Implementation Plan — Story 18.5 Memory & CPU Envelopes

> Companion to [story-18-05-memory-cpu-envelopes.md](story-18-05-memory-cpu-envelopes.md).
> Per-service RSS ceilings, no-leak soak, goroutine/asyncio-task tracking.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Measurement | RSS via OS (`/proc/<pid>/smaps_rollup` PSS on Linux; `task_info` on macOS). Never trust `runtime.MemStats` for the budget assertion. |
| Soak driver | `tests/soak/` — Go program that exercises representative load for 24 h. |
| Burst driver | Same harness, 10× steady-state for 5 min. |
| Goroutine/task tracking | `expvar` exports `goroutines` (Go); `asyncio.all_tasks()` count exports as a Prometheus gauge (Python). |
| Out of scope | Capacity (Epic 19); budgets-file structure (Story 18.1). |

## 1. Project layout

```
tests/soak/
├── main.go                 # `go run ./tests/soak --hours=24 --service=api`
├── workload/
│   ├── api.go              # 200 qps mix
│   ├── streaming.go        # 8 concurrent transcodes
│   └── pipeline.go         # 1 transcribe + idle index loop
├── meter/
│   ├── rss_linux.go        # smaps_rollup PSS
│   ├── rss_darwin.go       # task_info_64
│   ├── slope.go            # linear regression slope
│   └── goroutines.go       # poll /debug/vars
├── report.go               # CSV + summary
shared/instrumentation/
├── go/expvar.go            # publishes runtime stats
└── py/asyncio_metrics.py   # asyncio task gauge
```

## 2. RSS measurement

```go
// meter/rss_linux.go
func PSS(pid int) (int64, error) {
    f, err := os.Open(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
    if err != nil { return 0, err }
    defer f.Close()
    s := bufio.NewScanner(f)
    for s.Scan() {
        if strings.HasPrefix(s.Text(), "Pss:") {
            var n int64; fmt.Sscanf(s.Text(), "Pss: %d kB", &n)
            return n * 1024, nil
        }
    }
    return 0, fmt.Errorf("no Pss line")
}
```

```go
// meter/rss_darwin.go (CGO via mach task_info)
//go:build darwin
package meter
/*
#include <mach/mach.h>
*/
import "C"
import "unsafe"

func PSS(pid int) (int64, error) {
    var task C.task_t
    var info C.mach_task_basic_info_data_t
    count := C.mach_msg_type_number_t(C.MACH_TASK_BASIC_INFO_COUNT)
    if rc := C.task_for_pid(C.mach_task_self_(), C.int(pid), &task); rc != 0 {
        return 0, fmt.Errorf("task_for_pid: %d", rc)
    }
    if rc := C.task_info(task, C.MACH_TASK_BASIC_INFO,
        (*C.integer_t)(unsafe.Pointer(&info)), &count); rc != 0 {
        return 0, fmt.Errorf("task_info: %d", rc)
    }
    return int64(info.resident_size), nil
}
```

EC1 mapping: PSS includes CGO heap because it reads OS-level pages, not Go runtime.

## 3. Slope test (no-leak)

```go
// meter/slope.go
func SlopeMiBPerHour(samples []Sample) float64 {
    // simple OLS y = a + bx where x is hours since first sample
    var sx, sy, sxx, sxy float64
    n := float64(len(samples))
    t0 := samples[0].T
    for _, s := range samples {
        x := s.T.Sub(t0).Hours()
        y := float64(s.RSSBytes) / (1024*1024)
        sx += x; sy += y; sxx += x*x; sxy += x*y
    }
    return (n*sxy - sx*sy) / (n*sxx - sx*sx)
}
```

Assertion: `slope < 1.0` MiB/h.

## 4. Soak driver

```go
// tests/soak/main.go
func main() {
    flag := parseFlags()
    pid := launchService(flag.Service)
    workload := workload.For(flag.Service)
    go workload.Run(ctx)

    interval := time.Minute
    samples := []meter.Sample{}
    timer := time.NewTicker(interval)
    deadline := time.Now().Add(time.Duration(flag.Hours) * time.Hour)

    for time.Now().Before(deadline) {
        <-timer.C
        rss, _ := meter.PSS(pid)
        gor := meter.GoroutineCount(flag.AdminURL)
        samples = append(samples, meter.Sample{T: time.Now(), RSSBytes: rss, Goroutines: gor})
    }

    slope := meter.SlopeMiBPerHour(samples)
    if slope >= 1.0 {
        log.Fatalf("RSS slope %.3f MiB/h >= 1.0 — leak", slope)
    }
}
```

EC3 mapping: macOS-specific helper disables App Nap and sets QoS:

```go
// meter/macos_no_nap.go
//go:build darwin
func DisableAppNap() {
    cmd := exec.Command("caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
    cmd.Start()
}
```

Soak harness calls `DisableAppNap()` at startup; pins to performance cores via `taskpolicy -c utility -p`.

## 5. Burst driver

```go
// tests/soak/burst.go
func RunBurst(ctx context.Context, target, steady float64) error {
    baseline := readRSS(); time.Sleep(30*time.Second)
    workload.Set(steady * 10)         // 10× steady-state
    time.Sleep(5*time.Minute)
    workload.Set(steady)
    deadline := time.Now().Add(60 * time.Second)
    for time.Now().Before(deadline) {
        if math.Abs(readRSS()-baseline)/baseline <= 0.10 {
            return nil
        }
        time.Sleep(5*time.Second)
    }
    return fmt.Errorf("RSS did not return to ±10%% within 60s")
}
```

## 6. Goroutine / asyncio task export

### Go

```go
// shared/instrumentation/go/expvar.go
import "expvar"
import "runtime"

func init() {
    expvar.Publish("goroutines", expvar.Func(func() any { return runtime.NumGoroutine() }))
    expvar.Publish("memstats_pause_total_ns", expvar.Func(func() any {
        var m runtime.MemStats; runtime.ReadMemStats(&m); return m.PauseTotalNs
    }))
}
```

Exposed via `/debug/vars` on each Go service's admin port (read-only, bound to localhost / authenticated; never public).

### Python

```python
# shared/instrumentation/py/asyncio_metrics.py
from prometheus_client import Gauge
import asyncio

ASYNCIO_TASKS = Gauge("asyncio_tasks_pending", "Pending asyncio tasks")

async def emit_loop():
    while True:
        ASYNCIO_TASKS.set(len(asyncio.all_tasks()))
        await asyncio.sleep(5)
```

## 7. Per-service envelopes

Codified in `tests/soak/envelopes.yaml` (loaded by harness):

```yaml
api:
  idle_rss_max_mib: 80
  steady_rss_max_mib: 250
  steady_qps: 200
  slope_max_mib_per_h: 1.0

streaming:
  idle_rss_max_mib: 100
  steady_rss_max_mib: 300
  parent_only: true
  steady_concurrent_transcodes: 8
  slope_max_mib_per_h: 1.0

pipeline:
  idle_rss_max_mib: 600
  steady_overhead_mib: 500
  per_model_rss_mib: 4500
  slope_max_mib_per_h: 1.0
```

## 8. Pipeline shared-page accounting (EC2)

`/proc/<pid>/smaps_rollup` reports PSS (proportional set size), which divides shared pages by sharing count — the correct accounting for multiprocess workers. On macOS we approximate via `task_info` `phys_footprint` minus the shared-region pages exposed by `task_vm_info`.

Workers are launched via `multiprocessing.Process`. Harness sums PSS across the parent + children, asserts ≤ `idle_rss + transcribe_count × per_model_rss + 500 MiB`.

## 9. Test cases

### TC1 — 24-hour soak
`tests/soak/soak_test.go` (build tag `soak`) — runs `main` with `--hours=24 --service=api`. Slope assert < 1 MiB/h. CI nightly only.

### TC2 — Burst
`tests/soak/burst_test.go` — 5 min at 10× steady; assert RSS returns to ±10 % within 60 s.

### TC3 — Goroutine leak
`tests/streaming/leak_test.go` — `for i := 0; i < 1000; i++ { open(); close() }`; record `runtime.NumGoroutine()` before/after; assert delta ≤ 50.

### Unit tests
- `meter/slope_test.go`: known-leak fixture (10 MiB/h) detected; flat fixture passes.
- `meter/rss_*_test.go`: smoke against current process.

## 10. Edge cases

| Case | Source | Handling |
|---|---|---|
| EC1 CGO heap invisible to Go | story | Use OS PSS; documented in `meter/rss_linux.go` comment. |
| EC2 PSS shared pages | story | `smaps_rollup` PSS (Linux); `phys_footprint - shared_text` (macOS). |
| EC3 macOS App Nap | story | `caffeinate` and `taskpolicy` invoked at harness start. |
| Service crashes mid-soak | impl | Harness restarts up to 3× with `RestartCount` metric; > 3 fails. |
| Clock jump during sample | impl | Use `time.Now()` from `clock_monotonic`; reject samples with backwards delta. |

## 11. CI integration

- Burst + goroutine-leak run on every PR (~10 min).
- 24-h soak runs nightly on dedicated `mac-m2-8gb-soak` and `linux-x86-16gb-soak` self-hosted runners; failure pages on-call.

## 12. Dependencies

- Story 18.1 (budgets file extended with `envelopes:` section).
- Story 21.2 (metrics surface for goroutines/asyncio tasks).
- Epic 6 job-queue (workload generators).
