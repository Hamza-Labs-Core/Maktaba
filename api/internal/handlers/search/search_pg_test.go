package search

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// pgSearchDB is a scratch, fully migrated Postgres (MAKTABA_TEST_PG_URL) with
// users A and B and libraries A and B.
func pgSearchDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	for _, st := range []string{
		`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ('` + userA + `', 'search-a', 'x', true), ('` + userB + `', 'search-b', 'x', true)`,
		`INSERT INTO libraries (id, name, roots) VALUES ('` + libA + `', 'Lessons', '{/m/a}'), ('` + libB + `', 'Other', '{/m/b}')`,
	} {
		if _, err := db.Exec(st); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// pgVideo adds a video with one audio track and returns the track's id.
func pgVideo(t *testing.T, db *sql.DB, id, lib, title string) int64 {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec)
		VALUES ($1, $2, $3, $3, md5($1), 1, now(), 'ready', $4, 600)`, id, lib, "/m/"+id+".mp4", title); err != nil {
		t.Fatal(err)
	}
	var track int64
	if err := db.QueryRow(`INSERT INTO audio_tracks (video_id, track_index, codec, channels, language, is_default)
		VALUES ($1, 0, 'aac', 2, 'ara', true) RETURNING id`, id).Scan(&track); err != nil {
		t.Fatal(err)
	}
	return track
}

func pgTranscript(t *testing.T, db *sql.DB, id, video string, track int64, active bool, texts ...string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, is_active)
		VALUES ($1, $2, $3, 'ar', 'test', 'm', $4)`, id, video, track, active); err != nil {
		t.Fatal(err)
	}
	for i, text := range texts {
		if _, err := db.Exec(`INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text) VALUES ($1, $2, $3, $4, $5)`,
			id, i, i*10, i*10+9, text); err != nil {
			t.Fatal(err)
		}
	}
}

func pgUUID(n int) string { return fmt.Sprintf("20000000-0000-4000-8000-%012d", n) }

// The same flows as the SQLite tests on Postgres: tsvector matching and
// ts_rank, true counts over the active transcript, the saved set under a UUID
// user, ACL, passages, expiry and recent searches.
func TestSearch_Postgres(t *testing.T) {
	db := pgSearchDB(t)
	va, vb, vc, vold := pgUUID(1), pgUUID(2), pgUUID(3), pgUUID(4)
	tr := pgVideo(t, db, va, libA, "درس الصبر")
	pgTranscript(t, db, pgUUID(11), va, tr, true, repeat("الصبر مفتاح الفرج", 130)...)
	trOld := pgVideo(t, db, vold, libA, "قديم")
	pgTranscript(t, db, pgUUID(14), vold, trOld, false, repeat("الصبر", 20)...)
	tr = pgVideo(t, db, vb, libA, "الصبر والشكر")
	pgTranscript(t, db, pgUUID(12), vb, tr, true, "الصبر", "الشكر", "الصبر الصبر الصبر")
	tr = pgVideo(t, db, vc, libB, "مكتبة أخرى")
	pgTranscript(t, db, pgUUID(13), vc, tr, true, "الصبر")

	c := &clock{t: time.Now()}
	h := router(&Handler{DB: db, NowFunc: c.now})
	limited := &principal.Principal{UserID: userA, Libraries: []string{libA}}
	resp := decode[Response](t, call(t, h, limited, http.MethodPost, "/api/search", `{"q":"الصبر","mode":"fts","view":"list"}`), 200)
	if resp.SearchID == "" || resp.TranscriptTotal != 2 {
		t.Fatalf("search: %+v", resp)
	}
	n := map[string]int{}
	for _, v := range resp.TranscriptVideos {
		n[v.VideoID] = v.Matches
		if v.LibraryName != "Lessons" || len(v.Passages) == 0 || !strings.Contains(v.Passages[0].Snippet, "<mark>الصبر</mark>") {
			t.Fatalf("video %+v", v)
		}
	}
	if n[va] != 130 || n[vb] != 2 || n[vc] != 0 || n[vold] != 0 {
		t.Fatalf("counts %v", n)
	}
	// vb's best passage repeats the word: it ranks first
	if resp.TranscriptVideos[0].VideoID != vb {
		t.Fatalf("order %+v", resp.TranscriptVideos)
	}
	if len(resp.Videos) != 2 || !resp.Videos[0].InTranscripts {
		t.Fatalf("titles %+v", resp.Videos)
	}
	page := decode[passagesPage](t, call(t, h, limited, http.MethodGet,
		"/api/search/passages?sid="+resp.SearchID+"&video_id="+va+"&order=time&offset=100&limit=50&include_times=1", ""), 200)
	if page.Total != 130 || len(page.Items) != 30 || len(page.Times) != 130 || page.Items[0].StartSec != 1000 {
		t.Fatalf("passages total %d, %d items, %d times", page.Total, len(page.Items), len(page.Times))
	}
	if rec := call(t, h, adminB, http.MethodGet, "/api/search/results/"+resp.SearchID+"?list=titles", ""); rec.Code != http.StatusGone {
		t.Fatalf("user B read user A's search: %d", rec.Code)
	}
	c.t = c.t.Add(16 * time.Minute)
	if rec := call(t, h, limited, http.MethodGet, "/api/search/results/"+resp.SearchID+"?list=titles", ""); rec.Code != http.StatusGone {
		t.Fatalf("expired: %d", rec.Code)
	}
	recent := decode[recentList](t, call(t, h, limited, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(recent) != 1 || recent[0].Live || recent[0].TranscriptCount != 2 {
		t.Fatalf("recent %+v", recent)
	}
}

// TestSearch_PostgresPerf measures the first page of a search on a library of
// MAKTABA_SEARCH_PERF_SEGMENTS segments (the home: about 10.5 M) and logs the
// plan of the grouping query. Off unless that variable is set: seeding takes
// minutes.
func TestSearch_PostgresPerf(t *testing.T) {
	total, _ := strconv.Atoi(os.Getenv("MAKTABA_SEARCH_PERF_SEGMENTS"))
	if total <= 0 {
		t.Skip("MAKTABA_SEARCH_PERF_SEGMENTS unset")
	}
	db := pgSearchDB(t)
	ctx := context.Background()
	const perVideo = 700
	videos := (total + perVideo - 1) / perVideo
	start := time.Now()
	exec := func(q string, args ...any) {
		t.Helper()
		t0 := time.Now()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		t.Logf("%6.1fs  %s", time.Since(t0).Seconds(), strings.Join(strings.Fields(q), " ")[:min(90, len(strings.Join(strings.Fields(q), " ")))])
	}
	exec(`DROP INDEX IF EXISTS transcript_segments_search_tsv_idx`)
	exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec)
		SELECT gen_random_uuid(), $1, '/m/' || g || '.mp4', g || '.mp4', md5(g::text), 1, now(), 'ready', 'Lesson ' || g, 7000
		FROM generate_series(1, $2) g`, libA, videos)
	exec(`INSERT INTO audio_tracks (video_id, track_index, codec, channels, language, is_default)
		SELECT id, 0, 'aac', 2, 'ara', true FROM videos`)
	exec(`INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, is_active)
		SELECT gen_random_uuid(), a.video_id, a.id, 'ar', 'perf', 'm', true FROM audio_tracks a`)
	// 12 words a segment from 5000, Zipf-like: k0 is in about half the
	// segments, k10 in 5 %, k100 in 1 %, k1000 in 0.2 %, k4000 in 0.1 %
	exec(`INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text)
		SELECT t.id, g, g * 10, g * 10 + 9,
		       array_to_string(ARRAY(SELECT 'k' || floor(5000 * power(random(), 3))::int
		                             FROM generate_series(1, 12 + 0 * g)), ' ')
		FROM transcripts t, generate_series(0, $1 - 1) g`, perVideo)
	exec(`CREATE INDEX transcript_segments_search_tsv_idx ON transcript_segments USING GIN (search_tsv)`)
	exec(`VACUUM ANALYZE transcript_segments`)
	exec(`ANALYZE transcripts`)
	exec(`ANALYZE videos`)
	var segs int64
	if err := db.QueryRow(`SELECT count(*) FROM transcript_segments`).Scan(&segs); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d segments in %d videos in %v", segs, videos, time.Since(start).Round(time.Second))

	h := router(&Handler{DB: db})
	for _, term := range []string{"k4000", "k1000", "k100", "k10", "k0"} {
		var matching int64
		_ = db.QueryRow(`SELECT count(*) FROM transcript_segments WHERE search_tsv @@ plainto_tsquery('maktaba_search', $1)`, term).Scan(&matching)
		for _, run := range []string{"cold", "warm"} {
			t0 := time.Now()
			resp := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"`+term+`","mode":"fts","view":"grid"}`), 200)
			first := time.Since(t0)
			t1 := time.Now()
			decode[resultsPage](t, call(t, h, admin, http.MethodGet, "/api/search/results/"+resp.SearchID+"?list=transcripts&offset=24&limit=24", ""), 200)
			next := time.Since(t1)
			t.Logf("%-6s %-4s matching segments %8d  videos %5d  first page %6dms (fts %dms)  page 2 %4dms",
				term, run, matching, resp.TranscriptTotal, first.Milliseconds(), resp.TookMs.FTS, next.Milliseconds())
		}
	}
	for _, term := range []string{"k100", "k0"} {
		rows, err := db.Query(`EXPLAIN (ANALYZE, BUFFERS)
			SELECT t.video_id::text, count(*), max(ts_rank(s.search_tsv, plainto_tsquery('maktaba_search', $1))) AS best
			FROM transcript_segments s
			JOIN transcripts t ON t.id = s.transcript_id AND t.is_active
			JOIN videos v ON v.id = t.video_id AND v.deleted_at IS NULL
			WHERE s.search_tsv @@ plainto_tsquery('maktaba_search', $1)
			GROUP BY t.video_id ORDER BY best DESC, count(*) DESC, t.video_id LIMIT 500`, term)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var line string
			_ = rows.Scan(&line)
			plan = append(plan, line)
		}
		rows.Close()
		t.Logf("EXPLAIN %s:\n%s", term, strings.Join(plan, "\n"))
	}
}
