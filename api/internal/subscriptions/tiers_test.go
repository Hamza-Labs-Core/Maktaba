package subscriptions

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

// Story 16.2 — the tier model is free/home/pro (NOT free/premium). The
// gap analysis (epic-16-subscriptions.md Story 16.2) pins this as a
// headline defect: code shipped free/premium; every Epic-16 story
// specifies free/home/pro with distinct quotas (seat caps, feature
// matrix). These tests are the executable spec for the corrected model.

func TestTierConstantsAreFreeHomePro(t *testing.T) {
	if TierFree != "free" {
		t.Fatalf("TierFree = %q, want free", TierFree)
	}
	if TierHome != "home" {
		t.Fatalf("TierHome = %q, want home", TierHome)
	}
	if TierPro != "pro" {
		t.Fatalf("TierPro = %q, want pro", TierPro)
	}
}

// Story 16.2 — home tier: 4 seats, relay, daily backup, basic
// analytics; NO federation (pro-only).
func TestHomeTierFeatureMatrix(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	lic, err := Sign(priv, LicenseInner{
		LicenseID: "home-1",
		Tier:      TierHome,
		Seats:     4,
		IssuedAt:  now,
		ExpiresAt: now.Add(365 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	v := &Verifier{PublicKey: pub}
	ent, err := v.Verify(lic, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ent.Tier != TierHome {
		t.Fatalf("tier = %q, want home", ent.Tier)
	}
	if !ent.Allows(FeatureCloudRelay) {
		t.Fatal("home: cloud_relay should be enabled")
	}
	if !ent.Allows(FeatureMultiUser) {
		t.Fatal("home: multi_user should be enabled (4 seats)")
	}
	if !ent.Allows(FeatureCloudBackup) {
		t.Fatal("home: cloud_metadata_backup should be enabled")
	}
	if !ent.Allows(FeatureAdvancedMetric) {
		t.Fatal("home: basic analytics (advanced_analytics flag) should be enabled")
	}
	if ent.Allows(FeatureFederation) {
		t.Fatal("home: federation is PRO-ONLY and must NOT be enabled")
	}
	if got := ent.SeatLimit(); got != 4 {
		t.Fatalf("home SeatLimit = %d, want 4", got)
	}
}

// Story 16.2 — pro tier: unlimited seats, relay, federation, hourly
// backup, advanced analytics.
func TestProTierFeatureMatrix(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	lic, err := Sign(priv, LicenseInner{
		LicenseID: "pro-1",
		Tier:      TierPro,
		Seats:     0, // 0 == unlimited for pro
		IssuedAt:  now,
		ExpiresAt: now.Add(365 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	v := &Verifier{PublicKey: pub}
	ent, err := v.Verify(lic, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, f := range []Feature{
		FeatureCloudRelay, FeatureFederation, FeatureMultiUser,
		FeatureAdvancedMetric, FeatureCloudBackup,
	} {
		if !ent.Allows(f) {
			t.Fatalf("pro: feature %q should be enabled", f)
		}
	}
	if got := ent.SeatLimit(); got != SeatsUnlimited {
		t.Fatalf("pro SeatLimit = %d, want unlimited(%d)", got, SeatsUnlimited)
	}
}

// Story 16.1 — free tier: NO premium feature, seat limit of 1
// (single-user). nil Entitlements == free.
func TestFreeTierSeatLimitAndNoPremium(t *testing.T) {
	var ent *Entitlements // nil == free
	if got := ent.SeatLimit(); got != 1 {
		t.Fatalf("free SeatLimit = %d, want 1 (single-user)", got)
	}
	for _, f := range []Feature{
		FeatureCloudRelay, FeatureFederation, FeatureMultiUser,
		FeatureAdvancedMetric, FeatureCloudBackup,
	} {
		if ent.Allows(f) {
			t.Fatalf("free: feature %q must NOT be enabled", f)
		}
	}
}

// The verifier must reject the legacy "premium" tier string and any
// unknown tier — the wire contract is strictly free/home/pro.
func TestVerifierRejectsUnknownTier(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	for _, bad := range []Tier{"premium", "enterprise", "", "FREE"} {
		lic, err := Sign(priv, LicenseInner{
			LicenseID: "x",
			Tier:      bad,
			Seats:     1,
			IssuedAt:  now,
			ExpiresAt: now.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		v := &Verifier{PublicKey: pub}
		if _, err := v.Verify(lic, now); err == nil {
			t.Fatalf("tier %q should be rejected by the verifier", bad)
		}
	}
}
