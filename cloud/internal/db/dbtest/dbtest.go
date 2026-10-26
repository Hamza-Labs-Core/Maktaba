// Package dbtest gives an integration test a migrated cloud database of
// its own (MAKTABA_CLOUD_DB_URL names the server): CI's shared database
// already holds the home api's tables (its users among them), which the
// cloud's migrations must not meet.
package dbtest

import (
	"context"
	neturl "net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

var seq atomic.Int64

// Migrated makes a scratch database, migrates it and drops it after the
// test; it skips the test when MAKTABA_CLOUD_DB_URL is unset.
func Migrated(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := db.Open(url, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "cloud_t_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + strconv.FormatInt(seq.Add(1), 10)
	if _, err := admin.DB.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := db.Open(u.String(), 8, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_, _ = admin.DB.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})
	if err := db.NewMigrator(pool, migrations.FS, ".").Up(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}
