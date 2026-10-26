//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
)

// Registration needs the mailed link before sign-in works; the response
// is the same whether or not the address is taken.
func TestRegisterVerifyLogin(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	const email = "alice@example.test"

	if r := c.do("POST", "/v1/auth/register", map[string]string{"email": "not-an-email", "password": goodPassword}); r.code != 400 || r.errCode() != "invalid_email" {
		t.Fatalf("bad email: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/v1/auth/register", map[string]string{"email": email, "password": "short"}); r.code != 400 || r.errCode() != "weak_password" {
		t.Fatalf("weak password: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/v1/auth/register", map[string]string{"email": email, "password": "password123"}); r.code != 400 || r.errCode() != "weak_password" {
		t.Fatalf("leaked password: %d %s", r.code, r.body)
	}
	r := c.mustStatus(c.do("POST", "/v1/auth/register", map[string]string{"email": email, "password": goodPassword, "display_name": "Alice"}), http.StatusAccepted)
	if r.json(t)["status"] != "pending_verification" {
		t.Fatalf("register body: %s", r.body)
	}
	if _, ok := c.cookies["maktaba_refresh"]; ok {
		t.Fatal("registration must not start a session before verification")
	}

	// Unverified: right password, 403 with the resend route.
	r = c.do("POST", "/v1/auth/login", map[string]string{"email": email, "password": goodPassword})
	if r.code != 403 || r.errCode() != "email_not_verified" || r.json(t)["resend_url"] != "/v1/auth/verify-email/resend" {
		t.Fatalf("unverified login: %d %s", r.code, r.body)
	}

	// Registering the same address again: same 202, and the owner is told.
	c2 := e.client()
	c2.mustStatus(c2.do("POST", "/v1/auth/register", map[string]string{"email": "ALICE@example.test", "password": goodPassword}), http.StatusAccepted)
	if _, ok := e.mail.Last("already_registered", email); !ok {
		t.Fatal("existing owner not notified of the duplicate sign-up")
	}

	// Resend issues a new link and retires the old one.
	old := e.mailedToken("verify_email", email)
	c.mustStatus(c.do("POST", "/v1/auth/verify-email/resend", map[string]string{"email": email}), http.StatusAccepted)
	fresh := e.mailedToken("verify_email", email)
	if old == fresh {
		t.Fatal("resend reused the token")
	}
	if r := c.do("POST", "/v1/auth/verify-email", map[string]string{"token": old}); r.code != 400 || r.errCode() != "invalid_token" {
		t.Fatalf("superseded token: %d %s", r.code, r.body)
	}
	c.mustStatus(c.do("POST", "/v1/auth/verify-email", map[string]string{"token": fresh}), http.StatusOK)
	if r := c.do("POST", "/v1/auth/verify-email", map[string]string{"token": fresh}); r.code != 400 {
		t.Fatalf("token reuse: %d", r.code)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'auth.email_verified'`) != 1 {
		t.Fatal("verification not audited")
	}

	// Wrong password and unknown email answer the same.
	if r := c.do("POST", "/v1/auth/login", map[string]string{"email": email, "password": "wrong password!"}); r.code != 401 || r.errCode() != "invalid_credentials" {
		t.Fatalf("wrong password: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/v1/auth/login", map[string]string{"email": "nobody@example.test", "password": goodPassword}); r.code != 401 || r.errCode() != "invalid_credentials" {
		t.Fatalf("unknown email: %d %s", r.code, r.body)
	}

	r = c.mustStatus(c.do("POST", "/v1/auth/login", map[string]string{"email": email, "password": goodPassword}), http.StatusOK)
	body := r.json(t)
	user := body["user"].(map[string]any)
	if user["role"] != "user" || user["email_verified"] != true || body["access_token"] == "" {
		t.Fatalf("login body: %s", r.body)
	}
	if c.cookies["maktaba_refresh"] == "" {
		t.Fatal("no refresh cookie")
	}
	c.token = body["access_token"].(string)
	me := c.mustStatus(c.do("GET", "/v1/account/me", nil), 200).json(t)
	if me["email"] != email || me["display_name"] != "Alice" || me["has_password"] != true {
		t.Fatalf("me: %v", me)
	}
	if r := e.client().do("GET", "/v1/account/me", nil); r.code != 401 {
		t.Fatalf("me without token: %d", r.code)
	}
}

// A refresh rotates the token; the rotated token coming back later is a
// replay and signs the account out everywhere.
func TestRefreshRotationAndReplay(t *testing.T) {
	e := newEnv(t)
	c := e.registerVerified("bob@example.test")
	first := c.cookies["maktaba_refresh"]

	c.mustStatus(c.do("POST", "/v1/auth/refresh", nil), 200)
	second := c.cookies["maktaba_refresh"]
	if second == "" || second == first {
		t.Fatal("refresh did not rotate the cookie")
	}

	// Inside the grace window the old token is a plain miss (two tabs
	// racing), not a replay.
	thief := e.client()
	thief.cookies["maktaba_refresh"] = first
	if r := thief.do("POST", "/v1/auth/refresh", nil); r.code != 401 {
		t.Fatalf("old token in grace: %d", r.code)
	}
	if e.count(`SELECT count(*) FROM sessions WHERE revoked_at IS NULL`) != 1 {
		t.Fatal("a grace-window miss must not revoke the live session")
	}

	// Past the grace window it is a replay.
	e.exec(`UPDATE sessions SET revoked_at = now() - INTERVAL '1 minute' WHERE revoke_reason = 'rotated'`)
	thief.cookies["maktaba_refresh"] = first
	if r := thief.do("POST", "/v1/auth/refresh", nil); r.code != 401 {
		t.Fatalf("replay: %d", r.code)
	}
	if n := e.count(`SELECT count(*) FROM sessions WHERE revoked_at IS NULL`); n != 0 {
		t.Fatalf("replay left %d live sessions", n)
	}
	if e.count(`SELECT count(*) FROM abuse_signals WHERE kind = 'refresh_token_replay'`) != 1 {
		t.Fatal("replay not recorded")
	}
	if r := c.do("POST", "/v1/auth/refresh", nil); r.code != 401 {
		t.Fatalf("legit client after replay: %d", r.code)
	}

	// Logout revokes and clears.
	c.login("bob@example.test", goodPassword)
	c.mustStatus(c.do("POST", "/v1/auth/logout", nil), http.StatusNoContent)
	if _, ok := c.cookies["maktaba_refresh"]; ok {
		t.Fatal("logout left the cookie")
	}
}

// Ten failed sign-ins in 15 minutes lock the account for 30 minutes —
// even the right password gets 429 — and a reset clears it.
func TestLoginLockout(t *testing.T) {
	e := newEnv(t)
	const email = "carol@example.test"
	c := e.registerVerified(email)
	attacker := e.client()
	for i := 0; i < 10; i++ {
		if r := attacker.do("POST", "/v1/auth/login", map[string]string{"email": email, "password": "not the password"}); r.code != 401 {
			t.Fatalf("attempt %d: %d", i, r.code)
		}
	}
	r := c.do("POST", "/v1/auth/login", map[string]string{"email": email, "password": goodPassword})
	if r.code != 429 || r.errCode() != "too_many_attempts" {
		t.Fatalf("11th attempt: %d %s", r.code, r.body)
	}
	if ra := r.header.Get("Retry-After"); ra == "" || ra == "0" || len(ra) < 3 {
		t.Fatalf("Retry-After %q, want ~1800", ra)
	}

	c.mustStatus(c.do("POST", "/v1/auth/password/forgot", map[string]string{"email": email}), http.StatusAccepted)
	tok := e.mailedToken("password_reset", email)
	c.mustStatus(c.do("POST", "/v1/auth/password/reset", map[string]string{"token": tok, "password": "a brand new passphrase"}), http.StatusNoContent)
	c.login(email, "a brand new passphrase")
}

// A reset link sets a new password once, revokes every session, and
// "forgot" answers the same for unknown addresses.
func TestPasswordReset(t *testing.T) {
	e := newEnv(t)
	const email = "dave@example.test"
	c := e.registerVerified(email)
	other := e.client()
	other.login(email, goodPassword)

	c.mustStatus(c.do("POST", "/v1/auth/password/forgot", map[string]string{"email": "ghost@example.test"}), http.StatusAccepted)
	if _, ok := e.mail.Last("password_reset", "ghost@example.test"); ok {
		t.Fatal("mailed an unknown address")
	}
	c.mustStatus(c.do("POST", "/v1/auth/password/forgot", map[string]string{"email": email}), http.StatusAccepted)
	tok := e.mailedToken("password_reset", email)
	if r := c.do("POST", "/v1/auth/password/reset", map[string]string{"token": tok, "password": "short"}); r.code != 400 || r.errCode() != "weak_password" {
		t.Fatalf("weak reset: %d", r.code)
	}
	c.mustStatus(c.do("POST", "/v1/auth/password/reset", map[string]string{"token": tok, "password": "another fine passphrase"}), http.StatusNoContent)
	if r := c.do("POST", "/v1/auth/password/reset", map[string]string{"token": tok, "password": "yet another passphrase"}); r.code != 400 || r.errCode() != "invalid_token" {
		t.Fatalf("reset token reuse: %d", r.code)
	}
	if r := other.do("POST", "/v1/auth/refresh", nil); r.code != 401 {
		t.Fatalf("session survived the reset: %d", r.code)
	}
	if r := c.do("POST", "/v1/auth/login", map[string]string{"email": email, "password": goodPassword}); r.code != 401 {
		t.Fatalf("old password still works: %d", r.code)
	}
	c.login(email, "another fine passphrase")
}

// The account page: profile validation, devices, sign out a device,
// export, and deletion with a password confirm and a cancellable hold.
func TestAccountPage(t *testing.T) {
	e := newEnv(t)
	const email = "erin@example.test"
	c := e.registerVerified(email)
	phone := e.client()
	phone.login(email, goodPassword)

	if r := c.do("PATCH", "/v1/account/me", map[string]string{"display_name": strings.Repeat("x", 81)}); r.code != 400 || r.errCode() != "display_name_too_long" {
		t.Fatalf("long name: %d %s", r.code, r.body)
	}
	if r := c.do("PATCH", "/v1/account/me", map[string]string{"locale": "xx"}); r.code != 400 || r.errCode() != "invalid_locale" {
		t.Fatalf("bad locale: %d", r.code)
	}
	if r := c.do("PATCH", "/v1/account/me", map[string]string{"avatar_url": "javascript:alert(1)"}); r.code != 400 || r.errCode() != "invalid_avatar_url" {
		t.Fatalf("bad avatar: %d", r.code)
	}
	me := c.mustStatus(c.do("PATCH", "/v1/account/me", map[string]string{"display_name": "Erin", "locale": "ar"}), 200).json(t)
	if me["display_name"] != "Erin" || me["locale"] != "ar" {
		t.Fatalf("profile: %v", me)
	}

	list := c.mustStatus(c.do("GET", "/v1/account/sessions", nil), 200).json(t)["sessions"].([]any)
	if len(list) != 2 {
		t.Fatalf("sessions: %v", list)
	}
	var phoneID string
	current := 0
	for _, s := range list {
		m := s.(map[string]any)
		if m["current"] == true {
			current++
		} else {
			phoneID = m["id"].(string)
		}
	}
	if current != 1 || phoneID == "" {
		t.Fatalf("current marking: %v", list)
	}
	// Another account cannot revoke this session.
	stranger := e.registerVerified("stranger@example.test")
	if r := stranger.do("DELETE", "/v1/account/sessions/"+phoneID, nil); r.code != 404 {
		t.Fatalf("cross-account revoke: %d", r.code)
	}
	c.mustStatus(c.do("DELETE", "/v1/account/sessions/"+phoneID, nil), http.StatusNoContent)
	if r := phone.do("POST", "/v1/auth/refresh", nil); r.code != 401 {
		t.Fatalf("revoked device still refreshes: %d", r.code)
	}

	exp := c.mustStatus(c.do("GET", "/v1/account/export", nil), 200)
	if !strings.Contains(exp.header.Get("Content-Disposition"), "attachment") || exp.json(t)["account"].(map[string]any)["email"] != email {
		t.Fatalf("export: %s", exp.body)
	}

	if r := c.do("DELETE", "/v1/account/me", map[string]string{"password": "nope nope nope"}); r.code != 401 {
		t.Fatalf("delete without password: %d", r.code)
	}
	c.mustStatus(c.do("DELETE", "/v1/account/me", map[string]string{"password": goodPassword}), http.StatusNoContent)
	if r := c.do("POST", "/v1/auth/refresh", nil); r.code != 401 {
		t.Fatalf("deletion left the session: %d", r.code)
	}
	c.login(email, goodPassword)
	me = c.mustStatus(c.do("GET", "/v1/account/me", nil), 200).json(t)
	if me["deletion"] == nil {
		t.Fatal("deletion hold not shown")
	}
	c.mustStatus(c.do("POST", "/v1/account/deletion/cancel", nil), http.StatusNoContent)
	if me = c.mustStatus(c.do("GET", "/v1/account/me", nil), 200).json(t); me["deletion"] != nil {
		t.Fatal("hold not cancelled")
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action IN ('account.delete','account.delete_cancel')`) != 2 {
		t.Fatal("deletion not audited")
	}

	// Change password signs every device out.
	if r := c.do("POST", "/v1/account/password", map[string]string{"current_password": "bad bad bad bad", "new_password": "fresh passphrase here"}); r.code != 401 {
		t.Fatalf("change with wrong current: %d", r.code)
	}
	c.mustStatus(c.do("POST", "/v1/account/password", map[string]string{"current_password": goodPassword, "new_password": "fresh passphrase here"}), http.StatusNoContent)
	if r := c.do("POST", "/v1/auth/refresh", nil); r.code != 401 {
		t.Fatalf("password change left the session: %d", r.code)
	}
}

// The mail-sending routes are limited per IP: burst 5.
func TestMailRoutesRateLimited(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	got429 := false
	for i := 0; i < 8; i++ {
		r := c.do("POST", "/v1/auth/password/forgot", map[string]string{"email": "x@example.test"})
		if r.code == 429 {
			got429 = true
			if r.header.Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
			break
		}
	}
	if !got429 {
		t.Fatal("forgot-password not rate limited")
	}
	// A different client IP is unaffected; a spoofed X-Forwarded-For is
	// not trusted by default, so it cannot mint a new bucket.
	if r := e.client().do("POST", "/v1/auth/password/forgot", map[string]string{"email": "x@example.test"}); r.code != 202 {
		t.Fatalf("other IP: %d", r.code)
	}
}

// A blocklisted IP is refused on the auth routes.
func TestBlockedIPRefused(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	e.exec(`INSERT INTO blocklist (subject, subject_kind, reason) VALUES ($1, 'ip', 'test')`, c.ip)
	if r := c.do("POST", "/v1/auth/login", map[string]string{"email": "a@example.test", "password": goodPassword}); r.code != 403 || r.errCode() != "blocked" {
		t.Fatalf("blocked ip: %d %s", r.code, r.body)
	}
}
