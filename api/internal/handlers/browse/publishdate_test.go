package browse

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A video's publish date is its YouTube upload date (slot 0164), else its
// mtime: the hub's years and the cards' published_at follow it, and the
// SQLite triggers keep it in step with the matches and the upload dates.
func TestPublishDateIsTheUploadDate(t *testing.T) {
	e := newEnv(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`CREATE TABLE media_metadata_enrichment (id TEXT PRIMARY KEY, video_id TEXT NOT NULL, provider TEXT NOT NULL,
		external_id TEXT NOT NULL, mapped TEXT NOT NULL DEFAULT '{}', confidence REAL NOT NULL DEFAULT 0,
		is_accepted INTEGER NOT NULL DEFAULT 0, fetched_at TEXT, created_at TEXT, UNIQUE (video_id, provider, external_id))`)
	exec(`CREATE TABLE youtube_videos (youtube_id TEXT PRIMARY KEY, channel_id TEXT, title TEXT NOT NULL,
		upload_date TEXT, date_quality TEXT)`)
	// a2 (a 2024 file) has two matches before the slot: the best one came out in
	// 2015; a web match is not a YouTube one
	exec(`INSERT INTO youtube_videos (youtube_id, title, upload_date, date_quality) VALUES
		('yt-best', 'best', '2015-06-01', 'exact'), ('yt-low', 'low', '2010-01-01', 'approximate')`)
	exec(`INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, confidence, created_at) VALUES
		('m1', $1, 'youtube', 'yt-low', 0.5, '2026-01-01T00:00:00.000Z'),
		('m2', $1, 'youtube', 'yt-best', 0.9, '2026-01-01T00:00:00.000Z'),
		('m3', $2, 'web', 'yt-low', 1.0, '2026-01-01T00:00:00.000Z')`, v("a2"), v("a1"))
	execMigrationWithColumn(t, e, "0164_videos_published_at.sqlite.sql")

	years := func() []YearCount {
		t.Helper()
		var h hubBody
		if code := e.do(asViewer(), "GET", "/api/libraries/"+libA+"/hub", nil, &h); code != 200 {
			t.Fatalf("hub: %d", code)
		}
		return h.Rows.Years
	}
	published := func(name string) string {
		t.Helper()
		var h hubBody
		e.do(asViewer(), "GET", "/api/libraries/"+libA+"/hub", nil, &h)
		for _, c := range h.Rows.RecentlyAdded {
			if c.ID == v(name) && c.PublishedAt != nil {
				return c.PublishedAt.UTC().Format(time.DateOnly)
			}
		}
		t.Fatalf("%s: no card with a published_at", name)
		return ""
	}
	check := func(when string, want []YearCount) {
		t.Helper()
		if got := years(); !slices.Equal(got, want) {
			t.Errorf("%s: years %+v, want %+v", when, got, want)
		}
	}

	// the backfill took the highest-confidence match; a1 and a3 keep their mtime
	check("backfill", []YearCount{{2024, 1}, {2023, 1}, {2015, 1}})
	if got := published("a2"); got != "2015-06-01" {
		t.Errorf("a2 published %s, want the best match's 2015-06-01", got)
	}
	if got := published("a1"); got != "2024-03-01" {
		t.Errorf("a1 published %s, want its mtime", got)
	}

	// a match added (the API's "youtube:<id>" form) before its date is known,
	// then the date learned
	exec(`INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, confidence) VALUES ('m4', $1, 'youtube', 'youtube:yt-a1', 1.0)`, v("a1"))
	check("match without a date", []YearCount{{2024, 1}, {2023, 1}, {2015, 1}})
	exec(`INSERT INTO youtube_videos (youtube_id, title, upload_date) VALUES ('yt-a1', 'a1', '2016-02-02')`)
	check("date learned", []YearCount{{2023, 1}, {2016, 1}, {2015, 1}})

	// an upload date corrected; a match outranked
	exec(`UPDATE youtube_videos SET upload_date = '2014-01-01' WHERE youtube_id = 'yt-best'`)
	check("date corrected", []YearCount{{2023, 1}, {2016, 1}, {2014, 1}})
	exec(`UPDATE media_metadata_enrichment SET confidence = 0.95 WHERE id = 'm1'`)
	check("outranked", []YearCount{{2023, 1}, {2016, 1}, {2010, 1}})

	// a match removed: back to the mtime
	exec(`DELETE FROM media_metadata_enrichment WHERE id = 'm4'`)
	check("match removed", []YearCount{{2024, 1}, {2023, 1}, {2010, 1}})
}

// execMigrationWithColumn runs a SQLite twin whose videos column the test
// schema already has (raw SQLite has no ADD COLUMN IF NOT EXISTS; the
// migrator skips it: TestSQLiteMigrate_AppliesEveryTwinToAFreshDatabase).
func execMigrationWithColumn(t *testing.T, e *env, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "db", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(b), "-- +goose Down")
	var keep []string
	for _, ln := range strings.Split(up, "\n") {
		if !strings.HasPrefix(ln, "ALTER TABLE videos ADD COLUMN IF NOT EXISTS") {
			keep = append(keep, ln)
		}
	}
	if _, err := e.db.Exec(strings.Join(keep, "\n")); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}
