package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

func videosDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, path TEXT, duration_sec REAL)`,
		`INSERT INTO videos VALUES ('v1', 'lib1', 'smb://nas/Originals/a.mkv', 1800), ('v2', 'lib2', '/Volumes/Originals/b.mkv', NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestCatalogSyncKeepsAndReopens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "node", FileName)
	db := videosDB(t)
	c := Open(path, nil)
	if c.Len() != 0 {
		t.Fatalf("a new catalog: %d videos", c.Len())
	}
	if err := c.Sync(ctx, db, ""); err != nil {
		t.Fatal(err)
	}
	if v, ok := c.Video("v1"); !ok || v.LibraryID != "lib1" || v.Path != "smb://nas/Originals/a.mkv" || v.DurationSec != 1800 {
		t.Fatalf("v1: %+v %v", v, ok)
	}
	if v, ok := c.Video("v2"); !ok || v.DurationSec != 0 {
		t.Fatalf("v2 (no length): %+v %v", v, ok)
	}
	// a restart in an outage: the file answers
	again := Open(path, nil)
	if again.Len() != 2 || again.SyncedAt().IsZero() {
		t.Fatalf("reopened: %d videos, synced %v", again.Len(), again.SyncedAt())
	}
	if v, ok := again.Video("v1"); !ok || v.LibraryID != "lib1" {
		t.Fatalf("reopened v1: %+v %v", v, ok)
	}

	// an unchanged catalog is not written again
	before, _ := os.ReadFile(path)
	if err := c.Sync(ctx, db, ""); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("an unchanged catalog was rewritten")
	}

	// the home's database gone: a failed refresh keeps what the node has
	_ = db.Close()
	if err := c.Sync(ctx, db, ""); err == nil {
		t.Fatal("a sync on a closed database succeeded")
	}
	if c.Len() != 2 {
		t.Fatalf("after a failed sync: %d videos", c.Len())
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("a failed sync touched the file")
	}
}

func TestCatalogUnreadableFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Open(path, nil)
	if c.Len() != 0 {
		t.Fatalf("an unreadable file: %d videos", c.Len())
	}
	if err := c.Sync(context.Background(), videosDB(t), ""); err != nil || Open(path, nil).Len() != 2 {
		t.Fatalf("the next sync replaces it: %v", err)
	}
}

func TestWatch(t *testing.T) {
	db := videosDB(t)
	w := &Watch{DB: db}
	if w.Down() {
		t.Fatal("a new watch is down")
	}
	back := w.backCh()
	w.Failed(errors.New("dial tcp: connection refused"))
	if !w.Down() {
		t.Fatal("a failed call does not mark the database down")
	}
	w.Check(context.Background()) // the ping answers
	if w.Down() {
		t.Fatal("still down after a good ping")
	}
	select {
	case <-back:
	default:
		t.Fatal("the database answering again was not signalled")
	}
	_ = db.Close()
	w.Check(context.Background())
	if !w.Down() {
		t.Fatal("a failed ping does not mark the database down")
	}
	var nilWatch *Watch
	if nilWatch.Down() {
		t.Fatal("no watch is never down")
	}
}

// The share map comes from the node's own row (cluster_node_beat), as the API's share
// path resolver reads it.
func TestCatalogSyncShareMapPG(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	const node = "6f1c9a2e-0000-4000-8000-0000000000aa"
	if _, err := db.ExecContext(ctx, `INSERT INTO cluster_nodes (id, name, state, node_pubkey, db_role, path_map)
		VALUES ($1, 'win-box', 'approved', 'pub', session_user, '{"smb://nas/Originals/": "O:/"}'::jsonb)`, node); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO libraries (id, name, roots) VALUES ('11111111-0000-4000-8000-000000000001', 'lib', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, duration_sec)
		VALUES ('22222222-0000-4000-8000-000000000001', '11111111-0000-4000-8000-000000000001', repeat('a', 64), 'smb://nas/Originals/a.mkv', 'a.mkv', 1, now(), 60)`); err != nil {
		t.Fatal(err)
	}
	c := Open(filepath.Join(t.TempDir(), FileName), nil)
	if err := c.Sync(ctx, db, node); err != nil {
		t.Fatal(err)
	}
	if m := c.ShareMap(); m["smb://nas/Originals/"] != "O:/" {
		t.Fatalf("share map: %v", m)
	}
	if v, ok := c.Video("22222222-0000-4000-8000-000000000001"); !ok || v.LibraryID != "11111111-0000-4000-8000-000000000001" || v.DurationSec != 60 {
		t.Fatalf("video: %+v %v", v, ok)
	}
}
