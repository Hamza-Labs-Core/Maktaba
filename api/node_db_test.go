package main

// A full node's database is the home's, through the node's own limited role (slot
// 0103): its start must not hang on a home that accepts and never answers, and it
// must not run the home-only database work its role is denied.

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
)

// TestNodeStartStalledHome: startNode's role check had a 15 s context that lib/pq
// never applied to the SSL negotiation or startup, so a stalled home held the api
// before any listener bound, with no log line. The node's DATABASE_URL now carries a
// connect_timeout.
func TestNodeStartStalledHome(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(io.Discard, c) }()
		}
	}()
	prev := pgdsn.ConnectTimeout
	pgdsn.ConnectTimeout = time.Second
	t.Cleanup(func() { pgdsn.ConnectTimeout = prev })

	cfg := nodemode.Config{Enabled: true, NodeID: "1e943eda-0000-4000-8000-000000000001", HomeAPIURL: "http://home:8080"}
	t.Setenv("DATABASE_URL", "postgres://node_1e943eda:pw@"+ln.Addr().String()+"/maktaba?sslmode=prefer")
	boundNodeDatabaseURL(cfg)
	db, err := pgdsn.Open(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// without the bound the role check blocks inside lib/pq for good: wait for it here
	done := make(chan error, 1)
	go func() {
		_, err := startNode(context.Background(), cfg, db, noopLogger())
		done <- err
	}()
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("role check still blocked after 5 s against a home that never answers: the node's DATABASE_URL has no connect_timeout (pgdsn.ConnectTimeout %v)", pgdsn.ConnectTimeout)
	}
	if !errors.Is(err, nodemode.ErrDDLRole) {
		t.Fatalf("startNode = %v, want the role check to fail closed", err)
	}

	// the home's own database is left as it is
	t.Setenv("DATABASE_URL", "postgres://maktaba@127.0.0.1/maktaba?sslmode=disable")
	boundNodeDatabaseURL(nodemode.Config{})
	if got := os.Getenv("DATABASE_URL"); got != "postgres://maktaba@127.0.0.1/maktaba?sslmode=disable" {
		t.Fatalf("home DATABASE_URL changed: %q", got)
	}
}

// TestNodeSkipsHomeOnlyTables: the node's role is denied idempotency_keys (slot 0103)
// and writing health_samples (0119); the Postgres idempotency store warned every
// sweep and turned idempotency off on the node's own routes, the crash recorder
// dropped every event.
func TestNodeSkipsHomeOnlyTables(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://node_1e943eda:pw@127.0.0.1:1/maktaba?sslmode=disable")
	s, _, closeFn := buildIdempotencyStore(noopLogger(), true)
	defer closeFn()
	if _, ok := s.(*idempotency.MemoryStore); !ok {
		t.Fatalf("node idempotency store is %T, want the in-memory one", s)
	}
	s, _, closeHome := buildIdempotencyStore(noopLogger(), false)
	defer closeHome()
	if _, ok := s.(*idempotency.MemoryStore); ok {
		t.Fatal("the home lost its Postgres idempotency store")
	}

	db, err := pgdsn.Open(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if r := processRecorder("http://127.0.0.1:9103", db, true, noopLogger()); r != nil {
		t.Fatal("a full node records crashes into the home's health_samples")
	}
	if r := processRecorder("http://127.0.0.1:9103", db, false, noopLogger()); r == nil {
		t.Fatal("the home stopped recording crashes")
	}
}
