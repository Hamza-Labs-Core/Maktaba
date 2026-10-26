//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
)

// signInWithGoogle runs the browser side of the flow against the fake
// provider and returns where the callback sent the browser.
func (c *client) signInWithGoogle(t *testing.T, sub, email string, verified bool) string {
	t.Helper()
	start := c.do("GET", "/v1/auth/oauth/google", nil)
	if start.code != http.StatusFound {
		t.Fatalf("start: %d %s", start.code, start.body)
	}
	cb := c.e.idp.authorize(t, start.header.Get("Location"), sub, email, verified)
	r := c.do("GET", cb, nil)
	if r.code != http.StatusFound {
		t.Fatalf("callback: %d %s", r.code, r.body)
	}
	return r.header.Get("Location")
}

// Google sign-in (test provider): state, nonce and PKCE are checked;
// the id_token is verified against the provider's JWKS; the callback
// lands in the web app with a session the app swaps for a token.
func TestGoogleSignIn(t *testing.T) {
	e := newEnv(t)
	providers := e.client().do("GET", "/v1/auth/providers", nil).json(t)
	if providers["google"] != true || providers["apple"] != false {
		t.Fatalf("providers: %v", providers)
	}

	c := e.client()
	if loc := c.signInWithGoogle(t, "g-1", "gina@example.test", true); loc != testWebURL+"/signed-in" {
		t.Fatalf("landing: %s", loc)
	}
	r := c.mustStatus(c.do("POST", "/v1/auth/refresh", nil), 200).json(t)
	c.token = r["access_token"].(string)
	me := c.mustStatus(c.do("GET", "/v1/account/me", nil), 200).json(t)
	if me["email"] != "gina@example.test" || me["has_password"] != false || me["email_verified"] != true ||
		len(me["identities"].([]any)) != 1 || me["display_name"] != "Test g-1" {
		t.Fatalf("federated account: %v", me)
	}
	// A password login is impossible until one is set via reset.
	if r := e.client().do("POST", "/v1/auth/login", map[string]string{"email": "gina@example.test", "password": goodPassword}); r.code != 401 {
		t.Fatalf("password login on oauth account: %d", r.code)
	}

	// Signing in again resolves the same account.
	c2 := e.client()
	c2.signInWithGoogle(t, "g-1", "gina@example.test", true)
	if e.count(`SELECT count(*) FROM users`) != 1 {
		t.Fatal("second sign-in created another account")
	}

	// A verified local account links when Google vouches for the email.
	e.registerVerified("local@example.test")
	e.client().signInWithGoogle(t, "g-2", "local@example.test", true)
	if e.count(`SELECT count(*) FROM oauth_links l JOIN users u ON u.id = l.user_id WHERE u.email = 'local@example.test'`) != 1 {
		t.Fatal("verified account not linked")
	}

	// An unverified local account is not taken over by an OAuth identity
	// with its address.
	p := e.client()
	p.mustStatus(p.do("POST", "/v1/auth/register", map[string]string{"email": "squat@example.test", "password": goodPassword}), http.StatusAccepted)
	if loc := e.client().signInWithGoogle(t, "g-3", "squat@example.test", true); !strings.HasSuffix(loc, "/sign-in?error=email_in_use") {
		t.Fatalf("pre-registered unverified: %s", loc)
	}

	// Forged state: refused, recorded, no session.
	x := e.client()
	start := x.do("GET", "/v1/auth/oauth/google", nil)
	cb := e.idp.authorize(t, start.header.Get("Location"), "g-4", "x@example.test", true)
	cb = strings.Replace(cb, "state=", "state=tampered", 1)
	r2 := x.do("GET", cb, nil)
	if !strings.HasSuffix(r2.header.Get("Location"), "/sign-in?error=bad_state") {
		t.Fatalf("bad state: %d %s", r2.code, r2.header.Get("Location"))
	}
	if _, ok := x.cookies["maktaba_refresh"]; ok {
		t.Fatal("bad state started a session")
	}
	if e.count(`SELECT count(*) FROM abuse_signals WHERE kind = 'oauth_state'`) != 1 {
		t.Fatal("bad state not recorded")
	}

	// A suspended account cannot sign in through Google either.
	e.exec(`UPDATE users SET status = 'suspended' WHERE email = 'gina@example.test'`)
	if loc := e.client().signInWithGoogle(t, "g-1", "gina@example.test", true); !strings.HasSuffix(loc, "error=account_suspended") {
		t.Fatalf("suspended oauth: %s", loc)
	}
}
