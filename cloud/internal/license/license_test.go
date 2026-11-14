package license

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"
)

// The golden vector the home's verifier pins too
// (api/internal/subscriptions/golden_test.go): the same key, payload and
// signature in both modules, so the cloud's signer and the home's
// verifier cannot drift apart.
const (
	goldenLicenseSig     = "XkvL6lOqeqE4uZL4XTk2Dd7bOWWyByQGfbmhqusXGh7S907qu/A5JQ1WHAyVS+OwWYlNobZUbiHKOjms82J/CA=="
	goldenRevocationsSig = "Y6KHXQI01jiQoeYi9MtE6NO1m/aQn4lGjzqFyPshkEncsP3TayfH7OIT4wHx7qG4srhT1XwLf3YrJgI3iFa8DA=="
)

func goldenKey() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32)) }

var goldenAt = time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)

func TestGoldenVector(t *testing.T) {
	l, err := Sign(goldenKey(), Inner{LicenseID: "golden-1", Tier: TierHome, Seats: 4, IssuedAt: goldenAt,
		ExpiresAt: goldenAt.Add(PlanLicenseTTL), Features: []string{"x"}, ServerID: "srv-g", Holder: "Golden"})
	if err != nil || l.Signature != goldenLicenseSig {
		t.Fatalf("license signature changed: %s (%v); the home's verifier pins %s", l.Signature, err, goldenLicenseSig)
	}
	r, err := SignRevocations(goldenKey(), RevocationsInner{IssuedAt: goldenAt, LicenseIDs: []string{"a", "b"}})
	if err != nil || r.Signature != goldenRevocationsSig {
		t.Fatalf("revocation list signature changed: %s (%v)", r.Signature, err)
	}
}

func TestTierForPlan(t *testing.T) {
	for plan, want := range map[string]string{"free": "", "basic": TierHome, "pro": TierPro, "family": "", "bogus": ""} {
		if got := TierForPlan(plan); got != want {
			t.Errorf("%s: %q want %q", plan, got, want)
		}
	}
}

// A day's plan licenses are identical (the home changes nothing), and
// one lasts PlanLicenseTTL from the start of the day.
func TestForPlanStableWithinADay(t *testing.T) {
	a, _ := ForPlan(goldenKey(), "s1", TierPro, "o@x", goldenAt.Add(time.Hour))
	b, _ := ForPlan(goldenKey(), "s1", TierPro, "o@x", goldenAt.Add(20*time.Hour))
	if a.Signature != b.Signature || a.License.LicenseID != "cloud-s1" || !a.License.ExpiresAt.Equal(goldenAt.Add(PlanLicenseTTL)) {
		t.Fatalf("%+v / %+v", a, b)
	}
}

// Story 50.20: the broadcast flag is a feature the plan adds, for Pro and Pro with Broadcaster.
func TestBroadcastFeature(t *testing.T) {
	l, err := ForPlan(goldenKey(), "s1", TierPro, "o@x", goldenAt, FeatureBroadcast)
	if err != nil || len(l.License.Features) != 1 || l.License.Features[0] != "broadcast" {
		t.Fatalf("%+v %v", l.License, err)
	}
	if l, _ := ForPlan(goldenKey(), "s1", TierPro, "o@x", goldenAt); l.License.Features != nil {
		t.Fatalf("no features: %+v", l.License.Features)
	}
	for _, c := range []struct {
		plan, bp string
		want     bool
	}{{"free", "", false}, {"basic", "", false}, {"basic", "broadcaster", false}, {"pro", "", true}, {"pro", "broadcaster", true}} {
		if got := BroadcastIncluded(c.plan, c.bp); got != c.want {
			t.Errorf("%s + %q: %v", c.plan, c.bp, got)
		}
	}
}
