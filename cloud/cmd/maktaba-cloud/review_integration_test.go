//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The refresh cookie is host-only: a Domain=.maktaba.tv cookie would
// reach every home server behind the relay's subdomains.
func TestRefreshCookieIsHostOnly(t *testing.T) {
	e := newEnv(t)
	c := e.registerVerified("host@example.test")
	r := c.do("POST", "/v1/auth/refresh", nil)
	for _, sc := range r.header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, "maktaba_refresh=") && strings.Contains(strings.ToLower(sc), "domain=") {
			t.Fatalf("refresh cookie has a Domain: %s", sc)
		}
	}
}

// A tab that loses the refresh race gets 401 without deleting the
// cookie the winning tab just received.
func TestRefreshRaceKeepsCookie(t *testing.T) {
	e := newEnv(t)
	a := e.registerVerified("race@example.test")
	old := a.cookies["maktaba_refresh"]
	a.mustStatus(a.do("POST", "/v1/auth/refresh", nil), 200)
	b := e.client()
	b.cookies["maktaba_refresh"] = old
	r := b.do("POST", "/v1/auth/refresh", nil)
	if r.code != 401 {
		t.Fatalf("loser: %d", r.code)
	}
	for _, sc := range r.header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, "maktaba_refresh=") {
			t.Fatalf("loser's response touched the cookie: %s", sc)
		}
	}
}

// Sign out everywhere, and an admin block, stop live access tokens at
// once — including a token minted before its session was rotated.
func TestRevocationStopsAccessTokens(t *testing.T) {
	e := newEnv(t)
	const email = "rev@example.test"
	c := e.registerVerified(email)
	early := c.token // minted from the first session
	c.token = c.mustStatus(c.do("POST", "/v1/auth/refresh", nil), 200).json(t)["access_token"].(string)
	stale := e.client()
	stale.token = early
	stale.mustStatus(stale.do("GET", "/v1/account/me", nil), 200) // rotated session: still alive

	c.mustStatus(c.do("POST", "/v1/account/sessions/revoke-all", nil), http.StatusNoContent)
	for name, cl := range map[string]*client{"current": c, "pre-rotation": stale} {
		if r := cl.do("GET", "/v1/account/me", nil); r.code != 401 {
			t.Errorf("%s token after sign-out-everywhere: %d", name, r.code)
		}
	}

	c.login(email, goodPassword)
	admin := e.serviceAdmin("ops@example.test")
	admin.mustStatus(admin.do("POST", "/v1/admin/abuse/block", map[string]any{"subject_kind": "user", "subject": e.userID(email), "reason": "spam"}), http.StatusNoContent)
	if r := c.do("GET", "/v1/account/me", nil); r.code != 401 && r.code != 403 {
		t.Fatalf("blocked user's token: %d", r.code)
	}
	e.exec(`UPDATE sessions SET revoked_at = NULL, revoke_reason = NULL WHERE user_id = $1`, e.userID(email))
	if r := c.do("GET", "/v1/account/me", nil); r.code != 403 || r.errCode() != "blocked" {
		t.Fatalf("blocklisted live session: %d %s", r.code, r.body)
	}
}

// Same-second created(incomplete) arriving after updated(active) must
// not reset a paying customer; a second subscription being cancelled
// must not downgrade the live one.
func TestWebhookTiesAndSecondSubscription(t *testing.T) {
	e := newEnv(t)
	const email = "tie@example.test"
	c := e.registerVerified(email)
	uid := e.userID(email)
	s := e.client()
	at := time.Now().Add(-time.Minute).Truncate(time.Second)
	end := time.Now().Add(720 * time.Hour)
	body, sig := signedEvent("evt_upd", "customer.subscription.updated", at, subscriptionObj("sub_a", "cus_t", uid, "active", testPricePro, end))
	s.mustStatus(s.webhook(body, sig), 200)
	body, sig = signedEvent("evt_crt", "customer.subscription.created", at, subscriptionObj("sub_a", "cus_t", uid, "incomplete", testPricePro, end))
	s.mustStatus(s.webhook(body, sig), 200)
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["plan"] != "pro" || me["status"] != "active" {
		t.Fatalf("tie reset the subscription: %v", me)
	}

	body, sig = signedEvent("evt_b_del", "customer.subscription.deleted", at.Add(time.Minute), subscriptionObj("sub_b", "cus_t", uid, "canceled", testPriceFamily, end))
	s.mustStatus(s.webhook(body, sig), 200)
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["plan"] != "pro" {
		t.Fatalf("second subscription's cancel downgraded the live one: %v", me)
	}
}

// A new account is never created from an email the provider does not
// vouch for.
func TestOAuthRefusesUnverifiedNewAccount(t *testing.T) {
	e := newEnv(t)
	if loc := e.client().signInWithGoogle(t, "g-u", "unv@example.test", false); !strings.HasSuffix(loc, "error=email_unverified") {
		t.Fatalf("landing: %s", loc)
	}
	if e.count(`SELECT count(*) FROM users`) != 0 {
		t.Fatal("account created")
	}
}
