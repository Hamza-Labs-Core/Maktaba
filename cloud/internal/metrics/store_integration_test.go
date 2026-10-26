//go:build integration

package metrics

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

// Against Postgres (MAKTABA_CLOUD_DB_URL): the api role reads the newest
// gauge sample the relay flushed, and a stale one reads as nothing
// connected (story 30.3).
func TestLatestGauges(t *testing.T) {
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// CI's MAKTABA_CLOUD_DB_URL is the database the home api's own
	// integration tests migrate, whose users table is the api's; the cloud
	// schema gets a scratch database of its own, dropped afterwards.
	pool := scratchCloudDB(ctx, t, url)
	if err := db.NewMigrator(pool, migrations.FS, ".").Up(ctx); err != nil {
		t.Fatal(err)
	}
	st := NewStore(pool.DB)
	// a far-future window keeps this test's rows apart from anything else
	now := time.Date(2099, 1, 1, 12, 0, 0, 0, time.UTC)
	t.Cleanup(func() {
		_, _ = pool.DB.ExecContext(context.Background(), `DELETE FROM relay_metrics_raw WHERE bucket >= $1`, now.Add(-time.Hour))
	})
	old := now.Add(-2 * time.Minute)
	c := NewCollector()
	c.now = func() time.Time { return old }
	c.ObserveConnections(4, 1)
	if err := st.FlushRaw(ctx, c.Snapshot()); err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return now }
	c.ObserveConnections(6, 2)
	c.ObserveConnections(6, 3) // two samples in the newest minute
	if err := st.FlushRaw(ctx, c.Snapshot()); err != nil {
		t.Fatal(err)
	}
	s, tu, ok, err := st.LatestGauges(ctx, now, LiveSampleMaxAge)
	if err != nil || !ok || s != 6 || tu != 3 {
		t.Fatalf("latest = %d/%d ok=%v err=%v, want 6/3 (2.5 rounds up)", s, tu, ok, err)
	}
	s, tu, ok, err = st.LatestGauges(ctx, now.Add(10*time.Minute), LiveSampleMaxAge)
	if err != nil || ok || s != 0 || tu != 0 {
		t.Fatalf("stale = %d/%d ok=%v err=%v, want 0/0 not ok", s, tu, ok, err)
	}
}

func scratchCloudDB(ctx context.Context, t *testing.T, url string) *db.Pool {
	t.Helper()
	admin, err := db.Open(url, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "cloud_metrics_" + strconv.FormatInt(time.Now().UnixNano(), 36)
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
