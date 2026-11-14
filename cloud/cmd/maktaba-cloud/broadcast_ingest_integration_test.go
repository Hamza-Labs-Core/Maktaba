//go:build integration

package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	broadcasth "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/broadcast"
)

// Story 50.21: the api role mounts the ingest-token route. Off (the default) it answers 503
// broadcast_disabled; on with a key it asks for the home's own agent credential.
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
