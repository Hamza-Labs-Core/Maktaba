package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// After serve's check, its start reads the node config again (the machine agent, the
// bundled Postgres, the node's copy of the home's database): one that cannot be read
// then is an error, never "not joined", so no home's Postgres cluster is made and no
// home's service starts beside a node's config. A forced home starts its own.
func TestServeStartsNothingOfAHomeOnAnUnreadableNodeConfig(t *testing.T) {
	t.Setenv("MAKTABA_ROLE", "")
	path := filepath.Join(t.TempDir(), "node.json")
	if err := os.WriteFile(path, []byte(`{"node_id":"n1","priv`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_NODE_CONFIG", path)
	cfg := config.Defaults()
	cfg.Server.DataDir = filepath.Join(t.TempDir(), "data")
	ctx := context.Background()
	if pg, err := bundledPostgres(ctx, &cfg, true); err == nil || pg != nil {
		t.Errorf("bundledPostgres = %v, %v; want the node config's error", pg, err)
	}
	if ns, err := startNodeServices(ctx, cfg, nil); err == nil || ns != nil {
		t.Errorf("startNodeServices = %v, %v; want the node config's error", ns, err)
	}
	if cp, err := resolveNodeCopy(&cfg, true); err == nil || cp != nil {
		t.Errorf("resolveNodeCopy = %v, %v; want the node config's error", cp, err)
	}
	t.Setenv("MAKTABA_ROLE", "home")
	if _, err := bundledPostgres(ctx, &cfg, false); err != nil && strings.Contains(err.Error(), path) {
		t.Errorf("a forced home's Postgres: %v", err)
	}
}

// serve waits for a write of node.json still under way before it starts (once said),
// and starts, or stops with what the check says, once it ended; one that does not end
// within the limit is its error, and any other error stops it at once.
func TestServeWaitsOutAWriteOfTheNodeConfig(t *testing.T) {
	busy := errors.Join(errors.New("node config x"), config.ErrBeingWritten)
	var log bytes.Buffer
	left := 2
	err := nodeConfigSettles(func() error {
		if left > 0 {
			left--
			return busy
		}
		return nil
	}, time.Millisecond, time.Minute, &log)
	if err != nil || left != 0 || strings.Count(log.String(), "starting once the write ends") != 1 {
		t.Fatalf("after the write: %v (left %d), said %q", err, left, log.String())
	}
	if err := nodeConfigSettles(func() error { return busy }, time.Millisecond, 20*time.Millisecond, &log); !errors.Is(err, config.ErrBeingWritten) {
		t.Fatalf("a write that does not end: %v", err)
	}
	broken := errors.New("node config x: not JSON")
	calls := 0
	if err := nodeConfigSettles(func() error { calls++; return broken }, time.Millisecond, time.Minute, &log); !errors.Is(err, broken) || calls != 1 {
		t.Fatalf("a broken node config: %v after %d checks", err, calls)
	}
}
