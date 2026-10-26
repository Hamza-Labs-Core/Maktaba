//go:build integration

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
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
	m.Record(ctx, sv, 1000, 5_862_038)
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
