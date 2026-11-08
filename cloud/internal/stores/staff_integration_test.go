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

// Story 50.33: a staff account enrols once, its TOTP steps are good once, its mfa_token
// takes five attempts from one address, its lockout counts failures, and a rotated cookie
// presented again past the grace ends every session.
func TestStaffStore(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	s := &Staff{DB: pool.DB}
	now := time.Now().UTC().Truncate(time.Second)
	email := "ops-" + uuid.NewString()[:8] + "@example.test"

	a, err := s.Create(ctx, " "+email+" ", []string{StaffRoleServiceAdmin})
	if err != nil || a.Email != email || !a.HasRole(StaffRoleServiceAdmin) || a.Active() {
		t.Fatalf("create: %+v %v", a, err)
	}
	if _, err := s.Create(ctx, email, nil); !errors.Is(err, ErrStaffExists) {
		t.Fatalf("a second account for the email: %v", err)
	}

	// enrolment: a link, a TOTP secret, then finish once
	link := staffauth.TokenHash("link-" + email)
	if err := s.NewEnrolment(ctx, a.ID, link, now.Add(staffauth.EnrolTTL)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Enrolment(ctx, link, now); err != nil || got.ID != a.ID {
		t.Fatalf("enrolment lookup: %+v %v", got, err)
	}
	if _, err := s.Enrolment(ctx, link, now.Add(staffauth.EnrolTTL)); !errors.Is(err, ErrStaffTokenGone) {
		t.Errorf("an expired link: %v", err)
	}
	if err := s.FinishEnrolment(ctx, link, a.ID, "hash", []string{"h1"}, now); err == nil {
		t.Fatal("enrolment finished without a TOTP factor")
	}
	if err := s.PutTOTP(ctx, a.ID, "sealed-1"); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Methods(ctx, a.ID); len(m) != 0 {
		t.Errorf("an unconfirmed secret is a method: %v", m)
	}
	if err := s.FinishEnrolment(ctx, link, a.ID, "pwhash", []string{"h1", "h2"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishEnrolment(ctx, link, a.ID, "pwhash", nil, now); !errors.Is(err, ErrStaffTokenGone) {
		t.Errorf("the link worked twice: %v", err)
	}
	a, _ = s.ByEmail(ctx, email)
	if !a.Active() || a.PasswordHash != "pwhash" {
		t.Fatalf("after enrolment: %+v", a)
	}
	if m, _ := s.Methods(ctx, a.ID); len(m) != 2 || m[0] != "totp" || m[1] != "recovery" {
		t.Errorf("methods: %v", m)
	}
	// a confirmed secret can't be swapped by a second PutTOTP
	_ = s.PutTOTP(ctx, a.ID, "sealed-2")
	if sealed, _, ok, _ := s.TOTP(ctx, a.ID); sealed != "sealed-1" || !ok {
		t.Errorf("the confirmed secret was replaced: %q %v", sealed, ok)
	}

	// a TOTP step is used once, and never an older one
	if ok, _ := s.UseTOTPStep(ctx, a.ID, 100); !ok {
		t.Fatal("step 100")
	}
	for _, step := range []int64{100, 99} {
		if ok, _ := s.UseTOTPStep(ctx, a.ID, step); ok {
			t.Errorf("step %d after 100 was accepted", step)
		}
	}

	// recovery codes: each spent once
	codes, _ := s.UnusedRecoveryCodes(ctx, a.ID)
	if len(codes) != 2 {
		t.Fatalf("recovery codes: %v", codes)
	}
	if ok, _ := s.SpendRecoveryCode(ctx, codes[0].ID, now); !ok {
		t.Fatal("spend")
	}
	if ok, _ := s.SpendRecoveryCode(ctx, codes[0].ID, now); ok {
		t.Error("a code was spent twice")
	}

	// the mfa_token: five attempts, one address, five minutes
	mt := staffauth.TokenHash("mfa-" + email)
	_ = s.NewMFAToken(ctx, mt, a.ID, "198.51.100.7", now.Add(staffauth.MFATokenTTL))
	if _, err := s.TakeMFAAttempt(ctx, mt, "203.0.113.9", now); !errors.Is(err, ErrStaffTokenGone) {
		t.Errorf("another address: %v", err)
	}
	for i := range staffauth.MFAMaxAttempts {
		if id, err := s.TakeMFAAttempt(ctx, mt, "198.51.100.7", now); err != nil || id != a.ID {
			t.Fatalf("attempt %d: %q %v", i+1, id, err)
		}
	}
	if _, err := s.TakeMFAAttempt(ctx, mt, "198.51.100.7", now); !errors.Is(err, ErrStaffTokenGone) {
		t.Errorf("a sixth attempt: %v", err)
	}

	// lockout: the fifth failure in the window locks
	for i := range staffauth.LockFailures {
		locked, err := s.RecordFailure(ctx, a.ID, now.Add(time.Duration(i)*time.Second))
		if err != nil || locked != (i == staffauth.LockFailures-1) {
			t.Fatalf("failure %d: locked %v %v", i+1, locked, err)
		}
	}
	a, _ = s.ByID(ctx, a.ID)
	if !a.LockedUntil.After(now) {
		t.Errorf("not locked: %v", a.LockedUntil)
	}
	_ = s.Unlock(ctx, a.ID)
	if a, _ = s.ByID(ctx, a.ID); a.LockedUntil.After(now) || len(a.FailedAt) != 0 {
		t.Errorf("after unlock: %v %v", a.LockedUntil, a.FailedAt)
	}

	// sessions: rotate; the old cookie within the grace is a lost race, past it a replay
	x, err := s.NewSession(ctx, a.ID, "c1", []string{"pwd", "otp"}, "198.51.100.7", "ua", now)
	if err != nil || x.AuthTime != x.SignedInAt {
		t.Fatalf("session: %+v %v", x, err)
	}
	if err := s.Rotate(ctx, x.ID, "c1", "c2", now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionByCookie(ctx, "c2", now, 10*time.Second); err != nil || got.ID != x.ID {
		t.Fatalf("the new cookie: %v", err)
	}
	if _, err := s.SessionByCookie(ctx, "c1", now.Add(5*time.Second), 10*time.Second); !errors.Is(err, ErrStaffRotatedRecently) {
		t.Errorf("the old cookie within the grace: %v", err)
	}
	other, _ := s.NewSession(ctx, a.ID, "d1", []string{"pwd", "otp"}, "", "", now)
	if _, err := s.SessionByCookie(ctx, "c1", now.Add(time.Minute), 10*time.Second); !errors.Is(err, ErrStaffReplay) {
		t.Fatalf("the old cookie past the grace: %v", err)
	}
	for _, id := range []string{x.ID, other.ID} {
		if _, err := s.Session(ctx, id); !errors.Is(err, ErrStaffSessionEnd) {
			t.Errorf("session %s outlived the replay", id)
		}
	}

	// reset-mfa: no factors, not enrolled, no sessions
	y, _ := s.NewSession(ctx, a.ID, "e1", []string{"pwd", "otp"}, "", "", now)
	if err := s.ResetMFA(ctx, a.ID, now); err != nil {
		t.Fatal(err)
	}
	a, _ = s.ByID(ctx, a.ID)
	m, _ := s.Methods(ctx, a.ID)
	if a.Active() || len(m) != 0 {
		t.Errorf("after reset-mfa: active %v, methods %v", a.Active(), m)
	}
	if _, err := s.Session(ctx, y.ID); !errors.Is(err, ErrStaffSessionEnd) {
		t.Error("a session outlived reset-mfa")
	}
}
