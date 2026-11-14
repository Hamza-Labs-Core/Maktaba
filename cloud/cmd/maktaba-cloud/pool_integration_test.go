//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
)

// The api pool (Epic 50 story 50.17): several api processes on one database, any of which may
// serve any request.

// T03: an address tries to sign in, round robin over three api nodes: it is refused 429 once
// the shared burst (30) is spent, as on one node, not after three nodes' worth (90). The
// attempts are malformed, so each is quick (no password hash) and the 5/s refill adds next to
// nothing; the refill there was is allowed for. Another address still signs in.
func TestPoolSignInLimitIsShared(t *testing.T) {
	e := newEnv(t)
	e.registerVerified("pool@example.test")
	nodes := []http.Handler{e.h, e.node(), e.node()}
	attacker := e.client()
	start := time.Now()
	refusedAt := 0
	for i := 1; i <= 90 && refusedAt == 0; i++ {
		e.h = nodes[i%3]
		r := attacker.do("POST", "/v1/auth/login", []byte(`{"email":`))
		if r.code == http.StatusTooManyRequests {
			if r.errCode() != "rate_limited" || r.header.Get("Retry-After") == "" {
				t.Fatalf("429 without the limiter's body or Retry-After: %s", r.body)
			}
			refusedAt = i
		}
	}
	elapsed := time.Since(start)
	refill := int(elapsed.Seconds()*5) + 1
	t.Logf("first refusal at attempt %d after %s (burst 30, refill up to %d)", refusedAt, elapsed, refill)
	if refusedAt < 31 || refusedAt > 31+refill {
		t.Fatalf("first refusal at attempt %d of a shared burst of 30 (%s, refill %d)", refusedAt, elapsed, refill)
	}
	if n := e.count(`SELECT count(*) FROM cloud_rate_buckets WHERE key = $1`, "auth:ip:"+attacker.ip); n != 1 {
		t.Fatalf("%d shared buckets for the address", n)
	}
	// another address on the third node is not limited by the first one's bucket
	e.h = nodes[2]
	other := e.client()
	other.mustStatus(other.do("POST", "/v1/auth/login", map[string]string{"email": "pool@example.test", "password": goodPassword}), http.StatusOK)
}

// T04: a plan change from the payment provider's webhook, delivered to node 1, is what node 2
// serves on the next request for that account and its home: the api keeps no copy of it.
func TestPoolPlanChangeSeenByAnotherNode(t *testing.T) {
	e := newPaidEnv(t)
	const email = "pool-payer@example.test"
	c := e.registerVerified(email)
	uid := e.userID(email)
	e.exec(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, 'Home', 'pool-home', 'x')`, uid)
	withPrices(t)
	node1, node2 := e.h, e.node()

	e.h = node2
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["plan"] != "free" {
		t.Fatalf("before: %v", me)
	}
	e.h = node1
	hook := e.client()
	t0 := time.Now().Add(-time.Minute)
	body, sig := signedEvent(subEvent("evt_pool", "subscription_started", "sub_p", "cus_p", uid, "active", "basic", t0, time.Now().Add(30*24*time.Hour)))
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)
	changed := time.Now()
	e.h = node2
	me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t)
	if me["plan"] != "basic" {
		t.Fatalf("node 2 after the change on node 1: %v", me)
	}
	if took := time.Since(changed); took > time.Second {
		t.Fatalf("node 2 saw the change %s later", took)
	}
}

// A stopping node's /readyz answers 503 on both listeners while it drains (server.drain_delay),
// so the load balancer takes it out before its port closes; the other nodes stay ready.
func TestPoolDrainingNodeIsUnready(t *testing.T) {
	e := newEnv(t)
	a, stop, err := newAPI(e.logger, e.cfg, e.pool, e.mig, server.BuildInfo{Role: "api"}, e.o)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	readyz := func(h http.Handler, host string) int {
		req := httptest.NewRequest("GET", "/readyz", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == 503 && !strings.Contains(rec.Body.String(), "draining") {
			t.Fatalf("503 for another reason: %s", rec.Body)
		}
		return rec.Code
	}
	if c := readyz(a.User, "api.example.test"); c != 200 {
		t.Fatalf("ready before the drain: %d", c)
	}
	a.Draining.Store(true)
	if c := readyz(a.User, "api.example.test"); c != 503 {
		t.Fatalf("user listener while draining: %d", c)
	}
	if c := readyz(a.Admin, testAdminHost); c != 503 {
		t.Fatalf("admin listener while draining: %d", c)
	}
	if c := readyz(e.router, "api.example.test"); c != 200 {
		t.Fatalf("another node went unready: %d", c)
	}
}
