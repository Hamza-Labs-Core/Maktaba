package subscriptions

import (
	"context"
	"testing"
	"time"
)

// Story 16.2, the owner's decision of 2026-11-19: when a license expires or the home
// downgrades, the paid features keep working fully for 30 days counted from the
// license's expiry, then lock. A revocation (fraud) locks at once.

func graceStore(t *testing.T, now *time.Time) (*Store, *Verifier, func(LicenseInner)) {
	t.Helper()
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	s, err := NewPersistentStore(context.Background(), &statefulDB{}, v)
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return *now })
	return s, v, func(in LicenseInner) { applyLicense(t, s, v, priv, in) }
}

func TestAnExpiredLicenseWorksFullyForThirtyDaysFromItsExpiry(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s, _, apply := graceStore(t, &now)
	exp := now.Add(10 * 24 * time.Hour)
	apply(LicenseInner{LicenseID: "pro-1", Tier: TierPro, IssuedAt: now, ExpiresAt: exp})

	for _, at := range []time.Duration{0, 24 * time.Hour, 29 * 24 * time.Hour, Grace - time.Minute} {
		now = exp.Add(at)
		st := s.State()
		if st.Status != StatusGrace || st.Effective == nil || st.Effective.Tier != TierPro {
			t.Fatalf("%s after expiry: %+v", at, st)
		}
		// fully: every feature and the seats, not read-only
		if !s.Allows(FeatureFederation) || s.SeatLimit() != SeatsUnlimited {
			t.Fatalf("%s after expiry: federation %v, seats %d", at, s.Allows(FeatureFederation), s.SeatLimit())
		}
		if !st.GraceUntil.Equal(exp.Add(Grace)) {
			t.Fatalf("grace until %s, want %s (counted from the expiry)", st.GraceUntil, exp.Add(Grace))
		}
	}
	now = exp.Add(Grace)
	if st := s.State(); st.Status != StatusExpired || st.Effective != nil || s.Allows(FeatureCloudRelay) || s.SeatLimit() != 1 {
		t.Fatalf("30 days after expiry: %+v", st)
	}
}

func TestADowngradeKeepsTheOldPlanFullyForThirtyDays(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s, _, apply := graceStore(t, &now)
	apply(LicenseInner{LicenseID: "pro-1", Tier: TierPro, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour)})

	now = now.Add(5 * 24 * time.Hour)
	downgradedAt := now
	apply(LicenseInner{LicenseID: "home-1", Tier: TierHome, Seats: 4, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour)})
	st := s.State()
	if st.Status != StatusActive || st.License.LicenseID != "home-1" {
		t.Fatalf("after the downgrade: %+v", st)
	}
	if !s.Allows(FeatureFederation) || s.SeatLimit() != SeatsUnlimited || s.TierName() != "pro" {
		t.Fatalf("the old plan's features after a downgrade: federation %v seats %d tier %s",
			s.Allows(FeatureFederation), s.SeatLimit(), s.TierName())
	}
	// replaced before its expiry: its 30 days count from the replacement
	if want := downgradedAt.Add(Grace); !st.GraceUntil.Equal(want) {
		t.Fatalf("grace until %s, want %s", st.GraceUntil, want)
	}
	if u, ok := s.Until(FeatureFederation); !ok || !u.Equal(downgradedAt.Add(Grace)) {
		t.Fatalf("Until(federation) = %s %v", u, ok)
	}
	if u, ok := s.Until(FeatureCloudRelay); !ok || !u.Equal(now.Add(31*24*time.Hour+Grace)) {
		t.Fatalf("Until(cloud_relay) = %s %v: the home license grants it longer", u, ok)
	}

	// the cloud's daily refresh replaces the home license with the next: the pro
	// license's grace is not lost
	for d := 1; d <= 3; d++ {
		now = downgradedAt.Add(time.Duration(d) * 24 * time.Hour)
		apply(LicenseInner{LicenseID: "home-" + string(rune('1'+d)), Tier: TierHome, Seats: 4, IssuedAt: now,
			ExpiresAt: now.Add(31 * 24 * time.Hour)})
	}
	if !s.Allows(FeatureFederation) {
		t.Fatal("a refresh after the downgrade ended the old plan's grace")
	}
	now = downgradedAt.Add(Grace)
	if s.Allows(FeatureFederation) || s.SeatLimit() != 4 || s.TierName() != "home" || !s.State().GraceUntil.IsZero() {
		t.Fatalf("30 days after the downgrade: federation %v seats %d tier %s grace %s",
			s.Allows(FeatureFederation), s.SeatLimit(), s.TierName(), s.State().GraceUntil)
	}
}

func TestAnUpgradeOrARenewalShowsNoGrace(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s, _, apply := graceStore(t, &now)
	apply(LicenseInner{LicenseID: "home-1", Tier: TierHome, Seats: 4, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour)})
	now = now.Add(24 * time.Hour)
	apply(LicenseInner{LicenseID: "pro-1", Tier: TierPro, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour)})
	if st := s.State(); st.Status != StatusActive || !st.GraceUntil.IsZero() || s.TierName() != "pro" {
		t.Fatalf("an upgrade: %+v", st)
	}
}

func TestARevocationLocksAtOnceWhateverTheGrace(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s, _, apply := graceStore(t, &now)
	apply(LicenseInner{LicenseID: "pro-1", Tier: TierPro, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour)})
	apply(LicenseInner{LicenseID: "home-1", Tier: TierHome, Seats: 4, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour)})
	if err := s.Lock(context.Background(), LockRevoked); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.Status != StatusRevoked || st.Effective != nil || s.Allows(FeatureFederation) {
		t.Fatalf("revoked: %+v", st)
	}
	// a replaced license on the revocation list adds nothing either
	s2, _, apply2 := graceStore(t, &now)
	apply2(LicenseInner{LicenseID: "pro-bad", Tier: TierPro, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour)})
	apply2(LicenseInner{LicenseID: "home-2", Tier: TierHome, Seats: 4, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour)})
	s2.mu.Lock()
	s2.revoked = map[string]bool{"pro-bad": true}
	s2.mu.Unlock()
	if s2.Allows(FeatureFederation) || s2.TierName() != "home" {
		t.Fatal("a revoked replaced license still grants its plan")
	}
}

func TestARemovedLicenseKeepsItsGraceAcrossARestart(t *testing.T) {
	ctx := context.Background()
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	db := &statefulDB{}
	s, _ := NewPersistentStore(ctx, db, v)
	now := time.Now().UTC()
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "pro-1", Tier: TierPro, IssuedAt: now.Add(-time.Hour),
		ExpiresAt: now.Add(31 * 24 * time.Hour)})
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "home-1", Tier: TierHome, Seats: 4, IssuedAt: now,
		ExpiresAt: now.Add(31 * 24 * time.Hour)})
	s2, err := NewPersistentStore(ctx, db, v)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Allows(FeatureFederation) || s2.State().GraceUntil.IsZero() {
		t.Fatalf("after a restart the downgrade's grace is gone: %+v", s2.State())
	}
}

// The cloud's plan license keeps its id from plan to plan (cloud-<server>): Pro → Home on
// the same id is a downgrade all the same, kept in its grace, across a restart too.
func TestACloudDowngradeOnTheSameIDKeepsItsGrace(t *testing.T) {
	ctx := context.Background()
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	db := &statefulDB{}
	s, _ := NewPersistentStore(ctx, db, v)
	now := time.Now().UTC()
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "cloud-srv-1", Tier: TierPro, IssuedAt: now.Add(-time.Hour),
		ExpiresAt: now.Add(31 * 24 * time.Hour), ServerID: "srv-1"})
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "cloud-srv-1", Tier: TierHome, Seats: 4, IssuedAt: now,
		ExpiresAt: now.Add(31 * 24 * time.Hour), ServerID: "srv-1"})
	if !s.Allows(FeatureFederation) || s.State().GraceUntil.IsZero() || s.State().Status != StatusActive {
		t.Fatalf("a same-id downgrade: %+v", s.State())
	}
	// the daily renewal of Home on the same id: nothing new is kept, the grace stands
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "cloud-srv-1", Tier: TierHome, Seats: 4, IssuedAt: now.Add(time.Minute),
		ExpiresAt: now.Add(31 * 24 * time.Hour), ServerID: "srv-1"})
	if !s.Allows(FeatureFederation) {
		t.Fatal("the renewal after a same-id downgrade ended its grace")
	}
	s2, err := NewPersistentStore(ctx, db, v)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Allows(FeatureFederation) || s2.TierName() != "pro" {
		t.Fatalf("after a restart the same-id downgrade's grace is gone: %+v", s2.State())
	}
}

// A license the cloud revoked (fraud) and then replaced, or removed, is no grace after a
// restart either: its row's end is not "now".
func TestARevokedLicenseReplacedIsNoGraceAfterARestart(t *testing.T) {
	ctx := context.Background()
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	now := time.Now().UTC()
	for _, then := range []string{"replaced", "removed"} {
		db := &statefulDB{}
		s, _ := NewPersistentStore(ctx, db, v)
		applyLicense(t, s, v, priv, LicenseInner{LicenseID: "pro-fraud", Tier: TierPro, IssuedAt: now.Add(-time.Hour),
			ExpiresAt: now.Add(31 * 24 * time.Hour)})
		if err := s.Lock(ctx, LockRevoked); err != nil {
			t.Fatal(err)
		}
		if then == "replaced" {
			applyLicense(t, s, v, priv, LicenseInner{LicenseID: "home-ok", Tier: TierHome, Seats: 4, IssuedAt: now,
				ExpiresAt: now.Add(31 * 24 * time.Hour)})
		} else if err := s.Revoke(ctx); err != nil {
			t.Fatal(err)
		}
		if s.Allows(FeatureFederation) {
			t.Fatalf("%s: the revoked license's plan is granted", then)
		}
		s2, _ := NewPersistentStore(ctx, db, v)
		if s2.Allows(FeatureFederation) {
			t.Fatalf("%s, then a restart: the revoked license's plan is granted again", then)
		}
	}
}
