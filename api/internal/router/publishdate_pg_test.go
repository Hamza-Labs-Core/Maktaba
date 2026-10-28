package router_test

// A video's publish date is its YouTube upload date (slot 0164), else its
// mtime, against a real migrated Postgres behind the production router: the
// hub's years, the year filter, the sort by publish date and the published_at
// the lists answer; the triggers that keep it in step with the matches and the
// upload dates; and the slot's backfill, which takes the highest-confidence
// match. Runs when MAKTABA_TEST_PG_URL is set (see access_pg_test.go).

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPGPublishDateIsTheUploadDate(t *testing.T) {
	e, count := setupAccess(t) // acVidA1 in library A, its file written now
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	now := time.Now().UTC()
	// a2: a 2023 file with no YouTube match
	a2 := uuid.NewString()
	exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, created_at, updated_at)
		VALUES ($1,$2,$3,$4,encode(sha256(convert_to($7::text,'UTF8')),'hex'),1,$5,'ready','a2',60,$6,$6)`,
		a2, acLibA, "/media/ac/"+a2+".mp4", a2+".mp4", time.Date(2023, 5, 1, 12, 0, 0, 0, time.UTC), now, a2)
	// acVidA1 came out in 2015 (its best match); a weaker match says 2010
	exec(`INSERT INTO youtube_videos (youtube_id, title, upload_date, date_quality) VALUES
		('pd-best', 'best', '2015-06-01', 'exact'), ('pd-low', 'low', '2010-01-01', 'approximate')`)
	exec(`INSERT INTO media_metadata_enrichment (video_id, provider, external_id, confidence) VALUES
		($1, 'youtube', 'pd-low', 0.5), ($1, 'youtube', 'pd-best', 0.9), ($1, 'web', 'pd-low', 1.0)`, acVidA1)

	year := now.Year()
	// the hub's years row, as "year:videos"
	yc := func(y, n int) string { return fmt.Sprintf("%d:%d", y, n) }
	years := func() []string {
		t.Helper()
		hub := e.want(http.StatusOK, "member", "GET", "/api/libraries/"+acLibA+"/hub", nil)
		var out []string
		for _, y := range hub["rows"].(map[string]any)["years"].([]any) {
			m := y.(map[string]any)
			out = append(out, yc(int(m["year"].(float64)), int(m["videos"].(float64))))
		}
		return out
	}
	list := func(qs url.Values) ([]string, []string) {
		t.Helper()
		qs.Set("library", acLibA)
		out := e.want(http.StatusOK, "member", "GET", "/api/videos?"+qs.Encode(), nil)
		var dates []string
		for _, it := range out["items"].([]any) {
			d, _ := it.(map[string]any)["published_at"].(string)
			dates = append(dates, d)
		}
		return itemIDs(out, "id"), dates
	}
	check := func(what string, got []string, want ...string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", what, got, want)
		}
	}

	// the triggers set it as the matches came in: the best one's date
	check("years", years(), yc(2023, 1), yc(2015, 1))
	ids, dates := list(url.Values{"sort": {"published_at"}, "order": {"desc"}})
	check("by publish date", ids, a2, acVidA1)
	if len(dates) == 2 && !strings.HasPrefix(dates[1], "2015-06-01T00:00:00") {
		t.Errorf("published_at %q, want the upload date at midnight UTC", dates[1])
	}
	ids, _ = list(url.Values{"year": {"2015"}})
	check("year of the upload date", ids, acVidA1)
	ids, _ = list(url.Values{"year": {strconv.Itoa(year)}})
	check("year of the file", ids)
	ids, _ = list(url.Values{"year": {"2023"}})
	check("year of an mtime, no upload date", ids, a2)
	// the home's cards say the same
	home := e.want(http.StatusOK, "member", "GET", "/api/home", nil)
	for _, c := range home["recently_added"].([]any) {
		m := c.(map[string]any)
		if m["id"] == acVidA1 && !strings.HasPrefix(m["published_at"].(string), "2015-06-01") {
			t.Errorf("home card published_at %v", m["published_at"])
		}
	}

	// an upload date corrected; a match outranked; removed
	exec(`UPDATE youtube_videos SET upload_date = '2014-03-03' WHERE youtube_id = 'pd-best'`)
	check("date corrected", years(), yc(2023, 1), yc(2014, 1))
	exec(`UPDATE media_metadata_enrichment SET confidence = 0.95 WHERE external_id = 'pd-low' AND provider = 'youtube'`)
	check("outranked", years(), yc(2023, 1), yc(2010, 1))
	exec(`DELETE FROM media_metadata_enrichment WHERE video_id = $1 AND provider = 'youtube'`, acVidA1)
	check("matches removed", years(), yc(year, 1), yc(2023, 1))
	// the API's import names the video "youtube:<id>"; its date comes later
	exec(`INSERT INTO media_metadata_enrichment (video_id, provider, external_id, confidence) VALUES ($1, 'youtube', 'youtube:pd-late', 1.0)`, a2)
	check("match without a date", years(), yc(year, 1), yc(2023, 1))
	exec(`INSERT INTO youtube_videos (youtube_id, title, upload_date) VALUES ('pd-late', 'late', '2016-02-02')`)
	check("date learned", years(), yc(year, 1), yc(2016, 1))
	exec(`DELETE FROM youtube_videos WHERE youtube_id = 'pd-late'`)
	check("date gone", years(), yc(year, 1), yc(2023, 1))

	// a video deleted with its match: the cascade's trigger finds no row
	exec(`INSERT INTO media_metadata_enrichment (video_id, provider, external_id, confidence) VALUES ($1, 'youtube', 'pd-best', 0.9)`, a2)
	exec(`DELETE FROM videos WHERE id = $1`, a2)
	check("video deleted", years(), yc(year, 1))

	// slot 0164's backfill (the slot again: every statement is idempotent): the
	// highest-confidence match's date, whatever was there
	exec(`INSERT INTO media_metadata_enrichment (video_id, provider, external_id, confidence) VALUES
		($1, 'youtube', 'pd-low', 0.2), ($1, 'youtube', 'pd-best', 0.7)`, acVidA1)
	exec(`ALTER TABLE videos DISABLE TRIGGER USER`)
	exec(`UPDATE videos SET published_at = NULL`)
	exec(`ALTER TABLE videos ENABLE TRIGGER USER`)
	if n := count(`SELECT count(*) FROM videos WHERE published_at IS NOT NULL`); n != 0 {
		t.Fatalf("%d videos keep a date before the backfill", n)
	}
	up, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "db", "migrations", "0164_videos_published_at.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range strings.Split(strings.Split(string(up), "-- +goose Down")[0], "-- +goose StatementBegin") {
		stmt = strings.TrimSpace(strings.Replace(stmt, "-- +goose StatementEnd", "", 1))
		if stmt != "" && !strings.HasPrefix(stmt, "-- +goose NO TRANSACTION") {
			exec(stmt) // one at a time: CREATE INDEX CONCURRENTLY runs outside a transaction
		}
	}
	if n := count(`SELECT count(*) FROM videos WHERE id = $1 AND published_at = '2014-03-03T00:00:00Z'`, acVidA1); n != 1 {
		t.Errorf("backfill: not the highest-confidence match's date")
	}
	if n := count(`SELECT count(*) FROM videos WHERE published_at IS NOT NULL`); n != 1 {
		t.Errorf("backfill: %d videos dated, want only the one with a match", n)
	}
}
