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
