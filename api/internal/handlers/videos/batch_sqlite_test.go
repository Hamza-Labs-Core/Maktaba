package videos

// The batch readers on SQLite, over the shipped .sqlite.sql twins of the
// tables they read. The twins keep timestamps as TEXT, which the video,
// transcript and playback rows scan into time.Time only on Postgres, so
// those three readers are covered by the Postgres GraphQL tests
// (internal/graphql/fields_pg_test.go) and the rest run here.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

var addColumnIfNotExists = regexp.MustCompile(`(?i)ADD COLUMN IF NOT EXISTS`)

// upStatements returns the Up statements of a twin.
func upStatements(t *testing.T, name string) []string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "db", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	if i := strings.Index(up, "-- +goose Down"); i >= 0 {
		up = up[:i]
	}
	var out []string
	var block []string
	in := false
	var loose strings.Builder
	for _, line := range strings.Split(up, "\n") {
		s := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(s, "-- +goose StatementBegin"):
			in = true
			block = nil
		case strings.HasPrefix(s, "-- +goose StatementEnd"):
			in = false
			out = append(out, strings.Join(block, "\n"))
		case in:
			block = append(block, line)
		case strings.HasPrefix(s, "--"):
		default:
			loose.WriteString(line + "\n")
		}
	}
	for _, st := range strings.Split(loose.String(), ";") {
		if strings.TrimSpace(st) != "" {
			out = append(out, st)
		}
	}
	return out
}

func sqliteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, twin := range []string{
		"0001_init_libraries_and_videos.sqlite.sql",
		"0009_audio_tracks_extensions.sqlite.sql",
		"0012_transcripts_is_active.sqlite.sql",
		"0015_subtitle_files.sqlite.sql",
		"0032_chapters.sqlite.sql",
		"0034_tags.sqlite.sql",
		"0038_playback_state.sqlite.sql",
	} {
		for _, st := range upStatements(t, twin) {
			st = addColumnIfNotExists.ReplaceAllString(st, "ADD COLUMN")
			if _, err := db.Exec(st); err != nil && !strings.Contains(err.Error(), "duplicate column") {
				t.Fatalf("%s: %v\n%s", twin, err, st)
			}
		}
	}
	return db
}

func TestBatchReaders_SQLite(t *testing.T) {
	db := sqliteDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO libraries (id, name, roots) VALUES ('lib', 'L', '[]')`)
	for _, id := range []string{"v1", "v2"} {
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title)
			VALUES ($1, 'lib', $2, $2, $1, 10, '2026-01-01T00:00:00Z', 'ready', $1)`, id, "/m/"+id)
	}
	exec(`INSERT INTO tags (id, name, name_norm) VALUES ('t1', 'Fiqh', 'fiqh')`)
	exec(`INSERT INTO video_tags (video_id, tag_id) VALUES ('v1', 't1'), ('v2', 't1')`)
	exec(`INSERT INTO media_info (video_id, container, width, height, fps, bitrate_kbps) VALUES ('v1', 'mkv', 1280, 720, 30, 2500)`)
	exec(`INSERT INTO audio_tracks (video_id, track_index, codec, channels, language, detected_language, is_default) VALUES ('v1', 0, 'opus', 2, 'und', 'ara', 1)`)
	exec(`INSERT INTO subtitle_files (video_id, language, format, source, path) VALUES ('v2', 'en', 'srt', 'external', '/s.srt')`)
	exec(`INSERT INTO chapters (video_id, seq, start_sec, title) VALUES ('v1', 0, 0, 'Start')`)

	ids := []string{"v1", "v2", "v1", "missing"}
	tags, err := TagsByVideo(ctx, db, ids)
	if err != nil || len(tags["v1"]) != 1 || len(tags["v2"]) != 1 || tags["v1"][0].Name != "Fiqh" {
		t.Fatalf("TagsByVideo = %v, %v", tags, err)
	}
	mi, err := MediaInfoByVideo(ctx, db, ids)
	if err != nil || mi["v1"].Width == nil || *mi["v1"].Width != 1280 {
		t.Fatalf("MediaInfoByVideo = %v, %v", mi, err)
	}
	if _, ok := mi["v2"]; ok {
		t.Fatal("v2 was never probed")
	}
	at, err := AudioTracksByVideo(ctx, db, ids)
	if err != nil || len(at["v1"]) != 1 || *at["v1"][0].Language != "ara" || !at["v1"][0].Default {
		t.Fatalf("AudioTracksByVideo = %v, %v", at, err)
	}
	subs, err := SubtitlesByVideo(ctx, db, ids)
	if err != nil || len(subs["v2"]) != 1 || subs["v2"][0].URL != "/stream/subtitles/v2/en.srt" {
		t.Fatalf("SubtitlesByVideo = %v, %v", subs, err)
	}
	ch, err := ChaptersByVideo(ctx, db, ids)
	if err != nil || len(ch["v1"]) != 1 {
		t.Fatalf("ChaptersByVideo = %v, %v", ch, err)
	}
	if ps, err := PlaybackStates(ctx, db, "", ids); err != nil || len(ps) != 0 {
		t.Fatal("no user, no playback state")
	}
}

// More ids than one IN list holds are read in chunks.
func TestBatchReaders_Chunked(t *testing.T) {
	db := sqliteDB(t)
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO libraries (id, name, roots) VALUES ('lib', 'L', '[]')`)
	var ids []string
	for i := 0; i < batchSize+7; i++ {
		id := "v" + itoa(i)
		ids = append(ids, id)
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime) VALUES ($1, 'lib', $1, $1, $1, 1, '2026-01-01T00:00:00Z')`, id)
		exec(`INSERT INTO chapters (video_id, seq, start_sec, title) VALUES ($1, 0, 0, 'c')`, id)
	}
	ch, err := ChaptersByVideo(context.Background(), db, ids)
	if err != nil || len(ch) != len(ids) {
		t.Fatalf("got %d of %d: %v", len(ch), len(ids), err)
	}
}
