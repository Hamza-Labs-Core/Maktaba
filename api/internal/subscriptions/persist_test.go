package subscriptions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// errLoadActiveDB is a LicensePersistence whose LoadActive always
// fails — modelling a DB-level read error on the licenses table.
type errLoadActiveDB struct{ *fakeLicenseDB }

func (errLoadActiveDB) LoadActive(_ context.Context) (*persistedLicense, error) {
	return nil, errors.New("db: connection reset")
}

// signedRow builds a `licenses` row by signing `inner` with `priv`.
// It mirrors how the production SaveActive serialises the row (the raw
// signed JSON in RawJWT), letting the fail-closed cases below feed
// NewPersistentStore a row the runtime verifier will reject.
func signedRow(t *testing.T, priv ed25519.PrivateKey, inner LicenseInner) persistedLicense {
	t.Helper()
	lic, err := Sign(priv, inner)
	if err != nil {
		t.Fatal(err)
	}
	// Serialise exactly as production SetLicense does (json.Marshal of
	// the signed *License) so recover()'s json.Unmarshal round-trips.
	raw, err := json.Marshal(lic)
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}
	return persistedLicense{
		LicenseID: inner.LicenseID,
		Tier:      inner.Tier,
		Seats:     inner.Seats,
		IssuedAt:  inner.IssuedAt,
		ExpiresAt: inner.ExpiresAt,
		RawJWT:    string(raw),
		Features:  inner.Features,
	}
}

// TestPersistentStore_FailsClosed pins the headline security guarantee
// (spec / HLB-287): a tampered, key-rotated, garbage, or expired
// persisted license — or a backend read error — must leave the store on
// the FREE tier, return a non-nil error, and never panic. It must never
// silently grant premium. These assertions are deliberately strict
// (Current() nil AND no premium feature) so they fail loudly if recover
// were ever made fail-OPEN.
func TestPersistentStore_FailsClosed(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

	// The key the *runtime* verifier trusts (build-time public key).
	rtPub, rtPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// A DIFFERENT key — the row "signed by a rotated/foreign key".
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	premiumInner := func(id string, expires time.Time) LicenseInner {
		return LicenseInner{
			LicenseID: id,
			Tier:      TierPremium,
			Seats:     5,
			IssuedAt:  now,
			ExpiresAt: expires,
		}
	}

	cases := []struct {
		name string
		db   LicensePersistence
		v    *Verifier
	}{
		{
			// (a) garbage RawJWT — JSON decode failure in recover().
			name: "garbage_raw_jwt",
			db: func() LicensePersistence {
				f := &fakeLicenseDB{}
				f.rows = append(f.rows, persistedLicense{
					LicenseID: "lic-garbage",
					Tier:      TierPremium,
					Seats:     5,
					IssuedAt:  now,
					ExpiresAt: now.Add(365 * 24 * time.Hour),
					RawJWT:    "{not-valid-json", // decode fails
				})
				return f
			}(),
			v: &Verifier{PublicKey: rtPub},
		},
		{
			// (a') empty RawJWT — also a JSON decode failure.
			name: "empty_raw_jwt",
			db: func() LicensePersistence {
				f := &fakeLicenseDB{}
				f.rows = append(f.rows, persistedLicense{
					LicenseID: "lic-empty",
					Tier:      TierPremium,
					Seats:     5,
					IssuedAt:  now,
					ExpiresAt: now.Add(365 * 24 * time.Hour),
					RawJWT:    "",
				})
				return f
			}(),
			v: &Verifier{PublicKey: rtPub},
		},
		{
			// (b) row signed by a foreign/rotated key — signature
			// mismatch against the runtime public key.
			name: "wrong_key_signature_mismatch",
			db: func() LicensePersistence {
				f := &fakeLicenseDB{}
				f.rows = append(f.rows,
					signedRow(t, otherPriv,
						premiumInner("lic-wrongkey", now.Add(365*24*time.Hour))))
				return f
			}(),
			v: &Verifier{PublicKey: rtPub},
		},
		{
			// (c) correctly-signed but EXPIRED — Verify -> ErrExpired.
			// Must degrade to free, NOT premium.
			name: "expired_license",
			db: func() LicensePersistence {
				f := &fakeLicenseDB{}
				f.rows = append(f.rows,
					signedRow(t, rtPriv,
						premiumInner("lic-expired", now.Add(-1*time.Hour))))
				return f
			}(),
			v: &Verifier{PublicKey: rtPub},
		},
		{
			// (d) LoadActive errors — NewPersistentStore returns error,
			// store still free, no panic, no premium.
			name: "load_active_error",
			db:   errLoadActiveDB{&fakeLicenseDB{}},
			v:    &Verifier{PublicKey: rtPub},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			var (
				s     *Store
				gotErr error
			)
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("NewPersistentStore panicked (fail-closed must never panic): %v", r)
					}
				}()
				s, gotErr = NewPersistentStore(ctx, tc.db, tc.v)
			}()

			if gotErr == nil {
				t.Fatalf("expected a non-nil recovery error, got nil (the failure was silently swallowed)")
			}
			if s == nil {
				// load_active_error returns (nil, err); the others return
				// a usable free-tier store. Either way there must be NO
				// premium — a nil store cannot grant anything, so this is
				// acceptable only for the LoadActive-error case.
				if tc.name != "load_active_error" {
					t.Fatalf("expected a usable free-tier store, got nil")
				}
				return
			}

			// The security assertion: free tier, never premium.
			if cur := s.Current(); cur != nil {
				t.Fatalf("FAIL-OPEN: store is on tier %q (entitlement %+v); a "+
					"tampered/rotated/expired/garbage license must degrade to "+
					"the FREE tier", cur.Tier, cur)
			}
			if s.Allows(FeatureCloudRelay) {
				t.Fatal("FAIL-OPEN: premium feature cloud_relay is enabled after a failed recovery")
			}
			if s.Allows(FeatureFederation) {
				t.Fatal("FAIL-OPEN: premium feature federation is enabled after a failed recovery")
			}
		})
	}
}

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
