//go:build integration

package main

import (
	"net/http"
	"testing"
)

// Story 50.28 on the api role's real routers: the home door (role_api's MountAnalyticsHome)
// answers a home's agent with that home's own report and nothing else. Another home of the same
// owner, named by id, is 403; no credential, or a customer's token alone, is 401; and the owner's
// bearer route at /v1/broadcast/analytics is still the owner's (the two mounts don't collide).
func TestBroadcastHomeAnalyticsDoorOnTheRealRouter(t *testing.T) {
	e := newEnv(t)
	owner := e.registerVerified("analytics-owner@example.test")
	home := claimHome(e, owner, "viewers-home")
	sibling := claimHome(e, owner, "viewers-sibling")
	const door = "/v1/broadcast/home/analytics"

	r := home.agent.mustStatus(home.agent.do("GET", door+"?period=7d&tz=Africa/Cairo", nil), http.StatusOK).json(t)
	if r["time_zone"] != "Africa/Cairo" || r["level"] == nil || r["live_viewers"] != float64(0) {
		t.Fatalf("the home's own report = %v", r)
	}
	if got := home.agent.do("GET", door+"?server="+sibling.id, nil); got.code != http.StatusForbidden ||
		got.errCode() != "broadcast_analytics_forbidden" {
		t.Fatalf("a home naming its sibling = %d %s, want 403", got.code, got.body)
	}
	if got := home.agent.do("GET", door+"?server="+home.id, nil); got.code != http.StatusOK {
		t.Fatalf("a home naming itself = %d %s", got.code, got.body)
	}
	if got := e.client().do("GET", door, nil); got.code != http.StatusUnauthorized {
		t.Fatalf("no credential = %d %s, want 401", got.code, got.body)
	}
	if got := owner.do("GET", door, nil); got.code != http.StatusUnauthorized {
		t.Fatalf("the owner's token on the home door = %d %s, want 401", got.code, got.body)
	}
	if got := owner.do("GET", "/v1/broadcast/analytics?server="+home.id, nil); got.code != http.StatusOK {
		t.Fatalf("the owner's own route = %d %s", got.code, got.body)
	}
}
