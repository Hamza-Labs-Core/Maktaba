package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGenerateCodeShape(t *testing.T) {
	for i := 0; i < 16; i++ {
		c, err := GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		if len(c) != 9 || c[4] != '-' {
			t.Fatalf("unexpected shape %q", c)
		}
	}
}

func TestNormalizeCode(t *testing.T) {
	got := NormalizeCode("abcd-1234")
	if got != "ABCD1234" {
		t.Fatalf("normalize: got %q", got)
	}
	if NormalizeCode(" a b c d 1 2 3 4 ") != "ABCD1234" {
		t.Fatal("whitespace handling broken")
	}
}

func TestMemoryPairingStoreConsumeFlow(t *testing.T) {
	store := NewMemoryPairingStore()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	ticket := PairingTicket{
		Code:      "ABCD-1234",
		UserID:    "u1",
		IssuedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
	}
	if err := store.Put(context.Background(), ticket); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), "ABCD-1234")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != "u1" {
		t.Fatalf("user mismatch %q", got.UserID)
	}

	if _, err := store.Consume(context.Background(), "ABCD-1234"); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if _, err := store.Consume(context.Background(), "ABCD-1234"); !errors.Is(err, ErrCodeConsumed) {
		t.Fatalf("expected ErrCodeConsumed, got %v", err)
	}
}

func TestMemoryPairingStoreExpiry(t *testing.T) {
	store := NewMemoryPairingStore()
	current := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return current }

	_ = store.Put(context.Background(), PairingTicket{
		Code:      "EXP",
		IssuedAt:  current,
		ExpiresAt: current.Add(time.Minute),
	})
	current = current.Add(2 * time.Minute)
	if _, err := store.Get(context.Background(), "EXP"); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("expected expired, got %v", err)
	}
}

// TestSweepSelectionLogic is the Story 15.6 reaper regression. Sweep
// selects the union of two index-aligned predicates that
// SQLPairingStore.Sweep splits across two DELETEs (so the planner can
// use the slot-0055 partial index pairing_tickets_reaper for the
// growth-dominant unconsumed-expired case):
//
//   - unconsumed AND expired before the cutoff  → reaped
//   - consumed before the cutoff                → reaped
//   - everything else (live, recently expired,
//     recently consumed)                        → retained
//
// MemoryPairingStore mirrors that union exactly; this test pins it so a
// regression on either store's selection fails loudly. Validated without
// an embedded Postgres (the refresh/subscriptions interface-seam
// convention). The SQL column list is bound to the slot-0055 migration
// text by TestMigrationFiles_Slot0055_PairingTicketsStoreColumns in
// migrate_test.go.
func TestSweepSelectionLogic(t *testing.T) {
	store := NewMemoryPairingStore()
	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	consumedOld := base.Add(-8 * 24 * time.Hour) // past 7d horizon
	consumedNew := base.Add(-1 * time.Hour)      // inside retention

	// STALE: unconsumed, expired 8 days ago — past retention → reaped via
	// the consumed_at-IS-NULL partial-index-aligned predicate.
	_ = store.Put(context.Background(), PairingTicket{
		Code: "STALE", UserID: "u1",
		IssuedAt: base.Add(-9 * 24 * time.Hour), ExpiresAt: base.Add(-8 * 24 * time.Hour),
	})
	// RECENT: unconsumed, expired only 1 h ago — still inside retention,
	// must survive (the spec's expire-flip-before-7d-hard-delete).
	_ = store.Put(context.Background(), PairingTicket{
		Code: "RECENT", UserID: "u2",
		IssuedAt: base.Add(-2 * time.Hour), ExpiresAt: base.Add(-1 * time.Hour),
	})
	// LIVE: unconsumed, not yet expired — must survive.
	_ = store.Put(context.Background(), PairingTicket{
		Code: "LIVE", UserID: "u3",
		IssuedAt: base, ExpiresAt: base.Add(5 * time.Minute),
	})
	// CONSUMED_STALE: redeemed 8 days ago — past retention → reaped via
	// the consumed_at predicate (note its expires_at is still in the
	// future, proving the reaper keys on consumed_at, not expires_at,
	// for already-consumed rows).
	_ = store.Put(context.Background(), PairingTicket{
		Code: "CONSUMED_STALE", UserID: "u4",
		IssuedAt: base.Add(-9 * 24 * time.Hour), ExpiresAt: base.Add(10 * time.Minute),
		ConsumedAt: &consumedOld,
	})
	// CONSUMED_RECENT: redeemed 1 h ago — inside retention, must survive.
	_ = store.Put(context.Background(), PairingTicket{
		Code: "CONSUMED_RECENT", UserID: "u5",
		IssuedAt: base.Add(-2 * time.Hour), ExpiresAt: base.Add(10 * time.Minute),
		ConsumedAt: &consumedNew,
	})

	cutoff := base.Add(-7 * 24 * time.Hour) // mirrors main.pairingRetention horizon
	n, err := store.Sweep(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 2 {
		t.Fatalf("Sweep removed %d rows, want 2 (STALE + CONSUMED_STALE)", n)
	}

	store.now = func() time.Time { return base }
	if _, err := store.Get(context.Background(), "STALE"); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("STALE should be hard-deleted, got %v", err)
	}
	if _, err := store.Get(context.Background(), "CONSUMED_STALE"); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("CONSUMED_STALE should be hard-deleted, got %v", err)
	}
	// RECENT is expired-but-retained: present in the table, surfaces a
	// precise expired status (the spec's expire-flip-before-delete).
	if _, err := store.Get(context.Background(), "RECENT"); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("RECENT should be retained+expired, got %v", err)
	}
	if _, err := store.Get(context.Background(), "LIVE"); err != nil {
		t.Fatalf("LIVE should survive untouched, got %v", err)
	}
	if _, err := store.Get(context.Background(), "CONSUMED_RECENT"); err != nil {
		t.Fatalf("CONSUMED_RECENT should survive (inside retention), got %v", err)
	}
}

// TestSweepInterfaceContract proves both PairingStore implementations
// satisfy the Sweep seam the boot reaper (main.runPairingSweep) drives.
// A signature drift on either fails the build here.
func TestSweepInterfaceContract(_ *testing.T) {
	var _ PairingStore = (*MemoryPairingStore)(nil)
	var _ PairingStore = (*SQLPairingStore)(nil)
}

func TestNoopPublisherCapturesService(t *testing.T) {
	p := &NoopPublisher{}
	svc := Service{
		Instance: "Maktaba @ test",
		Domain:   "local.",
		Port:     8080,
		TXT:      map[string]string{"version": "1"},
	}
	if err := p.Publish(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	last := p.Last()
	if last == nil || !strings.Contains(last.Instance, "Maktaba") {
		t.Fatalf("missing publish: %+v", last)
	}
}
