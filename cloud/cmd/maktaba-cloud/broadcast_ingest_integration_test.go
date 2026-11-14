//go:build integration

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
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
