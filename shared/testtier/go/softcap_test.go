package testtier

import (
	"strings"
	"testing"
	"time"
)

// recorder is a testing.TB stub that captures Logf / Errorf without
// failing the outer test. We exercise WithSoftCap against this so the
// failure path doesn't actually fail this test binary.
type recorder struct {
	testing.TB
	logs    []string
	errors  []string
	cleanup func()
}

func (r *recorder) Helper()                          {}
func (r *recorder) Logf(f string, a ...any)          { r.logs = append(r.logs, fmtSafe(f, a...)) }
func (r *recorder) Errorf(f string, a ...any)        { r.errors = append(r.errors, fmtSafe(f, a...)) }
func (r *recorder) Cleanup(fn func())                { r.cleanup = fn }
func (r *recorder) Skip(args ...any)                 {}
func (r *recorder) Skipf(format string, args ...any) {}
func (r *recorder) SkipNow()                         {}
func (r *recorder) Skipped() bool                    { return false }

// clockAt is a clock that reads start, then start+took on every later read: a test that
// took exactly took, whatever the runner's load.
func clockAt(took time.Duration) func() time.Time {
	start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	calls := 0
	return func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(took)
	}
}

func fmtSafe(format string, args ...any) string {
	// Avoid pulling fmt into this tiny helper just to format; the
	// tests only check substrings, so a sloppy concat is fine.
	parts := []string{format}
	for _, a := range args {
		parts = append(parts, "·")
		_ = a
	}
	return strings.Join(parts, " ")
}

// TestSoftCapSilentBelowCap: a fast test produces no warning and no
// error.
func TestSoftCapSilentBelowCap(t *testing.T) {
	r := &recorder{}
	withSoftCap(r, 100*time.Millisecond, clockAt(100*time.Millisecond)) // at the cap: silent
	r.cleanup()
	if len(r.logs) != 0 {
		t.Fatalf("expected no logs, got %v", r.logs)
	}
	if len(r.errors) != 0 {
		t.Fatalf("expected no errors, got %v", r.errors)
	}
}

// TestSoftCapWarnsAboveCap: TC2 (slow but not >3×).
func TestSoftCapWarnsAboveCap(t *testing.T) {
	r := &recorder{}
	withSoftCap(r, 5*time.Millisecond, clockAt(10*time.Millisecond)) // 2x the cap
	r.cleanup()
	if len(r.logs) == 0 {
		t.Fatalf("expected a WARN log, got none")
	}
	if len(r.errors) != 0 {
		t.Fatalf("expected no errors, got %v", r.errors)
	}
}

// TestSoftCapFailsBeyondHardCap: TC2 hard-fail variant.
func TestSoftCapFailsBeyondHardCap(t *testing.T) {
	r := &recorder{}
	withSoftCap(r, 1*time.Millisecond, clockAt(10*time.Millisecond)) // 10x: past the 3x hard cap
	r.cleanup()
	if len(r.errors) == 0 {
		t.Fatalf("expected a hard-cap Errorf, got none")
	}
}

// TestRequireUnitSkipsWhenNotShort: the helper Skips when the binary
// was launched without -short.
func TestRequireUnitSkipsWhenNotShort(t *testing.T) {
	if testing.Short() {
		t.Skip("RequireUnit's no-skip branch is exercised in short mode by other tests")
	}
	// We can't usefully assert "Skip was called" without recursive
	// machinery; instead verify the function returns without
	// panicking when called against a no-op TB.
	r := &recorder{}
	RequireUnit(r)
}

// WithSoftCap itself measures on the real clock: a test that does nothing stays silent.
func TestWithSoftCapRealClock(t *testing.T) {
	r := &recorder{}
	WithSoftCap(r, time.Minute)
	r.cleanup()
	if len(r.logs)+len(r.errors) != 0 {
		t.Fatalf("logs %v errors %v", r.logs, r.errors)
	}
}

// Exactly 3x the cap still only warns; past it fails.
func TestSoftCapHardCapEdge(t *testing.T) {
	r := &recorder{}
	withSoftCap(r, 5*time.Millisecond, clockAt(15*time.Millisecond))
	r.cleanup()
	if len(r.logs) != 1 || len(r.errors) != 0 {
		t.Fatalf("at 3x: logs %v errors %v", r.logs, r.errors)
	}
	r = &recorder{}
	withSoftCap(r, 5*time.Millisecond, clockAt(15*time.Millisecond+time.Nanosecond))
	r.cleanup()
	if len(r.errors) != 1 {
		t.Fatalf("past 3x: errors %v", r.errors)
	}
}
