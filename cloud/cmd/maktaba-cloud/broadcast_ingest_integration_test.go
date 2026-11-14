//go:build integration

package main

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	broadcasth "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/broadcast"
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

// Story 50.23 §5: Maktaba's broadcast country deny list is a service admin's, with a reason,
// audited; the ingest route reads it from the same table.
func TestBroadcastGeoDenyIsAServiceAdmins(t *testing.T) {
	e := newEnv(t)
	const path = "/v1/admin/broadcast/geo-deny"
	body := map[string]any{"countries": []string{"kp", "IR", "KP"}, "reason": "sanctions review"}
	if r := e.registerVerified("user@example.test").do("PUT", path, body); r.code != http.StatusUnauthorized {
		t.Fatalf("a customer set the list: %d", r.code)
	}
	if r := e.staff("norole@example.test", "").do("PUT", path, body); r.code != http.StatusForbidden {
		t.Fatalf("a staff account without the role set the list: %d %s", r.code, r.body)
	}
	ops := e.serviceAdmin("ops@example.test")
	if r := ops.do("PUT", path, map[string]any{"countries": []string{"KP"}}); r.code != http.StatusBadRequest || r.errCode() != "reason_required" {
		t.Fatalf("no reason: %d %s", r.code, r.body)
	}
	if r := ops.do("PUT", path, map[string]any{"countries": []string{"Korea"}, "reason": "x"}); r.code != http.StatusUnprocessableEntity || r.errCode() != "bad_country" {
		t.Fatalf("bad country: %d %s", r.code, r.body)
	}
	ops.mustStatus(ops.do("PUT", path, body), http.StatusOK)
	got := ops.mustStatus(ops.do("GET", path, nil), http.StatusOK).json(t)["countries"].([]any)
	if len(got) != 2 || got[0].(map[string]any)["country"] != "IR" || got[1].(map[string]any)["reason"] != "sanctions review" {
		t.Fatalf("list %v", got)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.broadcast.geo_deny' AND detail->>'reason' = 'sanctions review'`) != 1 {
		t.Fatal("the change wasn't audited")
	}
	deny, err := broadcasth.DBGeoDeny(e.pool.DB)(context.Background())
	if err != nil || strings.Join(deny, ",") != "IR,KP" {
		t.Fatalf("the ingest route reads %v %v", deny, err)
	}
	// an empty list clears it
	ops.mustStatus(ops.do("PUT", path, map[string]any{"countries": []string{}, "reason": "lifted"}), http.StatusOK)
	if n := e.count(`SELECT count(*) FROM broadcast_geo_deny`); n != 0 {
		t.Fatalf("%d left", n)
	}
}

// publishedChannel records a live broadcast channel for the home and returns its cloud id: every
// ingest token names one (story 50.24).
func publishedChannel(t *testing.T, e *env, h bhome) string {
	t.Helper()
	var id string
	if err := e.pool.DB.QueryRow(`INSERT INTO broadcast_channels (server_id, slug, access, agreement_version)
		VALUES ($1, 'ingest-'||substr(md5(random()::text), 1, 8), 'open', 1) RETURNING id::text`, h.id).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// ingestRungs asks for a token for rungs, for the published channel cloudID, and returns the response.
func ingestRungs(h bhome, cloudID string, rungs ...string) resp {
	return h.agent.do(http.MethodPost, "/v1/broadcast/ingest-token", map[string]any{"channel": "7", "cloud_id": cloudID, "rungs": rungs})
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
		cid := publishedChannel(t, e, h)
		setPlan(e, uid, billingpkg.PlanFree)
		if r := ingestRungs(h, cid, "src"); r.code != http.StatusForbidden || r.errCode() != "broadcast_not_in_plan" {
			t.Fatalf("free: %d %s", r.code, r.body)
		}
		setPlan(e, uid, billingpkg.PlanPro)
		e.exec(`UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, h.id)
		if r := ingestRungs(h, cid, "src"); r.code != http.StatusForbidden || r.errCode() != "broadcast_not_enabled" {
			t.Fatalf("broadcaster, not a test publisher: %d %s", r.code, r.body)
		}
		e.exec(`UPDATE servers SET broadcast_test_publisher = true WHERE id = $1`, h.id)
		c := ingestClaims(t, ingestRungs(h, cid, rungs[:caster.MaxRungs]...))
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
		cid := publishedChannel(t, e, h)
		setPlan(e, uid, billingpkg.PlanBasic)
		if r := ingestRungs(h, cid, "src"); r.code != http.StatusForbidden || r.errCode() != "broadcast_not_in_plan" {
			t.Fatalf("basic: %d %s", r.code, r.body)
		}
		// Pro's guest sharing: its rungs and bitrate, one rung more is refused
		setPlan(e, uid, billingpkg.PlanPro)
		c := ingestClaims(t, ingestRungs(h, cid, rungs[:guest.MaxRungs]...))
		if c.Pub != h.id || c.Kbps != guest.MaxTopKbps || len(c.Rungs) != guest.MaxRungs {
			t.Fatalf("guest sharing: %+v", c)
		}
		if r := ingestRungs(h, cid, rungs[:guest.MaxRungs+1]...); r.code != http.StatusForbidden || r.errCode() != "plan_rungs" {
			t.Fatalf("guest sharing past its rungs: %d %s", r.code, r.body)
		}
		// the Broadcaster add-on: more rungs and a higher top bitrate
		e.exec(`UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, h.id)
		c = ingestClaims(t, ingestRungs(h, cid, rungs[:caster.MaxRungs]...))
		if c.Kbps != caster.MaxTopKbps || len(c.Rungs) != caster.MaxRungs {
			t.Fatalf("broadcaster: %+v", c)
		}
		if r := ingestRungs(h, cid, rungs[:caster.MaxRungs+1]...); r.code != http.StatusForbidden || r.errCode() != "plan_rungs" {
			t.Fatalf("broadcaster past its rungs: %d %s", r.code, r.body)
		}
		// a suspended owner's home gets none
		e.exec(`UPDATE users SET status = 'suspended' WHERE id = $1`, uid)
		if r := ingestRungs(h, cid, "src"); r.code != http.StatusForbidden || r.errCode() != "broadcast_not_in_plan" {
			t.Fatalf("suspended: %d %s", r.code, r.body)
		}
	})
}
