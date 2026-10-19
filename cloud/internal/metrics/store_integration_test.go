//go:build integration

package metrics

import (
	"context"
	"os"
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
	pool, err := db.Open(url, 4, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	// integration packages run in parallel against one database; a
	// migration that loses the race to another package succeeds on retry
	mig := db.NewMigrator(pool, migrations.FS, ".")
	for attempt := 1; ; attempt++ {
		err := mig.Up(ctx)
		if err == nil {
			break
		}
		if attempt == 5 {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
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
