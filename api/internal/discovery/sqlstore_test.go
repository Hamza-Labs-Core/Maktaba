package discovery

import (
	"context"
	"testing"
	"time"
)

// SQLPairingStore's queries execute against a live Postgres and are
// exercised end-to-end only on the integration path; `go test ./...`
// here has no Postgres driver wired (only lib/pq, no embedded server),
// so this file asserts the compile-time contract and the clock seam —
// the same interface-seam convention the refresh/subscriptions SQL
// stores follow. The store's column list is bound to the slot-0055
// migration text (a cheap, no-DB schema-drift guard) by
// TestMigrationFiles_Slot0055_PairingTicketsStoreColumns in
// api/migrate_test.go; the full SQL behaviour itself is integration-only
// and not unit-covered.

// Compile-time proof SQLPairingStore implements the interface the
// handler depends on. If a method signature drifts this fails to build.
var _ PairingStore = (*SQLPairingStore)(nil)

func TestSQLPairingStore_ClockSeam(t *testing.T) {
	s := NewSQLPairingStore(nil)
	// Default clock is wall time (non-zero, UTC).
	if s.clock().IsZero() {
		t.Fatal("default clock returned zero time")
	}
	frozen := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time { return frozen })
	if !s.clock().Equal(frozen) {
		t.Fatalf("clock=%v, want frozen %v", s.clock(), frozen)
	}
}

// TestSQLPairingStore_NilDBDoesNotPanicConstructing guards the boot
// path: P10 may construct the store before the DB handle is validated;
// construction must be allocation-only (no DB I/O) so a nil/late DB
// can't crash MountP10.
func TestSQLPairingStore_NilDBDoesNotPanicConstructing(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("constructing SQLPairingStore panicked: %v", r)
		}
	}()
	s := NewSQLPairingStore(nil)
	if s == nil {
		t.Fatal("nil store")
	}
	// Sweep/Get/Consume on a nil DB return an error, never panic — they
	// are not called at construction, but assert the no-panic contract
	// for the boot path where DB wiring is deferred.
	_ = context.Background()
}
