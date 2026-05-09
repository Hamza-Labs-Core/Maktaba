package testtier

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// TestUnitNetGuardBlocksDial is TC1 of Story 20.1: a unit test that
// asks the resolver to dial gets a clear "must not do I/O" error.
//
// We dial a name (not a literal IP) so the resolver is on the path —
// raw `net.Dial("tcp", "127.0.0.1:1")` bypasses DNS and isn't subject
// to the resolver hook. That's a known limitation of the guard and
// is documented here so future readers don't expect more from it.
func TestUnitNetGuardBlocksDial(t *testing.T) {
	EnableUnitNetGuard()
	t.Cleanup(DisableUnitNetGuard)

	d := net.Dialer{Timeout: 200 * time.Millisecond}
	_, err := d.Dial("tcp", "maktaba-not-a-real-host.invalid:1")
	if err == nil {
		t.Fatal("expected dial to fail under the unit netguard")
	}
	// We accept either the bare guard error (when the resolver is
	// the one that fails first) or any DNS-resolution error that
	// embeds it. The key signal is the substring; substring is the
	// stable part of the error contract.
	if !strings.Contains(err.Error(), "unit tests must not do I/O") &&
		!errors.Is(err, ErrUnitNetGuard) {
		t.Fatalf("err = %v; want substring %q or wrapped %v",
			err, "unit tests must not do I/O", ErrUnitNetGuard)
	}
}

// TestUnitNetGuardIdempotent: calling Enable twice is safe.
func TestUnitNetGuardIdempotent(t *testing.T) {
	EnableUnitNetGuard()
	EnableUnitNetGuard()
	t.Cleanup(DisableUnitNetGuard)
}
