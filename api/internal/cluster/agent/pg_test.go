package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Real Postgres tier: the library snapshot and the library roots against
// the migrated schema (runs only with MAKTABA_TEST_PG_URL).
func TestPGLibrarySnapshotAndRoots(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	var libA, libB string
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO libraries (name, roots) VALUES ('Channels', '{}') RETURNING id::text`).Scan(&libA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO libraries (name, roots) VALUES ('Courses', '{}') RETURNING id::text`).Scan(&libB); err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO library_roots (library_id, path, path_canonical) VALUES ($1, '/Volumes/Originals/Channels', '/volumes/originals/channels')`, libA)
	mustExec(`INSERT INTO library_roots (library_id, path, path_canonical) VALUES ($1, '/Volumes/Originals/Courses', '/volumes/originals/courses')`, libB)
	video := func(lib, hash string, dur float64, size int64, state string, deleted bool) string {
		t.Helper()
		var id string
		q := `INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, duration_sec, state, deleted_at)
		      VALUES ($1, $2, '/x/' || $2, $2, $3, now(), $4, $5, CASE WHEN $6 THEN now() END) RETURNING id::text`
		if err := db.QueryRowContext(ctx, q, lib, strings.Repeat(hash, 64), size, dur, state, deleted).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	v1 := video(libA, "a", 3600, 1000, "ready", false)
	video(libA, "b", 1800, 500, "probed", false)
	video(libA, "c", 9999, 9999, "missing", false) // not counted
	video(libA, "d", 9999, 9999, "ready", true)    // deleted: not counted
	video(libB, "e", 7200, 2000, "ready", false)
	var tag string
	if err := db.QueryRowContext(ctx, `INSERT INTO tags (name, name_norm) VALUES ('Fiqh', 'fiqh') RETURNING id::text`).Scan(&tag); err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO video_tags (video_id, tag_id) VALUES ($1, $2)`, v1, tag)
	mustExec(`INSERT INTO series (library_id, name) VALUES ($1, 'S')`, libA)

	snap, err := DBLibrary(db, time.Now)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Libraries) != 2 {
		t.Fatalf("libraries = %+v", snap.Libraries)
	}
	a := snap.Libraries[0]
	if a.Name != "Channels" || a.Videos != 2 || a.Hours != 1.5 || a.Bytes != 1500 || a.Tagged != 1 || a.Series != 1 ||
		a.TranscribedVideos != 0 || a.Indexed != 0 {
		t.Fatalf("Channels = %+v", a)
	}
	if snap.Totals.Videos != 3 || snap.Totals.Hours != 3.5 || snap.Totals.Bytes != 3500 {
		t.Fatalf("totals = %+v", snap.Totals)
	}

	roots, err := DBLibraryRoots(db)(ctx)
	if err != nil || len(roots) != 2 {
		t.Fatalf("roots = %+v %v", roots, err)
	}
	if got := ShareRootOf(roots); got != "/Volumes/Originals" {
		t.Fatalf("share root = %q", got)
	}
}
