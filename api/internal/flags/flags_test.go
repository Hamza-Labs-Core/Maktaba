package flags

import (
	"errors"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/serverkeys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

func licensed(tier subscriptions.Tier, fs ...subscriptions.Feature) *subscriptions.Entitlements {
	e := &subscriptions.Entitlements{Tier: tier, Features: map[subscriptions.Feature]bool{}}
	for _, f := range fs {
		e.Features[f] = true
	}
	return e
}

var home = licensed(subscriptions.TierHome, subscriptions.FeatureCloudRelay, subscriptions.FeatureMultiUser,
	subscriptions.FeatureCloudBackup, subscriptions.FeatureAdvancedMetric)

func TestPaidFlagsAreTheLicenseAnswer(t *testing.T) {
	user := Subject{UserID: "u"}
	free := merge(user, nil, nil, nil)
	for _, d := range Declarations {
		if d.Feature != "" && free[d.Key] != false {
			t.Errorf("free tier: %s = %v, want false", d.Key, free[d.Key])
		}
	}
	got := merge(user, home, nil, nil)
	for _, d := range Declarations {
		if d.Feature == "" {
			continue
		}
		if want := home.Allows(d.Feature); got[d.Key] != want {
			t.Errorf("home tier: %s = %v, want %v (Entitlements.Allows)", d.Key, got[d.Key], want)
		}
	}
	if got[FlagFederation] != false {
		t.Error("home tier without federation in the license must not get federation")
	}
}

func TestOverridesApplyInScopeOrderAndNeverGrantAPaidFeature(t *testing.T) {
	user := Subject{UserID: "u"}
	rows := []Override{
		{FlagKey: FlagEarlyAccess, Scope: ScopeUser, Value: false},
		{FlagKey: FlagEarlyAccess, Scope: ScopeGlobal, Value: true},
		{FlagKey: FlagAdvancedAnalytics, Scope: ScopeTier, Value: false},
		{FlagKey: FlagFederation, Scope: ScopeUser, Value: true}, // not licensed: stays off
		{FlagKey: "made.up.key", Scope: ScopeGlobal, Value: true},
	}
	got := merge(user, home, nil, rows)
	if got[FlagEarlyAccess] != false {
		t.Error("a user override must win over a global one, whatever the row order")
	}
	if got[FlagAdvancedAnalytics] != false {
		t.Error("an override may turn a licensed feature off")
	}
	if got[FlagFederation] != false {
		t.Error("an override must never grant a feature the license does not")
	}
	if _, ok := got["made.up.key"]; ok {
		t.Error("an undeclared flag must stay out of the bundle")
	}
}

func TestSameScopeLatestWins(t *testing.T) {
	rows := []Override{ // oldest first, as the store returns them
		{FlagKey: FlagEarlyAccess, Scope: ScopeUser, Value: true},
		{FlagKey: FlagEarlyAccess, Scope: ScopeUser, Value: false},
	}
	if got := merge(Subject{UserID: "u"}, nil, nil, rows); got[FlagEarlyAccess] != false {
		t.Error("the most recently created override of a scope must win")
	}
}

func TestCohortMembershipAndAdminOnly(t *testing.T) {
	got := merge(Subject{UserID: "u"}, nil, []string{CohortEarlyAccess}, nil)
	if got[FlagEarlyAccess] != true {
		t.Error("an early_access member gets early_access")
	}
	if _, ok := got[FlagFlagsAdmin]; ok {
		t.Error("a regular user must not see an admin-only flag")
	}
	if got := merge(Subject{UserID: "a", IsAdmin: true}, nil, nil, nil); got[FlagFlagsAdmin] != true {
		t.Error("an admin sees the admin-only flag")
	}
}

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func testKeys(t *testing.T, c *clock) *serverkeys.Store {
	t.Helper()
	k, err := serverkeys.Generate(c.t, "generated")
	if err != nil {
		t.Fatal(err)
	}
	s, err := serverkeys.NewStore(k, time.Hour, c)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBundleSignatureDetectsTampering(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	keys := testKeys(t, c)
	b := Bundle{Flags: map[string]any{FlagCloudRelay: false, "x": "a<b&c"}, Tier: "free"}
	if err := b.sign(keys, c.t); err != nil {
		t.Fatal(err)
	}
	if b.Kid != keys.Active().Kid || b.ExpiresAt != "2026-10-26T12:01:00Z" {
		t.Fatalf("kid %q expires %q", b.Kid, b.ExpiresAt)
	}
	if err := Verify(&b, keys, c.t); err != nil {
		t.Fatalf("a fresh bundle must verify: %v", err)
	}
	tampered := b
	tampered.Flags = map[string]any{FlagCloudRelay: true, "x": "a<b&c"}
	if err := Verify(&tampered, keys, c.t); !errors.Is(err, serverkeys.ErrBadSig) {
		t.Fatalf("a rewritten cache (cloud_relay=true) must fail verification, got %v", err)
	}
	if err := Verify(&b, keys, c.t.Add(TTL)); !errors.Is(err, ErrExpired) {
		t.Fatalf("an expired bundle must be refused, got %v", err)
	}
	unsigned := b
	unsigned.Signature = ""
	if err := Verify(&unsigned, keys, c.t); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("an unsigned bundle must be refused, got %v", err)
	}
}

func TestBundleOldKidVerifiesOneOverlapThenFails(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	keys := testKeys(t, c)
	b := Bundle{Flags: map[string]any{}, Tier: "free"}
	if err := b.sign(keys, c.t); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Rotate(serverkeys.RotateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(&b, keys, c.t); err != nil {
		t.Fatalf("the previous kid must verify within the overlap: %v", err)
	}
	c.t = c.t.Add(2 * time.Hour) // past the overlap (and the bundle's own expiry)
	if err := Verify(&b, keys, c.t); !errors.Is(err, serverkeys.ErrUnknownKid) {
		t.Fatalf("after the overlap the old kid must fail, got %v", err)
	}
}
