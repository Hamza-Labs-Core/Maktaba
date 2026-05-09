# Maktaba — Test Pyramid & Runtime Budgets

Reference for [Story 20.1](../specs/epics/20-testing/story-20-01-test-pyramid.md).
Covers tier definitions, conventions, soft caps, and how the budget
enforcer is wired into CI.

## Tiers

| Tier | What runs | Allowed I/O | Per-tier wall budget |
|---|---|---|---|
| **unit** | Pure logic, no I/O. | None — sockets blocked, FS limited to `t.TempDir`. | ≤ 30 s **per Go package**, ≤ ~30 s total per language. |
| **integration** | Real Postgres, real Chroma, real FFmpeg, fixture media. | All in-cluster services. | ≤ 2 min total wall-clock. |
| **e2e** | Playwright against the compose stack. | Full stack. | ≤ 5 min total wall-clock. |
| **perf-ci** | Reduced perf regression suite (Epic 20.7). | Full stack. | ≤ 2 min total wall-clock. |

Total PR green-to-merge wall-clock target: ≤ 18 min, well inside the
20-min budget from Story 22.1.

## Tagging conventions (AC3)

| Runtime | Unit | Integration | E2E |
|---|---|---|---|
| Go | default tag, runs under `go test -short` | `//go:build integration` | (n/a — owned by Playwright/Python) |
| Python | `@pytest.mark.unit` | `@pytest.mark.integration` | `@pytest.mark.e2e` |
| TypeScript | `*.unit.spec.ts` | `*.int.spec.ts` | `*.e2e.spec.ts` |

The Go convention uses `-short` (the standard library's
`testing.Short()` knob) for the unit tier rather than a build tag —
fewer test files need a header line, and `go test ./...` continues to
run every test for ad-hoc debugging.

## Per-test soft caps (AC4)

A test that exceeds its tier's soft cap **warns**; >3× the cap **fails**:

| Tier | Soft cap | Hard cap |
|---|---|---|
| unit | 100 ms | 300 ms |
| integration | 5 s | 15 s |
| e2e | 30 s | 90 s |

The cap values are exported from three places that must stay in sync:

- `shared/testtier/go/tier.go` — Go constants (`UnitSoftCap`, …).
- `shared/testtier/py/maktaba_testtier/tiers.py` — Python dict
  (`TIER_SOFT_CAPS`).
- `shared/testtier/ts/{vitest.unit.config.ts,playwright.config.ts}`
  — TS literals.

Change one, change all three.

### Wiring caps into a test

#### Go

```go
import testtier "github.com/Hamza-Labs-Core/Maktaba/shared/testtier/go"

func TestThing(t *testing.T) {
    testtier.WithUnitSoftCap(t)   // or WithIntegrationSoftCap / WithE2ESoftCap
    // ...
}
```

#### Python

The plugin loaded from `pipeline/tests/conftest.py` enforces caps
automatically based on the test's tier marker. No per-test code
required.

#### TypeScript

Vitest reports breaches via `slowTestThreshold`; Playwright via
`timeout` and `expect.timeout`. Per-test overrides go through
`test.slow()` or `test.setTimeout()`.

## Tier helpers

The `shared/testtier` toolkit exports the canonical tier names,
budgets, and soft caps to every runtime. Tests should import these
constants instead of hard-coding numbers.

### Unit-tier I/O guards (AC1)

* **Go** — `testtier.EnableUnitNetGuard()` from a `TestMain` swaps
  `net.DefaultResolver.Dial` for a stub that returns
  `testtier.ErrUnitNetGuard`.
* **Python** — the autouse `unit_netguard` fixture (loaded by
  `pipeline/tests/conftest.py`) replaces `socket.socket` with a
  raising stub for any `@pytest.mark.unit` test.
* **TypeScript** — `shared/testtier/ts/netguard.ts`, registered via
  `vitest.unit.config.ts :: setupFiles`, throws on `fetch` and
  `node:net.Socket`.

### Tmp-dir leak sweep (EC3)

`testtier.AssertNoTmpLeaks(out, "/tmp/maktaba-*", code)` from a
`TestMain` reports any working dirs that integration code dropped
outside of `t.TempDir`. Pytest-side tests use `tmp_path` and rely on
pytest's auto-cleanup.

## Budget enforcer

`tools/test-budget/` is a small Go program that consumes
`go test -json` and asserts both the per-package budget and the
per-test hard cap. Wrapped by `tools/test-budget.sh`:

```bash
# Unit tier — per-package budget + per-test hard cap.
GO_MODULES="api streaming shared/log/go shared/testtier/go" \
    bash tools/test-budget.sh unit

# Wall-clock enforcement for the other tiers.
bash tools/test-budget.sh wall integration 2m -- make test-integration-inner
bash tools/test-budget.sh wall e2e         5m -- make test-e2e-inner
bash tools/test-budget.sh wall perf-ci     2m -- make perf-ci-inner
```

The Makefile targets `make test-unit`, `make test-integration`,
`make test-e2e`, and `make perf-ci` all funnel through this script
so CI and local runs share the same budget assertions. A breach
emits a `::error::` annotation that GitHub Actions promotes to a
visible failure.

## CI matrix

The GitHub Actions matrix at `.github/workflows/ci.yml` runs the
four tiers in parallel jobs. The test-budget enforcer fails the job
on a soft-cap or wall-clock breach, so a regression doesn't silently
push the merge gate over its 20-minute budget.

## See also

- Story [20.2](../specs/epics/20-testing/) — test fixtures + seed data.
- Story [20.4](../specs/epics/20-testing/) — real backends in the
  integration tier.
- Story [20.5](../specs/epics/20-testing/) — golden e2e flows.
- Story [20.7](../specs/epics/20-testing/) — perf regression CI.
- Story [20.8](../specs/epics/20-testing/) — flake quarantine policy.
