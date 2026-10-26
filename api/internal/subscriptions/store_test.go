package subscriptions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"
)

// statefulDB is fakeLicenseDB plus the slot-0155 state row.
type statefulDB struct {
	fakeLicenseDB
	state *persistedState
}

func (f *statefulDB) SaveState(_ context.Context, st persistedState) error {
	cp := st
	f.state = &cp
	return nil
}

func (f *statefulDB) LoadState(_ context.Context) (*persistedState, error) {
	if f.state == nil {
		return nil, nil
	}
	cp := *f.state
	return &cp, nil
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func applyLicense(t *testing.T, s *Store, v *Verifier, priv ed25519.PrivateKey, inner LicenseInner) {
	t.Helper()
	lic, err := Sign(priv, inner)
	if err != nil {
		t.Fatal(err)
	}
	ent, err := v.Verify(lic, s.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetLicense(context.Background(), lic, ent); err != nil {
		t.Fatal(err)
	}
}

// Story 16.2 EC: an expired license keeps working for 72 h (clock skew),
// then the home is on the free tier; Settings still sees the license.
func TestStore_ExpiryGraceThenFree(t *testing.T) {
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := NewStore()
	s.SetClock(func() time.Time { return now })
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "l1", Tier: TierHome, IssuedAt: now, ExpiresAt: now.Add(24 * time.Hour)})

	if st := s.State(); st.Status != StatusActive || !s.Allows(FeatureMultiUser) {
		t.Fatalf("fresh license: status %q, multi_user %v", st.Status, s.Allows(FeatureMultiUser))
	}
	now = now.Add(24*time.Hour + time.Hour)
	if st := s.State(); st.Status != StatusGrace || s.SeatLimit() != 4 {
		t.Fatalf("1 h past expiry: status %q seats %d, want grace and 4", st.Status, s.SeatLimit())
	}
	now = now.Add(SkewGrace)
	st := s.State()
	if st.Status != StatusExpired || s.Current() != nil || s.SeatLimit() != 1 {
		t.Fatalf("past the grace: status %q current %v seats %d, want expired, nil, 1", st.Status, s.Current(), s.SeatLimit())
	}
	if st.License == nil || st.License.LicenseID != "l1" {
		t.Fatal("the expired license must stay on record for Settings")
	}
}

// Story 16.4 EC: a clock set back cannot revive an expired license; the
// license server's time wins when it is known.
func TestStore_ClockSetBackDoesNotRevive(t *testing.T) {
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	db := &statefulDB{}
	s, err := NewPersistentStore(context.Background(), db, v)
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return now })
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "l1", Tier: TierPro, IssuedAt: now, ExpiresAt: now.Add(10 * 24 * time.Hour)})

	now = now.Add(20 * 24 * time.Hour) // past expiry and grace
	if err := s.RecordRefresh(context.Background(), nil); err != nil {
		t.Fatal(err) // persists the high-water mark
	}
	if s.Current() != nil {
		t.Fatal("expired license still grants")
	}
	now = now.Add(-15 * 24 * time.Hour) // the clock goes back inside the validity
	if s.Current() != nil {
		t.Fatal("a clock set back revived an expired license")
	}
	// and across a restart (the high-water mark is persisted)
	s2, err := NewPersistentStore(context.Background(), db, v)
	if err != nil {
		t.Fatal(err)
	}
	s2.SetClock(func() time.Time { return now })
	if s2.Current() != nil {
		t.Fatal("a restart with the clock set back revived an expired license")
	}
	// the license server's time (inside the validity) wins when known
	s2.ObserveServerTime(now)
	if s2.Current() == nil {
		t.Fatal("the license server's time should be trusted over the high-water mark")
	}
}

// Story 16.4: a revoked id locks at once; taken off the list it works
// again; the list survives a restart and is used while offline.
func TestStore_RevocationList(t *testing.T) {
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	now := time.Now().UTC()
	db := &statefulDB{}
	s, err := NewPersistentStore(context.Background(), db, v)
	if err != nil {
		t.Fatal(err)
	}
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "lic-9", Tier: TierHome, IssuedAt: now, ExpiresAt: now.Add(365 * 24 * time.Hour)})

	setList := func(ids ...string) {
		list, err := SignRevocations(priv, RevocationsInner{IssuedAt: now, LicenseIDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.VerifyRevocations(list); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(list)
		if err := s.SetRevocations(context.Background(), list, string(raw), now); err != nil {
			t.Fatal(err)
		}
	}
	setList("other")
	if s.Current() == nil {
		t.Fatal("a list without this id must not lock")
	}
	setList("lic-9")
	if st := s.State(); st.Status != StatusRevoked || s.Current() != nil || st.LockReason != LockRevoked {
		t.Fatalf("revoked: status %q lock %q", st.Status, st.LockReason)
	}
	s2, err := NewPersistentStore(context.Background(), db, v)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Current() != nil {
		t.Fatal("the last-known revocation list must hold across a restart")
	}
	setList()
	if s.Current() == nil {
		t.Fatal("taken off the list, the license works again")
	}
}

// A tampered revocation list is refused (it could unrevoke a license).
func TestVerifyRevocations_Tampered(t *testing.T) {
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	list, err := SignRevocations(priv, RevocationsInner{IssuedAt: time.Now(), LicenseIDs: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	list.Revocations.LicenseIDs = nil
	if err := v.VerifyRevocations(list); err == nil {
		t.Fatal("tampered list verified")
	}
}

// A cloud-locked license (Lock) grants nothing and survives a restart;
// applying a license clears the lock.
func TestStore_LockAndReapply(t *testing.T) {
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	now := time.Now().UTC()
	db := &statefulDB{}
	s, _ := NewPersistentStore(context.Background(), db, v)
	inner := LicenseInner{LicenseID: "c1", Tier: TierHome, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour), ServerID: "srv"}
	applyLicense(t, s, v, priv, inner)
	if err := s.Lock(context.Background(), LockRevoked); err != nil {
		t.Fatal(err)
	}
	s2, _ := NewPersistentStore(context.Background(), db, v)
	if s2.Current() != nil || s2.State().Status != StatusRevoked {
		t.Fatal("a lock must survive a restart")
	}
	inner.LicenseID = "c2"
	applyLicense(t, s2, v, priv, inner)
	if s2.Current() == nil {
		t.Fatal("a new license clears the lock")
	}
}

// Story 16.1 EC: removing a license returns to free with nothing lost.
func TestStore_ExpiredRecoveredWithoutError(t *testing.T) {
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	now := time.Now().UTC()
	db := &statefulDB{}
	db.rows = append(db.rows, signedRow(t, priv, LicenseInner{
		LicenseID: "old", Tier: TierHome, IssuedAt: now.Add(-60 * 24 * time.Hour), ExpiresAt: now.Add(-30 * 24 * time.Hour),
	}))
	s, err := NewPersistentStore(context.Background(), db, v)
	if err != nil {
		t.Fatalf("an expired but genuine license is not a recovery error: %v", err)
	}
	if s.Current() != nil || s.State().Status != StatusExpired {
		t.Fatalf("expired license: current %v status %q", s.Current(), s.State().Status)
	}
}
