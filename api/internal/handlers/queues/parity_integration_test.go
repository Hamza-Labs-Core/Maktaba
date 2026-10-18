//go:build integration

package queues

// Runs the control-centre parity endpoints (explain, style overrides, insights,
// pulse) against a real, migrated Postgres, because the scripted driver of the
// unit tests cannot tell whether the SQL is valid.
//
//	MAKTABA_TEST_PG_URL=postgres://u:p@127.0.0.1:5432/scratch_db?sslmode=disable \
//	    go test -tags integration ./internal/handlers/queues/ -run Parity
//
// The database must be a scratch one with every migration applied; the test
// writes rows into it.

import (
	"database/sql"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

func parityDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("MAKTABA_TEST_PG_URL")
	if url == "" {
		t.Skip("MAKTABA_TEST_PG_URL not set")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestParity_AgainstPostgres(t *testing.T) {
	db := parityDB(t)
	h := &Handler{DB: db, Hub: ws.NewHub(), NowFunc: time.Now}
	r := chi.NewRouter()
	h.Mount(r)
	f := &fixture{h: h, r: r}

	mustExec(t, db, `DELETE FROM processing_jobs; DELETE FROM season_style; DELETE FROM series_episodes;
		DELETE FROM series_seasons; DELETE FROM series; DELETE FROM videos; DELETE FROM libraries;
		DELETE FROM media_metadata_enrichment; DELETE FROM youtube_videos; DELETE FROM youtube_channels`)
	mustExec(t, db, `INSERT INTO libraries (id, name, roots) VALUES ($1, 'قناة', ARRAY['/x'])`, libL)
	mustExec(t, db, `INSERT INTO series (id, name) VALUES ($1, 'الحبيب')`, seriesX)
	mustExec(t, db, `INSERT INTO series_seasons (id, series_id, library_id, season, kind, name, membership_hash,
		detector_version, metadata) VALUES ($1, $2, $3, 1, 'episodic', 'الحبيب', 'h', 1,
		jsonb_build_object('extras', jsonb_build_array($4::text)))`, seasonA, seriesX, libL, "66666666-6666-4666-8666-666666666666")
	mustExec(t, db, `INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, title, duration_sec, metadata)
		VALUES ($1, $2, repeat('a', 64), '/x/e1.mp4', 'e1.mp4', 1, now(), 'طلع البدر', 1500, '{"width":1920,"height":1080}'),
		       ('66666666-6666-4666-8666-666666666666', $2, repeat('b', 64), '/x/promo.mp4', 'promo.mp4', 1, now(), 'برومو', 30, '{}')`, videoV, libL)
	mustExec(t, db, `INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1, $2, 1, 1)`, seriesX, videoV)
	mustExec(t, db, `INSERT INTO youtube_channels (channel_id, library_id, url) VALUES ('UC1', $1, 'https://youtube.com/@x')`, libL)
	mustExec(t, db, `INSERT INTO youtube_videos (youtube_id, channel_id, title, upload_date, date_quality, duration_s, tab)
		VALUES ('yt-1', 'UC1', 'طلع البدر', '2020-01-01', 'exact', 1500, 'videos'),
		       ('yt-remote', 'UC1', 'طلع البدر 2', '2020-02-01', 'approximate', 600, 'videos')`)
	mustExec(t, db, `INSERT INTO media_metadata_enrichment (video_id, provider, external_id, mapped, confidence)
		VALUES ($1, 'youtube', 'yt-1', '{"link_method":"info_json"}', 1)`, videoV)

	// explain: the linked episode, the extra, and the video on YouTube only
	rec := f.do("GET", "/api/explain?q=%D8%B7%D9%84%D8%B9&library_id="+libL, "", viewer())
	var ex ExplainResponse
	if rec.Code != 200 {
		t.Fatalf("explain %d %s", rec.Code, rec.Body.String())
	}
	decode(t, rec, &ex)
	if len(ex.Videos) != 1 || ex.Videos[0].Verdict.Kind != VerdictEpisode || *ex.Videos[0].LinkMethod != "info_json" ||
		*ex.Videos[0].Width != 1920 || len(ex.Remote) != 1 || ex.Remote[0].YouTubeID != "yt-remote" {
		t.Fatalf("explain %+v", ex)
	}
	rec = f.do("GET", "/api/explain?q=%D8%A8%D8%B1%D9%88%D9%85%D9%88", "", viewer())
	decode(t, rec, &ex)
	if rec.Code != 200 || len(ex.Videos) != 1 || ex.Videos[0].Verdict.Kind != VerdictExtra {
		t.Fatalf("explain extra %d %s", rec.Code, rec.Body.String())
	}

	// style: pin, list, re-queued rate_style, clear
	rec = f.do("PUT", "/api/series/"+seriesX+"/style", `{"style":"cinematic"}`, admin())
	if rec.Code != 200 {
		t.Fatalf("pin %d %s", rec.Code, rec.Body.String())
	}
	var override sql.NullString
	if err := db.QueryRow(`SELECT override_style FROM season_style WHERE season_id = $1`, seasonA).Scan(&override); err != nil || override.String != "cinematic" {
		t.Fatalf("override %v %v", override, err)
	}
	var queued int
	if err := db.QueryRow(`SELECT count(*) FROM processing_jobs WHERE stage = 'rate_style' AND subject_id = $1::uuid AND state = 'pending'`, seasonA).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("rate_style queued %d %v", queued, err)
	}
	rec = f.do("GET", "/api/seasons/styles?pinned=true", "", viewer())
	var list struct{ Items []SeasonStyle }
	decode(t, rec, &list)
	if rec.Code != 200 || len(list.Items) != 1 || *list.Items[0].OverrideStyle != "cinematic" {
		t.Fatalf("list %d %s", rec.Code, rec.Body.String())
	}
	if rec = f.do("PUT", "/api/seasons/"+seasonA+"/style", `{"style":null}`, admin()); rec.Code != 200 {
		t.Fatalf("clear %d %s", rec.Code, rec.Body.String())
	}
	if err := db.QueryRow(`SELECT override_style FROM season_style WHERE season_id = $1`, seasonA).Scan(&override); err != nil || override.Valid {
		t.Fatalf("cleared override %v %v", override, err)
	}

	// insights and pulse run on the real schema
	if rec = f.do("GET", "/api/queues/insights", "", viewer()); rec.Code != http.StatusOK {
		t.Fatalf("insights %d %s", rec.Code, rec.Body.String())
	}
	mustExec(t, db, `UPDATE processing_jobs SET state = 'running', claimed_at = now() - interval '1 hour',
		started_at = now() - interval '1 hour', last_heartbeat_at = now() - interval '30 minutes',
		claimed_by = 'pc/1' WHERE stage = 'rate_style'`)
	rec = f.do("GET", "/api/queues/pulse", "", viewer())
	var p Pulse
	decode(t, rec, &p)
	if rec.Code != 200 || p.State != PulseStalled || len(p.Stuck) != 1 || p.Stuck[0].Stage != "rate_style" {
		t.Fatalf("pulse %d %s", rec.Code, rec.Body.String())
	}
}
