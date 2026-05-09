package testtier

import (
	"context"
	"errors"
	"net"
	"sync"
)

// ErrUnitNetGuard is returned by the swapped-in resolver/dialer when
// a unit test attempts a network dial. The error string is asserted
// in TC1 of Story 20.1, so changes here must be mirrored in the
// tier_test.go assertions.
var ErrUnitNetGuard = errors.New("unit tests must not do I/O: network dial blocked (Story 20.1 AC1)")

var (
	netGuardMu      sync.Mutex
	netGuardEnabled bool
	savedPreferGo   bool
	savedStrictErr  bool
	savedDial       func(ctx context.Context, network, address string) (net.Conn, error)
)

// EnableUnitNetGuard installs a process-wide guard that blocks every
// network dial routed through net.DefaultResolver and returns
// ErrUnitNetGuard.
//
// Call this from a TestMain in unit-tier packages that should be
// I/O-free. It is safe to call repeatedly — the second call is a
// no-op until DisableUnitNetGuard runs.
//
// The guard is intentionally per-process (no per-test toggle) because
// the unit tier is supposed to be I/O-free for *every* test in the
// package; toggling per-test would let a stray network dial through
// from a fixture or init() func.
//
// We mutate net.DefaultResolver's fields in place rather than swap
// the pointer because net.DefaultResolver carries an internal
// singleflight mutex and copying it is a vet violation.
func EnableUnitNetGuard() {
	netGuardMu.Lock()
	defer netGuardMu.Unlock()
	if netGuardEnabled {
		return
	}
	r := net.DefaultResolver
	savedPreferGo = r.PreferGo
	savedStrictErr = r.StrictErrors
	savedDial = r.Dial
	r.PreferGo = true
	r.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, ErrUnitNetGuard
	}
	netGuardEnabled = true
}

// DisableUnitNetGuard restores the original resolver. Mostly useful
// in tests of the guard itself; production code should never need
// it.
func DisableUnitNetGuard() {
	netGuardMu.Lock()
	defer netGuardMu.Unlock()
	if !netGuardEnabled {
		return
	}
	r := net.DefaultResolver
	r.PreferGo = savedPreferGo
	r.StrictErrors = savedStrictErr
	r.Dial = savedDial
	netGuardEnabled = false
}
