package subscriptions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

// fakeLicenseDB is an in-process LicensePersistence stand-in. It mimics
// the `licenses` table semantics (slot 0056): at most one active
// (revoked_at IS NULL) row at a time. Mirrors the interface-seam test
// convention used by authz (mockLib/mockOwner) and the documented
// idempotency MemoryStore pattern — the real Postgres schema is
// exercised by the integration tier.
type fakeLicenseDB struct {
	rows []persistedLicense // append-only history; newest active wins
}

func (f *fakeLicenseDB) SaveActive(_ context.Context, rec persistedLicense) error {
	// Revoke any currently-active row (one-active invariant).
	now := time.Now().UTC()
	for i := range f.rows {
		if f.rows[i].RevokedAt == nil {
			f.rows[i].RevokedAt = &now
		}
	}
	rec.RevokedAt = nil
	f.rows = append(f.rows, rec)
	return nil
}

func (f *fakeLicenseDB) RevokeActive(_ context.Context) error {
	now := time.Now().UTC()
	for i := range f.rows {
		if f.rows[i].RevokedAt == nil {
			f.rows[i].RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeLicenseDB) LoadActive(_ context.Context) (*persistedLicense, error) {
	for i := len(f.rows) - 1; i >= 0; i-- {
		if f.rows[i].RevokedAt == nil {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, nil
}

func mintLicense(t *testing.T) (*License, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	lic, err := Sign(priv, LicenseInner{
		LicenseID: "lic-persist-1",
		Tier:      TierPremium,
		Seats:     7,
		IssuedAt:  now,
		ExpiresAt: now.Add(365 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return lic, pub
}

// TestPersistentStore_AppliedLicenseSurvivesRestart is the core gap
// fix: an applied license must come back after the process restarts.
// We model "restart" as a fresh Store constructed over the same
// backend.
func TestPersistentStore_AppliedLicenseSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	lic, pub := mintLicense(t)
	v := &Verifier{PublicKey: pub}
	ent, err := v.Verify(lic, time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	db := &fakeLicenseDB{}

	// First boot: apply the license.
	s1, err := NewPersistentStore(ctx, db, v)
	if err != nil {
		t.Fatalf("NewPersistentStore: %v", err)
	}
	if s1.Current() != nil {
		t.Fatal("fresh store should be free tier")
	}
	if err := s1.SetLicense(ctx, lic, ent); err != nil {
		t.Fatalf("SetLicense: %v", err)
	}
	if !s1.Allows(FeatureCloudRelay) {
		t.Fatal("premium feature should be on after apply")
	}

	// Restart: brand-new store over the SAME backend.
	s2, err := NewPersistentStore(ctx, db, v)
	if err != nil {
		t.Fatalf("NewPersistentStore (restart): %v", err)
	}
	cur := s2.Current()
	if cur == nil {
		t.Fatal("applied license vanished after restart")
	}
	if cur.LicenseID != "lic-persist-1" {
		t.Fatalf("license_id = %q after restart, want lic-persist-1", cur.LicenseID)
	}
	if cur.Tier != TierPremium || cur.Seats != 7 {
		t.Fatalf("entitlement not restored: tier=%q seats=%d", cur.Tier, cur.Seats)
	}
	if !s2.Allows(FeatureFederation) {
		t.Fatal("premium feature should still be on after restart")
	}
}

// TestPersistentStore_RevokeSurvivesRestart confirms a revoke is
// durable: after revoke + restart the instance is back to free tier.
func TestPersistentStore_RevokeSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	lic, pub := mintLicense(t)
	v := &Verifier{PublicKey: pub}
	ent, _ := v.Verify(lic, time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC))

	db := &fakeLicenseDB{}
	s1, err := NewPersistentStore(ctx, db, v)
	if err != nil {
		t.Fatalf("NewPersistentStore: %v", err)
	}
	if err := s1.SetLicense(ctx, lic, ent); err != nil {
		t.Fatalf("SetLicense: %v", err)
	}
	if err := s1.Revoke(ctx); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if s1.Current() != nil {
		t.Fatal("revoke should drop to free tier in-memory")
	}

	s2, err := NewPersistentStore(ctx, db, v)
	if err != nil {
		t.Fatalf("NewPersistentStore (restart): %v", err)
	}
	if s2.Current() != nil {
		t.Fatal("revoked license came back after restart")
	}
}

// TestPersistentStore_ReplaceKeepsOneActive verifies the
// one-active-license invariant (slot 0056 partial unique index): a
// second SetLicense replaces the first, and only the latest survives a
// restart.
func TestPersistentStore_ReplaceKeepsOneActive(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v := &Verifier{PublicKey: pub}
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

	mk := func(id string, seats int) (*License, *Entitlements) {
		lic, e := Sign(priv, LicenseInner{
			LicenseID: id, Tier: TierPremium, Seats: seats,
			IssuedAt: now, ExpiresAt: now.Add(365 * 24 * time.Hour),
		})
		if e != nil {
			t.Fatal(e)
		}
		ent, e := v.Verify(lic, now)
		if e != nil {
			t.Fatal(e)
		}
		return lic, ent
	}

	db := &fakeLicenseDB{}
	s1, err := NewPersistentStore(ctx, db, v)
	if err != nil {
		t.Fatalf("NewPersistentStore: %v", err)
	}
	licA, entA := mk("lic-A", 1)
	licB, entB := mk("lic-B", 9)
	if err := s1.SetLicense(ctx, licA, entA); err != nil {
		t.Fatalf("SetLicense A: %v", err)
	}
	if err := s1.SetLicense(ctx, licB, entB); err != nil {
		t.Fatalf("SetLicense B: %v", err)
	}

	s2, err := NewPersistentStore(ctx, db, v)
	if err != nil {
		t.Fatalf("NewPersistentStore (restart): %v", err)
	}
	cur := s2.Current()
	if cur == nil || cur.LicenseID != "lic-B" || cur.Seats != 9 {
		t.Fatalf("expected lic-B (the latest) to be the sole active license, got %+v", cur)
	}

	active := 0
	for _, r := range db.rows {
		if r.RevokedAt == nil {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("one-active invariant violated: %d active rows", active)
	}
}
