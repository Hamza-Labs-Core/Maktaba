# Implementation Plan — Story 20.1 Test Pyramid & Runtime Budgets

> Companion to [story-20-01-test-pyramid.md](story-20-01-test-pyramid.md).
> Three tiers (unit / integration / e2e), per-tier budgets, per-test soft caps,
> and lint enforcement.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Tagging | Go `//go:build unit`, `//go:build integration`, `//go:build e2e`. Python `pytest.mark.unit/integration/e2e`. TS `*.unit.spec.ts`, `*.int.spec.ts`, `*.e2e.spec.ts`. |
| Per-tier runner | Make targets `make test-unit`, `make test-integration`, `make test-e2e`; CI matrix runs them in parallel. |
| Soft cap warning | Custom test reporter emits `WARN` at 100 ms unit / 5 s int / 30 s e2e. Hard fail at 3×. |
| I/O isolation | Go: net dialer hook in unit tier rejects all dials; Python: `socket.socket = forbidden`; TS: vitest `setupFiles` patches `fetch`/`net`. |
| Out of scope | Specific test content (per-feature stories own that). |

## 1. Project layout

```
shared/testtier/
├── go/
│   ├── tier_unit.go              # //go:build unit
│   ├── tier_int.go               # //go:build integration
│   ├── softcap.go                # reporter
│   └── netguard.go               # forbids dials in unit
├── py/
│   ├── conftest_unit.py          # imported by unit conftests
│   └── netguard.py
└── ts/
    ├── vitest.unit.config.ts
    ├── vitest.int.config.ts
    ├── playwright.config.ts
    └── netguard.ts

Makefile
.github/workflows/test.yml
```

## 2. Go tier guards

```go
//go:build unit
// shared/testtier/go/tier_unit.go
package testtier

import (
    "context"
    "fmt"
    "net"
    "testing"
)

func init() { SetUnitDialerGuard() }

func SetUnitDialerGuard() {
    net.DefaultResolver.PreferGo = true
    net.DefaultResolver.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
        return nil, fmt.Errorf("unit tests must not do I/O: dial %s/%s blocked", network, address)
    }
}

// Soft-cap reporter
func WithSoftCap(t *testing.T, cap time.Duration) {
    t.Helper()
    start := time.Now()
    t.Cleanup(func() {
        dur := time.Since(start)
        if dur > 3*cap { t.Fatalf("test took %s > 3× soft cap %s", dur, cap) }
        if dur > cap { t.Logf("WARN: test took %s > soft cap %s", dur, cap) }
    })
}
```

Used as:

```go
func TestThing(t *testing.T) {
    testtier.WithSoftCap(t, 100*time.Millisecond)
    ...
}
```

A vet check (`make lint:tier`) ensures every test file under tier `unit` calls `WithSoftCap`.

## 3. Python tier guards

```python
# shared/testtier/py/conftest_unit.py
import socket, pytest

class _ForbiddenSocket:
    def __init__(self, *a, **k): raise RuntimeError("unit tests must not open sockets")

@pytest.fixture(autouse=True, scope="session")
def _forbid_sockets():
    real = socket.socket
    socket.socket = _ForbiddenSocket
    yield
    socket.socket = real

def pytest_runtest_call(item):
    if item.get_closest_marker("unit") and item.duration > 0.3:
        item.warn(pytest.PytestWarning(f"unit test {item.nodeid} took {item.duration:.2f}s"))
```

Soft-cap fail:

```python
# pyproject.toml
[tool.pytest.ini_options]
markers = ["unit", "integration", "e2e"]
```

Per-mark hard timeouts via `pytest-timeout`:

```ini
[tool.pytest.ini_options]
timeout = 0
[tool.pytest.ini_options.tier_caps]
unit = 0.3
integration = 15
e2e = 90
```

## 4. TS guards

```ts
// shared/testtier/ts/vitest.unit.config.ts
import { defineConfig } from 'vitest/config';
export default defineConfig({
    test: {
        include: ['**/*.unit.spec.ts'],
        setupFiles: ['shared/testtier/ts/netguard.ts'],
        testTimeout: 300,
        slowTestThreshold: 100,
    },
});
```

```ts
// shared/testtier/ts/netguard.ts
const blocked = (..._: any[]) => { throw new Error('unit tests must not use fetch/network'); };
globalThis.fetch = blocked as any;
import('node:net').then(net => { (net as any).Socket = class { constructor(){ throw new Error('blocked'); } }; });
```

## 5. CI matrix

```yaml
# .github/workflows/test.yml
name: test
on: [push, pull_request]
jobs:
  unit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: make test-unit
    timeout-minutes: 3

  integration:
    runs-on: ubuntu-latest
    services:
      docker: { image: docker:dind }
    steps:
      - uses: actions/checkout@v4
      - run: make test-integration
    timeout-minutes: 12

  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: make test-e2e
    timeout-minutes: 18
```

PR green-to-merge wall-clock = `max(unit, integration, e2e)` ≤ 18 min, well inside 20 min budget.

## 6. Make targets

```makefile
.PHONY: test test-unit test-integration test-e2e

test: test-unit test-integration test-e2e

test-unit:
	go test -tags=unit -timeout=60s ./...
	pytest -m unit -q --timeout=15
	pnpm --recursive vitest run --config shared/testtier/ts/vitest.unit.config.ts

test-integration:
	go test -tags=integration -timeout=8m ./...
	pytest -m integration -q --timeout=300
	pnpm --recursive vitest run --config shared/testtier/ts/vitest.int.config.ts

test-e2e:
	docker compose -f deploy/compose/test.yml up -d
	pnpm --filter web e2e
	docker compose -f deploy/compose/test.yml down
```

## 7. testcontainers timeout (EC1)

```go
// shared/testtier/go/containers.go
//go:build integration
func startPostgres(t *testing.T) testcontainers.Container {
    t.Helper()
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    var c testcontainers.Container
    var err error
    for i := 0; i < 2; i++ {                                  // one retry
        c, err = postgres.RunContainer(ctx, postgres.WithImage("postgres:16"))
        if err == nil { return c }
    }
    t.Fatalf("postgres testcontainer failed: %v", err)
    return nil
}
```

Failure is recorded as `flake_category=container_start` (Story 20.8); does not count toward test failure budgets.

## 8. macOS-only tagging (EC2)

```go
//go:build darwin
package mlx

func TestMLXBackend(t *testing.T) { /* runs only on darwin */ }
```

```python
@pytest.mark.skipif(sys.platform != "darwin", reason="MLX requires macOS")
def test_mlx_backend(): ...
```

CI Linux job logs skip count; nightly mac runner exercises them.

## 9. Tmp dir cleanup (EC3)

`t.TempDir()` (Go) and `tmp_path` (pytest) auto-clean. End-of-run sweep:

```go
// shared/testtier/go/tmp_clean.go
//go:build integration
func TestMain(m *testing.M) {
    code := m.Run()
    leftover := globTmp("/tmp/maktaba-*")
    if len(leftover) > 0 {
        fmt.Fprintf(os.Stderr, "leak: %d tmp dirs left: %v\n", len(leftover), leftover)
        if code == 0 { code = 1 }
    }
    os.Exit(code)
}
```

## 10. Test cases

### TC1 — Tier compliance
`shared/testtier/go/tier_test.go` (build tag `unit`):

```go
//go:build unit
func TestUnitCannotDial(t *testing.T) {
    _, err := net.Dial("tcp", "127.0.0.1:1")
    require.Error(t, err)
    require.Contains(t, err.Error(), "unit tests must not do I/O")
}
```

### TC2 — Runtime breach
Test with `time.Sleep(2 * 100ms)` runs under `unit`; assert reporter logs WARN. With `time.Sleep(4 * 100ms)`, assert it FAILs.

### TC3 — CI parallelism
Confirm via wall-clock measurement that the three matrix jobs run concurrently (job start times overlap). Slowest tier is integration.

## 11. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 testcontainers slow | story | 60s timeout + 1 retry; failure → flake category. |
| EC2 macOS-only | story | Build tag `darwin` and `skipif`. |
| EC3 tmp cleanup | story | `TestMain` leak sweep. |
| Test imports forbidden package | impl | `internal/` boundaries; vet rule. |
| Unit test pulls in DB driver init | impl | Init paths must be lazy; lint forbids `init()` outside `cmd/` (Story 20.3 EC3). |

## 12. Dependencies

- Story 20.2 (fixtures used by integration).
- Story 20.4 (real backends).
- Story 20.5 (e2e flows).
- Story 20.7 (perf-ci — separate matrix).
- Story 20.8 (flake registry).
