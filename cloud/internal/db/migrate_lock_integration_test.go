//go:build integration

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// Two processes migrating one database at once (blue and green starting
// together) serialize on the advisory lock: both succeed.
func TestUpConcurrently(t *testing.T) {
	_, url := dbtest.MigratedURL(t)
	ctx := context.Background()
	pool, err := db.Open(url, 8, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id = 500001`); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- db.NewMigrator(pool, migrations.FS, ".").Up(ctx) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Up: %v", err)
		}
	}
}
