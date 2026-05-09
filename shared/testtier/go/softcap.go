package testtier

import (
	"testing"
	"time"
)

// WithSoftCap installs a t.Cleanup hook that measures the test's
// wall-clock duration and reports against the supplied soft cap.
//
// Behavior matches AC4 of Story 20.1:
//
//   - dur ≤ cap          → silent.
//   - cap < dur ≤ 3*cap  → t.Logf WARN (visible in `-v` output and CI
//     test reports, doesn't fail the run).
//   - dur > 3*cap        → t.Errorf, fails the test.
//
// The helper is intentionally lightweight — no goroutines, no
// timers — so it costs essentially nothing on the happy path.
//
// Tests opt in per-test rather than via package-level magic so the
// cap value lives next to the test it applies to and shows up in
// code review.
func WithSoftCap(t testing.TB, cap time.Duration) {
	t.Helper()
	start := time.Now()
	t.Cleanup(func() {
		dur := time.Since(start)
		switch {
		case dur > time.Duration(HardCapMultiplier)*cap:
			t.Errorf("test took %s > %dx soft cap %s (Story 20.1 AC4)",
				dur, HardCapMultiplier, cap)
		case dur > cap:
			t.Logf("WARN: test took %s > soft cap %s (Story 20.1 AC4)",
				dur, cap)
		}
	})
}

// WithUnitSoftCap is shorthand for WithSoftCap with the unit cap.
func WithUnitSoftCap(t testing.TB) { t.Helper(); WithSoftCap(t, UnitSoftCap) }

// WithIntegrationSoftCap is shorthand for WithSoftCap with the
// integration cap.
func WithIntegrationSoftCap(t testing.TB) {
	t.Helper()
	WithSoftCap(t, IntegrationSoftCap)
}

// WithE2ESoftCap is shorthand for WithSoftCap with the e2e cap.
func WithE2ESoftCap(t testing.TB) { t.Helper(); WithSoftCap(t, E2ESoftCap) }

// RequireUnit skips the test when the unit tier is not active.
//
// `go test -short` is how Maktaba's Makefile selects the unit tier.
// Tests that require unit-tier semantics (no I/O, no real DB) call
// this so they don't run when the integration tier sweeps `./...`
// without `-short`.
func RequireUnit(t testing.TB) {
	t.Helper()
	if !testing.Short() {
		t.Skip("skipping unit-only test (go test -short not set)")
	}
}

// RequireIntegration skips the test when the integration tier is not
// active. Test files that need real services typically combine this
// with `//go:build integration` so they're excluded from the unit
// build entirely; RequireIntegration is the runtime safety net for
// tests in mixed files.
func RequireIntegration(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration-only test (go test -short is set)")
	}
}
