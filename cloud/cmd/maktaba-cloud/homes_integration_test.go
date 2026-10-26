//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// On the api role's real router: the home agent's routes take no user
// token (the claim code, then the server's secret), the user routes need
// one, and a linked account gets a sign-in assertion (Epic 42 story 42.7).
func TestHomesOnTheRealRouter(t *testing.T) {
	e := newEnv(t)
	owner := e.registerVerified("owner@example.test")
	friend := e.registerVerified("friend@example.test")
	friendID := e.userID("friend@example.test")

	code := owner.mustStatus(owner.do("POST", "/v1/servers/claims", nil), http.StatusOK).json(t)["code"].(string)
	agent := e.client() // no token
	red := agent.mustStatus(agent.do("POST", "/v1/servers/claims/redeem",
		map[string]string{"code": code, "name": "Home", "slug": "homeone"}), http.StatusCreated).json(t)
	id, secret := red["server_id"].(string), red["server_secret"].(string)
	if key, _ := red["assertion_public_key"].(string); key == "" {
		t.Fatal("redeem without the assertion key to pin")
	}
	if got := agent.do("GET", "/v1/homes/assertion-key", nil); got.code != http.StatusOK || got.json(t)["public_key"] != red["assertion_public_key"] {
		t.Fatalf("assertion key: %d %s", got.code, got.body)
	}

	// the agent's links report: its own secret, no user token
	links := func(secret string) int {
		req := httptest.NewRequest("PUT", "/v1/servers/"+id+"/links",
			strings.NewReader(`{"cloud_user_ids":["`+friendID+`"]}`))
		req.RemoteAddr = "198.51.100.250:40000"
		req.SetBasicAuth(id, secret)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := links("wrong"); got != http.StatusUnauthorized {
		t.Fatalf("links with a wrong secret: %d", got)
	}
	if got := links(secret); got != http.StatusNoContent {
		t.Fatalf("links: %d", got)
	}

	// the user routes need a user
	if got := agent.do("GET", "/v1/homes", nil); got.code != http.StatusUnauthorized {
		t.Fatalf("/v1/homes without a user: %d", got.code)
	}
	a := friend.mustStatus(friend.do("POST", "/v1/homes/"+id+"/assertion", map[string]string{"purpose": "signin"}), http.StatusOK).json(t)
	if a["assertion"] == "" {
		t.Fatalf("assertion %v", a)
	}
	if got := owner.do("POST", "/v1/homes/"+id+"/assertion", map[string]string{"purpose": "signin"}); got.code != http.StatusNotFound {
		t.Fatalf("the unlinked owner got a sign-in assertion: %d", got.code)
	}
}
