//go:build integration

package privacy

import (
	"context"
	neturl "net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// Against Postgres (MAKTABA_CLOUD_DB_URL): a self-deleted account whose
// hold has elapsed is purged together with its push logs; an account
// whose deletion was cancelled keeps both.
func TestPurgeDueRemovesPushLogs(t *testing.T) {
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// CI points MAKTABA_CLOUD_DB_URL at the database the api's own
	// integration tests migrate, whose users table is the api's (its
	// CREATE TABLE IF NOT EXISTS wins). The cloud schema gets a scratch
	// database of its own, dropped afterwards.
	pool := scratchCloudDB(ctx, t, url)
	if err := db.NewMigrator(pool, migrations.FS, ".").Up(ctx); err != nil {
		t.Fatal(err)
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	newUser := func(email string) string {
		t.Helper()
		var id string
		if err := pool.DB.QueryRowContext(ctx,
			`INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			bg := context.Background()
			_, _ = pool.DB.ExecContext(bg, `DELETE FROM push_dispatch_log WHERE user_id = $1`, id)
			_, _ = pool.DB.ExecContext(bg, `DELETE FROM users WHERE id = $1`, id)
		})
		if _, err := pool.DB.ExecContext(ctx, `INSERT INTO push_dispatch_log (user_id, platform, status)
            VALUES ($1, 'ios', 'sent'), ($1, 'web', 'failed')`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	gone := newUser("purge-gone-" + stamp + "@example.test")
	kept := newUser("purge-kept-" + stamp + "@example.test")
	if _, err := pool.DB.ExecContext(ctx, `
        INSERT INTO account_deletions (user_id, requested_at, purge_after, cancelled_at) VALUES
            ($1, now() - INTERVAL '31 days', now() - INTERVAL '1 day', NULL),
            ($2, now() - INTERVAL '31 days', now() - INTERVAL '1 day', now() - INTERVAL '20 days')`,
		gone, kept); err != nil {
		t.Fatal(err)
	}

	res, err := NewAccountPurger(pool.DB).PurgeDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 || res.Purged < 1 {
		t.Fatalf("purge: %+v", res)
	}
	count := func(q, id string) int {
		t.Helper()
		var n int
		if err := pool.DB.QueryRowContext(ctx, q, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM users WHERE id = $1`, gone); n != 0 {
		t.Fatal("purged user still there")
	}
	if n := count(`SELECT count(*) FROM push_dispatch_log WHERE user_id = $1`, gone); n != 0 {
		t.Fatalf("purged user's push logs left behind: %d", n)
	}
	if n := count(`SELECT count(*) FROM users WHERE id = $1`, kept); n != 1 {
		t.Fatal("cancelled deletion purged the user")
	}
	if n := count(`SELECT count(*) FROM push_dispatch_log WHERE user_id = $1`, kept); n != 2 {
		t.Fatalf("cancelled deletion lost push logs: %d", n)
	}
}

// scratchCloudDB creates an empty database next to the one url names and
// returns a pool on it; the test's cleanup closes the pool and drops it.
func scratchCloudDB(ctx context.Context, t *testing.T, url string) *db.Pool {
	t.Helper()
	admin, err := db.Open(url, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "cloud_purge_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := admin.DB.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := db.Open(u.String(), 4, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_, _ = admin.DB.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})
	return pool
}
