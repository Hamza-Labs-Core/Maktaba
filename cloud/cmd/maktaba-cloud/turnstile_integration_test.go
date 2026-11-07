//go:build integration

package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/turnstile"
)

// fakeSiteverify stands in for Cloudflare's siteverify: "good-*" tokens pass once.
type fakeSiteverify struct {
	mu   sync.Mutex
	used map[string]bool
	ips  []string
	down bool
}

func (f *fakeSiteverify) Verify(_ context.Context, token, remoteIP string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ips = append(f.ips, remoteIP)
	if f.down {
		return turnstile.ErrUnavailable
	}
	if !strings.HasPrefix(token, "good-") || f.used[token] {
		return turnstile.ErrFailed
	}
	f.used[token] = true
	return nil
}

// Story 50.30 T05: register, login and forgot from a web origin need a token; a native app
// (no Origin) goes on to the normal checks; siteverify down is 503 with Retry-After.
func TestTurnstileOnTheAuthRoutes(t *testing.T) {
	ts := &fakeSiteverify{used: map[string]bool{}}
	e := newEnvOpts(t, nil, ts)
	web := e.client()
	web.origin = testWebURL
	app := e.client()

	body := func(token string) map[string]any {
		return map[string]any{"email": "ts@example.test", "password": "a long enough passphrase", "turnstile_token": token}
	}
	for _, path := range []string{"/v1/auth/register", "/v1/auth/login", "/v1/auth/password/forgot"} {
		for _, tok := range []string{"", "bad"} {
			r := web.do(http.MethodPost, path, body(tok))
			if r.code != http.StatusForbidden || !strings.Contains(string(r.body), "turnstile_failed") {
				t.Fatalf("%s from the web, token %q: %d %s", path, tok, r.code, r.body)
			}
		}
	}
	// nothing was created by the refused sign-ups
	var n int
	if err := e.pool.DB.QueryRow(`SELECT count(*) FROM users WHERE email = 'ts@example.test'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("users after refused sign-ups: %d %v", n, err)
	}
	// a good token passes once; reused, it is refused
	if r := web.do(http.MethodPost, "/v1/auth/register", body("good-1")); r.code != http.StatusCreated && r.code != http.StatusAccepted && r.code != http.StatusOK {
		t.Fatalf("register with a good token: %d %s", r.code, r.body)
	}
	if r := web.do(http.MethodPost, "/v1/auth/login", body("good-1")); r.code != http.StatusForbidden {
		t.Fatalf("a reused token: %d %s", r.code, r.body)
	}
	if ts.ips[len(ts.ips)-1] != web.ip {
		t.Fatalf("siteverify got remoteip %q, want %q", ts.ips[len(ts.ips)-1], web.ip)
	}
	// a native app (no Origin) is not asked: the normal checks answer
	before := len(ts.ips)
	if r := app.do(http.MethodPost, "/v1/auth/login", body("")); strings.Contains(string(r.body), "turnstile") {
		t.Fatalf("the app was asked for a token: %d %s", r.code, r.body)
	}
	if len(ts.ips) != before {
		t.Fatal("siteverify was asked for the app's request")
	}
	// siteverify down: 503, Retry-After
	ts.down = true
	if r := web.do(http.MethodPost, "/v1/auth/login", body("good-2")); r.code != http.StatusServiceUnavailable || r.header.Get("Retry-After") != "30" ||
		!strings.Contains(string(r.body), "turnstile_unavailable") {
		t.Fatalf("siteverify down: %d %v %s", r.code, r.header, r.body)
	}
	// the providers answer carries the widget's site key
	ts.down = false
	if r := web.do(http.MethodGet, "/v1/auth/providers", nil); !strings.Contains(string(r.body), `"turnstile_site_key":"test-site-key"`) {
		t.Fatalf("providers: %s", r.body)
	}
}
