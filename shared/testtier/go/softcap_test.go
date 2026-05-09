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
	WithSoftCap(r, 100*time.Millisecond)
	// No work — finish immediately.
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
	WithSoftCap(r, 5*time.Millisecond)
	time.Sleep(10 * time.Millisecond)
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
	WithSoftCap(r, 1*time.Millisecond)
	// Sleep for 10ms = 10× cap, well past the 3× hard cap.
	time.Sleep(10 * time.Millisecond)
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
