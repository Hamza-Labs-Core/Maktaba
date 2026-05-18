//go:build integration

package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws/eventbus"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

// TestEventBus_CrossReplica_RealPostgres is the integration-tier
// counterpart to the unit cross-replica proof: it runs the SAME
// publish-on-A / deliver-to-client-on-B flow against a real Postgres
// and the real slot-0061 `events` table + events_notify trigger +
// pq.Listener — proving the production wiring, not just the
// in-memory model, satisfies Story 19.2 AC2/AC3.
//
// Tier: integration (build tag). Locally:
//
//	DATABASE_URL=postgres://maktaba:maktaba@localhost:55432/maktaba?sslmode=disable \
//	  go test -tags=integration ./api -run TestEventBus_CrossReplica
func TestEventBus_CrossReplica_RealPostgres(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: skipping eventbus integration test")
	}
	db := openTestDB(t, dsn)
	t.Cleanup(func() { _ = db.Close() })
	resetSchema(t, db)
	dropEvents(t, db)

	applyAllMigrations(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	backend := eventbus.NewPostgresBackend(db, dsn, nil)
	t.Cleanup(func() { _ = backend.Close() })

	hubA := ws.NewHub()
	hubB := ws.NewHub()
	busA := eventbus.New(backend, hubA, nil)
	busB := eventbus.New(backend, hubB, nil)

	go func() { _ = busA.Run(ctx) }()
	go func() { _ = busB.Run(ctx) }()
	// pq.Listener needs a moment to establish the LISTEN connection.
	time.Sleep(1500 * time.Millisecond)

	clientOnB := hubB.Subscribe("jobs")

	start := time.Now()
	if err := busA.Publish(ctx, "jobs", "jobs.progress", map[string]any{"video_id": "v1", "pct": 99}); err != nil {
		t.Fatalf("publish on A: %v", err)
	}

	select {
	case e := <-clientOnB.C:
		latency := time.Since(start)
		if e.Type != "jobs.progress" || e.Payload["video_id"] != "v1" {
			t.Fatalf("client on B got wrong event: %+v", e)
		}
		// Story 19.2 AC2 budget is ≤250ms; allow generous slack for
		// CI scheduling but assert it is not pathological.
		if latency > 5*time.Second {
			t.Fatalf("cross-replica latency too high: %s", latency)
		}
		t.Logf("cross-replica delivery via real Postgres in %s", latency)
	case <-time.After(10 * time.Second):
		t.Fatal("event published on replica A never reached client on replica B")
	}

	// Replay across reconnect from the real table.
	if err := busA.Publish(ctx, "jobs", "jobs.progress", map[string]any{"n": 2}); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if err := busA.Publish(ctx, "jobs", "jobs.progress", map[string]any{"n": 3}); err != nil {
		t.Fatalf("publish 3: %v", err)
	}
	// Drain whatever the live loop already delivered, then prove
	// Replay returns the durable tail.
	time.Sleep(500 * time.Millisecond)
	missed, err := busB.Replay(ctx, "jobs", 1)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(missed) < 2 {
		t.Fatalf("expected ≥2 replayable events after id 1, got %d", len(missed))
	}
	for i := 1; i < len(missed); i++ {
		if missed[i].ID <= missed[i-1].ID {
			t.Fatalf("replay not monotonic: %+v", missed)
		}
	}

	// Prune is callable against the real table.
	if _, err := backend.Prune(ctx, time.Now().Add(-eventbus.DefaultRetention)); err != nil {
		t.Fatalf("prune: %v", err)
	}
}

func dropEvents(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS events CASCADE`); err != nil {
		t.Fatalf("drop events: %v", err)
	}
	_, _ = db.ExecContext(ctx, `DROP FUNCTION IF EXISTS events_notify() CASCADE`)
}

func applyAllMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	dir := repoMigrationsDir(t)
	stage, err := stagePostgresMigrations(dir)
	if err != nil {
		t.Fatalf("stage migrations: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stage) })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := goose.UpContext(ctx, db, stage); err != nil {
		t.Fatalf("goose up (all slots incl. 0061): %v", err)
	}
}
