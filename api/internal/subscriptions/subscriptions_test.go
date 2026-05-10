package subscriptions

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func newKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestPremiumLicenseUnlocksFeatures(t *testing.T) {
	pub, priv := newKeys(t)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	lic, err := Sign(priv, LicenseInner{
		LicenseID: "lic-1",
		Tier:      TierPremium,
		Seats:     5,
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
	if !ent.Allows(FeatureCloudRelay) {
		t.Fatal("cloud_relay should be on for premium")
	}
	if !ent.Allows(FeatureMultiUser) {
		t.Fatal("multi_user should be on for premium")
	}
}

func TestExpiredLicenseRejected(t *testing.T) {
	pub, priv := newKeys(t)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	lic, _ := Sign(priv, LicenseInner{
		LicenseID: "old",
		Tier:      TierPremium,
		Seats:     1,
		IssuedAt:  now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(-time.Hour),
	})
	v := &Verifier{PublicKey: pub}
	if _, err := v.Verify(lic, now); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

func TestTamperedLicenseFailsSignature(t *testing.T) {
	pub, priv := newKeys(t)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	lic, _ := Sign(priv, LicenseInner{
		LicenseID: "lic",
		Tier:      TierPremium,
		Seats:     1,
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	})
	lic.License.Seats = 9999
	v := &Verifier{PublicKey: pub}
	if _, err := v.Verify(lic, now); err == nil {
		t.Fatal("tampered license should fail verification")
	}
}

func TestFreeTierWithNilStoreDeniesPremium(t *testing.T) {
	s := NewStore()
	if s.Allows(FeatureCloudRelay) {
		t.Fatal("free tier should not allow cloud_relay")
	}
	if s.Current() != nil {
		t.Fatal("free tier should have nil current")
	}
}

func TestStoreSetAndGet(t *testing.T) {
	s := NewStore()
	s.Set(&Entitlements{
		Tier:     TierPremium,
		Features: map[Feature]bool{FeatureCloudBackup: true},
	})
	if !s.Allows(FeatureCloudBackup) {
		t.Fatal("Allows should follow current entitlement")
	}
	s.Set(nil)
	if s.Allows(FeatureCloudBackup) {
		t.Fatal("Set(nil) should revert to free tier")
	}
}
