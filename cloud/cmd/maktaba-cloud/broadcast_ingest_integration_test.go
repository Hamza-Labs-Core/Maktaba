//go:build integration

package main

import (
	"crypto/ed25519"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
)

// Story 50.21: the api role mounts the ingest-token route. Without broadcast.key_path (the
// default) it answers 503 broadcast_disabled; with the key it asks for the home's own agent
// credential.
func TestBroadcastIngestTokenRouteIsWired(t *testing.T) {
	off := newEnv(t).client()
	if r := off.do(http.MethodPost, "/v1/broadcast/ingest-token", map[string]any{"channel": "c1", "rungs": []string{"720p"}}); r.code != http.StatusServiceUnavailable || r.errCode() != "broadcast_disabled" {
		t.Fatalf("broadcast off: %d %s", r.code, r.body)
	}

	withBroadcastPrices(t) // the gate opens only with every broadcast price set
	key := ingestKeyFile(t)
	on := newEnvCfg(t, nil, func(c *config.Config) {
		c.Broadcast.Enabled = true
		c.Broadcast.KeyPath = key
	}).client()
	r := on.do(http.MethodPost, "/v1/broadcast/ingest-token", map[string]any{"channel": "c1", "rungs": []string{"720p"}})
	if r.code != http.StatusUnauthorized || r.errCode() != "unauthorized" {
		t.Fatalf("broadcast on, no agent credential: %d %s", r.code, r.body)
	}
}

// ingestKeyFile writes an ingest-token signing seed (32 zero bytes) for one test.
func ingestKeyFile(t *testing.T) string {
	t.Helper()
	key := filepath.Join(t.TempDir(), "broadcast.key")
	if err := os.WriteFile(key, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	return key
}

// ingestRungs asks for a token for rungs and returns the response.
func ingestRungs(h bhome, rungs ...string) resp {
	return h.agent.do(http.MethodPost, "/v1/broadcast/ingest-token", map[string]any{"channel": "7", "rungs": rungs})
}

// ingestClaims checks a 200 and reads the token's claims with the test key's public half.
func ingestClaims(t *testing.T, r resp) broadcast.IngestClaims {
	t.Helper()
	if r.code != http.StatusOK {
		t.Fatalf("ingest token: %d %s", r.code, r.body)
	}
	k, err := broadcast.KeyFromSeed(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	c, err := broadcast.VerifyIngest(map[string]ed25519.PublicKey{k.Kid: k.Priv.Public().(ed25519.PublicKey)}, r.json(t)["token"].(string), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Role rules for the ingest token, from 50.20's entitlements: with the gate shut (the key set,
// broadcast.enabled off) only a test publisher a service admin granted gets one; with the gate
// open, guest sharing and the Broadcaster add-on get their catalog tier's rungs and top bitrate;
// no plan, or a suspended owner, gets none. The token's publisher is the home.
func TestBroadcastIngestTokenEntitlements(t *testing.T) {
	guest, caster := billingpkg.Tiers[billingpkg.PlanPro].Broadcast, billingpkg.Broadcaster
	rungs := []string{"src", "720p", "480p", "360p"}

	t.Run("gate shut", func(t *testing.T) {
		e := newEnvCfg(t, nil, func(c *config.Config) { c.Broadcast.KeyPath = ingestKeyFile(t) })
		owner := e.registerVerified("shut@example.test")
		uid := e.userID("shut@example.test")
		h := claimHome(e, owner, "shut")
		setPlan(e, uid, billingpkg.PlanFree)
		if r := ingestRungs(h, "src"); r.code != http.StatusForbidden || r.errCode() != "broadcast_not_in_plan" {
			t.Fatalf("free: %d %s", r.code, r.body)
		}
		setPlan(e, uid, billingpkg.PlanPro)
		e.exec(`UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, h.id)
		if r := ingestRungs(h, "src"); r.code != http.StatusForbidden || r.errCode() != "broadcast_not_enabled" {
			t.Fatalf("broadcaster, not a test publisher: %d %s", r.code, r.body)
		}
		e.exec(`UPDATE servers SET broadcast_test_publisher = true WHERE id = $1`, h.id)
		c := ingestClaims(t, ingestRungs(h, rungs[:caster.MaxRungs]...))
		if c.Pub != h.id || c.Home != h.id || c.Kbps != caster.MaxTopKbps || len(c.Rungs) != caster.MaxRungs {
			t.Fatalf("a test publisher's token: %+v", c)
		}
	})

	t.Run("gate open", func(t *testing.T) {
		withBroadcastPrices(t)
		e := newEnvCfg(t, nil, func(c *config.Config) {
			c.Broadcast.Enabled = true
			c.Broadcast.KeyPath = ingestKeyFile(t)
		})
		owner := e.registerVerified("open@example.test")
		uid := e.userID("open@example.test")
		h := claimHome(e, owner, "open")
		setPlan(e, uid, billingpkg.PlanBasic)
		if r := ingestRungs(h, "src"); r.code != http.StatusForbidden || r.errCode() != "broadcast_not_in_plan" {
			t.Fatalf("basic: %d %s", r.code, r.body)
		}
		// Pro's guest sharing: its rungs and bitrate, one rung more is refused
		setPlan(e, uid, billingpkg.PlanPro)
		c := ingestClaims(t, ingestRungs(h, rungs[:guest.MaxRungs]...))
		if c.Pub != h.id || c.Kbps != guest.MaxTopKbps || len(c.Rungs) != guest.MaxRungs {
			t.Fatalf("guest sharing: %+v", c)
		}
		if r := ingestRungs(h, rungs[:guest.MaxRungs+1]...); r.code != http.StatusForbidden || r.errCode() != "plan_rungs" {
			t.Fatalf("guest sharing past its rungs: %d %s", r.code, r.body)
		}
		// the Broadcaster add-on: more rungs and a higher top bitrate
		e.exec(`UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, h.id)
		c = ingestClaims(t, ingestRungs(h, rungs[:caster.MaxRungs]...))
		if c.Kbps != caster.MaxTopKbps || len(c.Rungs) != caster.MaxRungs {
			t.Fatalf("broadcaster: %+v", c)
		}
		if r := ingestRungs(h, rungs[:caster.MaxRungs+1]...); r.code != http.StatusForbidden || r.errCode() != "plan_rungs" {
			t.Fatalf("broadcaster past its rungs: %d %s", r.code, r.body)
		}
		// a suspended owner's home gets none
		e.exec(`UPDATE users SET status = 'suspended' WHERE id = $1`, uid)
		if r := ingestRungs(h, "src"); r.code != http.StatusForbidden || r.errCode() != "broadcast_not_in_plan" {
			t.Fatalf("suspended: %d %s", r.code, r.body)
		}
	})
}
