//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// TestUpAppliesALowerSlotLate: an epic's migration numbered below the database's version
// (Epic 50's 0050 landing after Epic 51's 0051 was applied) is still applied by Up.
func TestUpAppliesALowerSlotLate(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	// the database as it was before 0050 shipped: 0051 applied, 0050 not
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM reserved_slugs WHERE reason = 'infrastructure'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id = 500001`); err != nil {
		t.Fatal(err)
	}
	if err := db.NewMigrator(pool, migrations.FS, ".").Up(ctx); err != nil {
		t.Fatalf("Up with a lower slot missing: %v", err)
	}
	var n int
	if err := pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM reserved_slugs WHERE slug IN ('canary','r42')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("reserved infra slugs after a late Up = %d, want 2", n)
	}
}

// A database that had a later slot when a lower one landed is at head once Up applied it (v0.3.163 was
// never ready on such a database: goose's current version is the one applied last), and only then.
func TestAtHeadWithALowerSlotAppliedLate(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	m := db.NewMigrator(pool, migrations.FS, ".")
	at := func(want bool, when string) {
		t.Helper()
		if got, err := m.AtHead(ctx); err != nil || got != want {
			t.Fatalf("%s: at head %v %v, want %v", when, got, err, want)
		}
	}
	at(true, "migrated in order")
	// as the release before the late one left it
	if _, err := pool.DB.ExecContext(ctx, `ALTER TABLE server_claims DROP COLUMN retry_key_hash`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id = 40002`); err != nil {
		t.Fatal(err)
	}
	at(false, "a lower slot missing")
	if err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	at(true, "the lower slot applied last")
	// a schema ahead of the binary (a release that went back and kept it) is at head for it
	if _, err := pool.DB.ExecContext(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (99999999, true)`); err != nil {
		t.Fatal(err)
	}
	at(true, "a later release's migration beside its own")
	if _, err := pool.DB.ExecContext(ctx, `DROP TABLE goose_db_version CASCADE`); err != nil {
		t.Fatal(err)
	}
	at(false, "a database with no migrations")
}
