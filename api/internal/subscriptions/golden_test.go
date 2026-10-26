package subscriptions

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"
)

// The cloud's golden vector (cloud/internal/license/license_test.go):
// a license and a revocation list the cloud signed with a fixed key.
// The home must verify both byte for byte, or every cloud license
// breaks.
func TestGoldenVectorFromTheCloud(t *testing.T) {
	pub := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32)).Public().(ed25519.PublicKey)
	v := &Verifier{PublicKey: pub}
	at := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	lic := &License{License: LicenseInner{LicenseID: "golden-1", Tier: TierHome, Seats: 4, IssuedAt: at,
		ExpiresAt: at.Add(31 * 24 * time.Hour), Features: []Feature{"x"}, ServerID: "srv-g", Holder: "Golden"},
		Signature: "XkvL6lOqeqE4uZL4XTk2Dd7bOWWyByQGfbmhqusXGh7S907qu/A5JQ1WHAyVS+OwWYlNobZUbiHKOjms82J/CA=="}
	if _, err := v.Verify(lic, at); err != nil {
		t.Fatalf("the cloud's golden license does not verify: %v", err)
	}
	list := &RevocationList{Revocations: RevocationsInner{IssuedAt: at, LicenseIDs: []string{"a", "b"}},
		Signature: "Y6KHXQI01jiQoeYi9MtE6NO1m/aQn4lGjzqFyPshkEncsP3TayfH7OIT4wHx7qG4srhT1XwLf3YrJgI3iFa8DA=="}
	if err := v.VerifyRevocations(list); err != nil {
		t.Fatalf("the cloud's golden revocation list does not verify: %v", err)
	}
}
