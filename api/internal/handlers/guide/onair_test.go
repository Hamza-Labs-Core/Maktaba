package guide

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestOnAirFromChannelRuntime(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`CREATE TABLE channel_runtime (channel_id TEXT PRIMARY KEY, state TEXT NOT NULL,
		heartbeat_at TEXT, last_segment_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	fresh := now.Add(-10 * time.Second).Format(time.RFC3339)
	old := now.Add(-5 * time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO channel_runtime VALUES ('A','running',$1,NULL), ('B','idle',$1,NULL),
		('C','running',$2,NULL), ('D','running',NULL,$1)`, fresh, old); err != nil {
		t.Fatal(err)
	}
	got := (&Handler{DB: db}).onAir(context.Background(), now)
	if !got["a"] || got["b"] || got["c"] || !got["d"] || len(got) != 2 {
		t.Fatalf("on air %v", got)
	}
	// no table: nothing on air, no error
	db2, _ := sql.Open("sqlite", filepath.Join(t.TempDir(), "e.db"))
	defer db2.Close()
	if n := len((&Handler{DB: db2}).onAir(context.Background(), now)); n != 0 {
		t.Fatalf("no table: %d", n)
	}
}
