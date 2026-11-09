//go:build integration

package stores

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
)

// Story 50.33 part 2: a credential id is registered once; a challenge is taken once and not
// after it expires; a disabled passkey is no factor and can't be used; an enrolment finishes
// with a passkey and no TOTP code (dropping the unconfirmed secret) but never with neither;
// a passkey's first step reads the mfa_token without spending an attempt.
func TestStaffPasskeyStore(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	s := &Staff{DB: pool.DB}
	now := time.Now().UTC().Truncate(time.Second)
	a, err := s.Create(ctx, "keys-"+uuid.NewString()[:8]+"@example.test", []string{StaffRoleServiceAdmin})
	if err != nil {
		t.Fatal(err)
	}

	// an enrolment with neither factor is refused, whole
	link := staffauth.TokenHash("link-" + a.ID)
	if err := s.NewEnrolment(ctx, a.ID, link, now.Add(staffauth.EnrolTTL)); err != nil {
		t.Fatal(err)
	}
	_ = s.PutTOTP(ctx, a.ID, "sealed-unconfirmed")
	if err := s.FinishEnrolment(ctx, link, a.ID, "pw", false, []string{"h"}, now); !errors.Is(err, ErrStaffNoFactor) {
		t.Fatalf("no passkey, no code: %v", err)
	}
	if got, _ := s.ByID(ctx, a.ID); got.Active() {
		t.Fatal("a refused enrolment left the account enrolled")
	}

	// a passkey, once per credential id
	cred := []byte(`{"id":"AQID"}`)
	p, err := s.AddPasskey(ctx, a.ID, []byte{1, 2, 3}, []byte("pk"), 0, "Laptop", cred)
	if err != nil || p.ID == "" {
		t.Fatalf("add: %+v %v", p, err)
	}
	if _, err := s.AddPasskey(ctx, a.ID, []byte{1, 2, 3}, []byte("pk"), 0, "Again", cred); !errors.Is(err, ErrStaffPasskeyExists) {
		t.Errorf("the same credential twice: %v", err)
	}
	if err := s.FinishEnrolment(ctx, link, a.ID, "pw", false, []string{"h1"}, now); err != nil {
		t.Fatalf("enrolment with a passkey: %v", err)
	}
	if _, _, _, err := s.TOTP(ctx, a.ID); !errors.Is(err, ErrStaffNotFound) {
		t.Errorf("the unconfirmed TOTP secret outlived a passkey enrolment: %v", err)
	}
	if m, _ := s.Methods(ctx, a.ID); len(m) != 2 || m[0] != "passkey" || m[1] != "recovery" {
		t.Errorf("methods: %v", m)
	}
	if err := s.UsePasskey(ctx, a.ID, []byte{1, 2, 3}, 7, []byte(`{"id":"AQID","n":7}`), now); err != nil {
		t.Fatal(err)
	}
	keys, err := s.Passkeys(ctx, a.ID, false)
	if err != nil || len(keys) != 1 || keys[0].SignCount != 7 || !keys[0].LastUsedAt.Valid || keys[0].Name != "Laptop" {
		t.Fatalf("after use: %+v %v", keys, err)
	}

	// a challenge: replaced by a newer one of its purpose, taken once, never after it expires
	if err := s.PutChallenge(ctx, a.ID, WebAuthnLogin, []byte(`{"c":1}`), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutChallenge(ctx, a.ID, WebAuthnLogin, []byte(`{"c":2}`), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutChallenge(ctx, a.ID, WebAuthnReauth, []byte(`{"c":3}`), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.TakeChallenge(ctx, a.ID, WebAuthnLogin, now); err != nil || string(got) != `{"c": 2}` && string(got) != `{"c":2}` {
		t.Fatalf("take: %s %v", got, err)
	}
	if _, err := s.TakeChallenge(ctx, a.ID, WebAuthnLogin, now); !errors.Is(err, ErrStaffTokenGone) {
		t.Errorf("a challenge taken twice: %v", err)
	}
	if _, err := s.TakeChallenge(ctx, a.ID, WebAuthnReauth, now.Add(time.Minute)); !errors.Is(err, ErrStaffTokenGone) {
		t.Errorf("an expired challenge: %v", err)
	}

	// the mfa_token: read without an attempt, from its own address only
	tok := staffauth.TokenHash("mfa-" + a.ID)
	if err := s.NewMFAToken(ctx, tok, a.ID, "198.51.100.1", now.Add(staffauth.MFATokenTTL)); err != nil {
		t.Fatal(err)
	}
	for range staffauth.MFAMaxAttempts + 1 {
		if id, err := s.MFATokenAccount(ctx, tok, "198.51.100.1", now); err != nil || id != a.ID {
			t.Fatalf("read: %q %v", id, err)
		}
	}
	if _, err := s.MFATokenAccount(ctx, tok, "198.51.100.2", now); !errors.Is(err, ErrStaffTokenGone) {
		t.Errorf("another address read the token: %v", err)
	}
	if _, err := s.TakeMFAAttempt(ctx, tok, "198.51.100.1", now); err != nil {
		t.Errorf("reads spent the attempts: %v", err)
	}

	// disabled: no factor, no use
	if err := s.DisablePasskey(ctx, a.ID, []byte{1, 2, 3}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.UsePasskey(ctx, a.ID, []byte{1, 2, 3}, 8, cred, now); !errors.Is(err, ErrStaffNotFound) {
		t.Errorf("a disabled passkey was used: %v", err)
	}
	if m, _ := s.Methods(ctx, a.ID); len(m) != 0 {
		t.Errorf("methods with the passkey disabled: %v", m)
	}
	if all, _ := s.Passkeys(ctx, a.ID, true); len(all) != 1 || !all[0].Disabled {
		t.Errorf("listed with the disabled: %+v", all)
	}
}
