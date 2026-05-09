package testtier

import "time"

// Tier names. Kept as exported strings so test-budget tooling, lint
// rules, and future tier-aware reporters can reference the canonical
// list instead of typing "unit"/"integration"/"e2e" everywhere.
const (
	TierUnit        = "unit"
	TierIntegration = "integration"
	TierE2E         = "e2e"
	TierPerfCI      = "perf-ci"
)

// Per-test soft caps. AC4 of Story 20.1: a test that exceeds its soft
// cap emits a WARN; >3× the soft cap fails the build.
const (
	UnitSoftCap        = 100 * time.Millisecond
	IntegrationSoftCap = 5 * time.Second
	E2ESoftCap         = 30 * time.Second
)

// Per-tier wall-clock budgets. The budget enforcer in
// tools/test-budget reads these via the build script, so changing a
// number here changes both the test-time soft cap and the CI budget
// gate.
//
// `UnitTotalBudget` is intentionally per-package (every Go package
// gets its own test binary under `go test ./...`); the integration
// and e2e budgets are wall-clock totals because those tiers are run
// as one suite.
const (
	UnitPerPackageBudget   = 30 * time.Second
	IntegrationTotalBudget = 2 * time.Minute
	E2ETotalBudget         = 5 * time.Minute
	PerfCITotalBudget      = 2 * time.Minute
)

// HardCapMultiplier is the per-test failure threshold above the soft
// cap (AC4: "> 3× the soft cap fails the build").
const HardCapMultiplier = 3
