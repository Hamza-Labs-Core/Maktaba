package speakers

// #1483: the speakers hook's query runs on a SQLite-backed API too (it was Postgres-only:
// ::text and = ANY).

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	_ "modernc.org/sqlite"
)

func TestVideosOfOnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, st := range []string{
		`CREATE TABLE transcripts (id TEXT PRIMARY KEY, video_id TEXT NOT NULL, is_active INTEGER NOT NULL)`,
		`CREATE TABLE transcript_segments (id INTEGER PRIMARY KEY, transcript_id TEXT NOT NULL)`,
		`CREATE TABLE segment_speakers (segment_id INTEGER NOT NULL, speaker_id TEXT NOT NULL)`,
		`INSERT INTO transcripts VALUES ('t1', 'v1', 1), ('t2', 'v2', 1), ('t3', 'v3', 0)`,
		`INSERT INTO transcript_segments VALUES (1, 't1'), (2, 't1'), (3, 't2'), (4, 't3')`,
		`INSERT INTO segment_speakers VALUES (1, 's1'), (2, 's1'), (3, 's2'), (4, 's1')`,
	} {
		if _, err := db.Exec(st); err != nil {
			t.Fatal(err)
		}
	}
	got, err := videosOf(context.Background(), db, "s1", "s2")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	// v3's transcript is not active: left out; v1 once though two of its lines are s1's
	if !reflect.DeepEqual(got, []string{"v1", "v2"}) {
		t.Fatalf("videos %v", got)
	}
	if got, err := videosOf(context.Background(), db); err != nil || got != nil {
		t.Fatalf("no speakers: %v %v", got, err)
	}
}
