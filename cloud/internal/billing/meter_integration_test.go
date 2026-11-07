//go:build integration

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Close makes the last flush before it returns: the relay's shutdown must
// not lose the bytes counted since the last tick.
func TestMeterCloseFlushes(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	u, err := stores.NewUsers(pool.DB).Create(ctx, uuid.NewString()+"@example.test", "", "M", true)
	if err != nil {
		t.Fatal(err)
	}
	var sv stores.Server
	if err := pool.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash)
	        VALUES ($1, 'm', $2, 'x') RETURNING id`, u.ID, "m"+uuid.NewString()[:8]).Scan(&sv.ID); err != nil {
		t.Fatal(err)
	}
	m := NewMeter(pool.DB)
	m.FlushInterval = time.Hour // only Close flushes
	m.Start(ctx)
	m.Record(ctx, sv, Decision{Mode: ModeFull}, 1000, 5_862_038)
	m.Close()
	got, err := m.MonthlyBytes(ctx, sv.ID, MonthStart(time.Now().UTC()))
	if err != nil || got != 5_863_038 {
		t.Fatalf("after Close: %d bytes, err %v", got, err)
	}
	// Close of a meter never started returns at once
	done := make(chan struct{})
	go func() { NewMeter(pool.DB).Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close of an unstarted meter blocked")
	}
}

// Story 50.19 T06: bytes are counted by mode into relay_usage_monthly (and in total into
// bandwidth_monthly), and Decide reads them back: a Basic home past fair use is capped_slow.
func TestMeterCountsByMode(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	u, err := stores.NewUsers(pool.DB).Create(ctx, uuid.NewString()+"@example.test", "", "M", true)
	if err != nil {
		t.Fatal(err)
	}
	sv := stores.Server{Plan: PlanBasic}
	if err := pool.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan)
	        VALUES ($1, 'm', $2, 'x', 'basic') RETURNING id`, u.ID, "m"+uuid.NewString()[:8]).Scan(&sv.ID); err != nil {
		t.Fatal(err)
	}
	m := NewMeter(pool.DB)
	m.FlushInterval = time.Hour
	m.Start(ctx)
	if d := m.Decide(ctx, sv); d.Mode != ModeCapped || d.CapKbps != 3000 {
		t.Fatalf("a new Basic home: %+v", d)
	}
	m.Record(ctx, sv, Decision{Mode: ModeFull}, 10, 90)
	m.Record(ctx, sv, Decision{Mode: ModeFull, PAYG: true}, 0, 200)
	m.Record(ctx, sv, Decision{Mode: ModeCapped}, 0, 500*GB)
	m.Record(ctx, sv, Decision{Mode: ModeCappedSlow}, 0, 7)
	// before the flush: Decide sees the bytes in memory
	if d := m.Decide(ctx, sv); d.Mode != ModeCappedSlow || d.CapKbps != 1500 {
		t.Fatalf("past fair use, unflushed: %+v", d)
	}
	m.Close()
	got, err := m.MonthlyUsage(ctx, sv.ID, MonthStart(time.Now().UTC()))
	if err != nil || got != (Usage{Full: 100, PAYG: 200, Capped: 500 * GB, CappedSlow: 7}) {
		t.Fatalf("relay_usage_monthly %+v %v", got, err)
	}
	if total, _ := m.MonthlyBytes(ctx, sv.ID, MonthStart(time.Now().UTC())); total != got.Total() {
		t.Fatalf("bandwidth_monthly %d, by mode %d", total, got.Total())
	}
	// a fresh meter reads the month back from the database
	if d := NewMeter(pool.DB).Decide(ctx, sv); d.Mode != ModeCappedSlow {
		t.Fatalf("read back: %+v", d)
	}
}

// Story 50.19 T09: the rename moves pro to basic and family to pro in users, servers and
// subscriptions, no row twice, and the CHECKs take the new names (and roll back).
func TestPlanNamesMigration(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	mig := db.NewMigrator(pool, migrations.FS, ".")
	before := func() { // down to 00500006, below the rename (later slots, 0051, go down too)
		goose.SetBaseFS(migrations.FS)
		if err := goose.SetDialect("postgres"); err != nil {
			t.Fatal(err)
		}
		if err := goose.DownToContext(ctx, pool.DB, ".", 500006); err != nil {
			t.Fatal(err)
		}
	}
	before()
	users := stores.NewUsers(pool.DB)
	ids := map[string]string{}
	for _, plan := range []string{"free", "pro", "family"} {
		u, err := users.Create(ctx, uuid.NewString()+"@example.test", "", plan, true)
		if err != nil {
			t.Fatal(err)
		}
		ids[plan] = u.ID
		if _, err := pool.DB.ExecContext(ctx, `UPDATE users SET plan = $2 WHERE id = $1`, u.ID, plan); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.DB.ExecContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan)
		        VALUES ($1, 'h', $2, 'x', $3)`, u.ID, "h"+uuid.NewString()[:8], plan); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.DB.ExecContext(ctx, `INSERT INTO subscriptions (user_id, plan) VALUES ($1, $2)`, u.ID, plan); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.DB.ExecContext(ctx, `UPDATE users SET plan = 'basic' WHERE id = $1`, ids["free"]); err == nil {
		t.Fatal("basic accepted before the migration")
	}
	if err := mig.Up(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"free": "free", "pro": "basic", "family": "pro"}
	for old, id := range ids {
		for _, q := range []string{
			`SELECT plan FROM users WHERE id = $1`,
			`SELECT plan FROM servers WHERE owner_user_id = $1`,
			`SELECT plan FROM subscriptions WHERE user_id = $1`,
		} {
			var got string
			if err := pool.DB.QueryRowContext(ctx, q, id).Scan(&got); err != nil || got != want[old] {
				t.Fatalf("%s (was %s): %q %v", q, old, got, err)
			}
		}
	}
	if _, err := pool.DB.ExecContext(ctx, `UPDATE servers SET plan = 'basic' WHERE owner_user_id = $1`, ids["free"]); err != nil {
		t.Fatalf("basic refused after the migration: %v", err)
	}
	if _, err := pool.DB.ExecContext(ctx, `UPDATE servers SET plan = 'gold' WHERE owner_user_id = $1`, ids["free"]); err == nil {
		t.Fatal("an unknown plan accepted")
	}
	// and back: basic is pro again, pro is family again
	if _, err := pool.DB.ExecContext(ctx, `UPDATE servers SET plan = 'free' WHERE owner_user_id = $1`, ids["free"]); err != nil {
		t.Fatal(err)
	}
	before()
	var back string
	if err := pool.DB.QueryRowContext(ctx, `SELECT plan FROM users WHERE id = $1`, ids["family"]).Scan(&back); err != nil || back != "family" {
		t.Fatalf("down: %q %v", back, err)
	}
}
