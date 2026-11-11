package parental_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// pgHome is a scratch database with the recompute over it; the job the migration
// puts in (every library) is worked first, so each test starts with an empty queue.
type pgHome struct {
	t  *testing.T
	db *sql.DB
	rc *parental.Recomputer
}

func newPGHome(t *testing.T) *pgHome {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	h := &pgHome{t: t, db: db, rc: parental.NewRecomputer(db, slog.New(slog.NewTextHandler(io.Discard, nil)))}
	h.drain()
	return h
}

func (h *pgHome) exec(q string, args ...any) {
	h.t.Helper()
	if _, err := h.db.Exec(q, args...); err != nil {
		h.t.Fatalf("%s: %v", q, err)
	}
}

func (h *pgHome) drain() int {
	h.t.Helper()
	n, err := h.rc.Drain(context.Background())
	if err != nil {
		h.t.Fatalf("drain: %v", err)
	}
	return n
}

func (h *pgHome) jobs() int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT count(*) FROM parental_recompute_jobs`).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *pgHome) library(name string) string {
	h.t.Helper()
	id := uuid.NewString()
	h.exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, $2, $3)`, id, name, "{/media/"+name+"}")
	return id
}

// videos puts n videos in a library, each a second older than the one before.
func (h *pgHome) videos(lib string, n int) {
	h.t.Helper()
	h.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
		SELECT gen_random_uuid(), $1::uuid, '/media/' || $1 || '/' || g || '.mp4', g || '.mp4',
		       encode(sha256(convert_to($1 || '-' || g, 'UTF8')), 'hex'), 1000, now(), 'ready', 't' || g, 100, now(),
		       now() - make_interval(secs => g)
		  FROM generate_series(1, $2::int) g`, lib, n)
}

func (h *pgHome) video(lib string) string {
	h.t.Helper()
	id := uuid.NewString()
	h.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
		VALUES ($1::uuid, $2::uuid, '/media/v/' || $1::text || '.mp4', $1::text || '.mp4',
		        encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), 1000, now(), 'ready', 't', 100, now(), now())`, id, lib)
	return id
}

// want checks a video's stored tier (-1: unrated) and its source.
func (h *pgHome) want(video string, tier int, source string) {
	h.t.Helper()
	var (
		gotTier   sql.NullInt64
		gotSource sql.NullString
	)
	if err := h.db.QueryRow(`SELECT age_tier, age_tier_source FROM videos WHERE id = $1`, video).Scan(&gotTier, &gotSource); err != nil {
		h.t.Fatal(err)
	}
	got := -1
	if gotTier.Valid {
		got = int(gotTier.Int64)
	}
	if got != tier || gotSource.String != source {
		h.t.Fatalf("video tier = %d from %q, want %d from %q", got, gotSource.String, tier, source)
	}
}

// T04: a library of 3,000 videos is worked in three batches of 1,000, and working
// the job again changes nothing.
func TestRecomputeLibraryInBatches(t *testing.T) {
	h := newPGHome(t)
	lib := h.library("batches")
	h.videos(lib, 3000)
	if n := h.jobs(); n != 0 {
		t.Fatalf("videos of a library with no default and no rating queued %d jobs, want 0", n)
	}
	h.exec(`UPDATE libraries SET age_tier = 9 WHERE id = $1`, lib)
	ctx := context.Background()
	if err := h.rc.Enqueue(ctx, h.db, parental.KindLibrary, lib); err != nil {
		t.Fatal(err)
	}

	// after one batch the job keeps its place: 1,000 rated, the newest first
	if worked, err := h.rc.Step(ctx); err != nil || !worked {
		t.Fatalf("first batch: worked=%v err=%v", worked, err)
	}
	var rated, batches, olderRated int
	if err := h.db.QueryRow(`SELECT count(*) FROM videos WHERE library_id = $1 AND age_tier = 9`, lib).Scan(&rated); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(`SELECT batches FROM parental_recompute_jobs WHERE kind = 'library' AND subject = $1`, lib).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(`
		SELECT count(*) FROM videos v
		 WHERE v.library_id = $1 AND v.age_tier IS NOT NULL
		   AND EXISTS (SELECT 1 FROM videos n WHERE n.library_id = $1 AND n.age_tier IS NULL AND n.created_at > v.created_at)`,
		lib).Scan(&olderRated); err != nil {
		t.Fatal(err)
	}
	if rated != 1000 || batches != 1 || olderRated != 0 {
		t.Fatalf("after one batch: %d rated, batches=%d, %d rated before a newer one; want 1000, 1, 0", rated, batches, olderRated)
	}

	if n := h.drain(); n != 2 {
		t.Fatalf("the rest took %d batches, want 2 (3 in all)", n)
	}
	if err := h.db.QueryRow(`SELECT count(*) FROM videos WHERE library_id = $1 AND age_tier = 9 AND age_tier_source = 'library_default'`, lib).Scan(&rated); err != nil {
		t.Fatal(err)
	}
	if rated != 3000 || h.jobs() != 0 {
		t.Fatalf("%d of 3000 rated, %d jobs left", rated, h.jobs())
	}
	if n, err := h.rc.CountVideos(ctx, parental.KindLibrary, lib); err != nil || n != 3000 {
		t.Fatalf("CountVideos = %d, %v", n, err)
	}

	// worked again: the same three batches, the same tiers
	if err := h.rc.Enqueue(ctx, h.db, parental.KindLibrary, lib); err != nil {
		t.Fatal(err)
	}
	if n := h.drain(); n != 3 {
		t.Fatalf("the second run took %d batches, want 3", n)
	}
	if err := h.db.QueryRow(`SELECT count(*) FROM videos WHERE library_id = $1 AND age_tier = 9 AND age_tier_source = 'library_default'`, lib).Scan(&rated); err != nil {
		t.Fatal(err)
	}
	if rated != 3000 {
		t.Fatalf("after the second run %d of 3000 rated", rated)
	}
}

// The precedence chain against the real tables: each stronger source takes over as
// it appears and gives way as it goes, and the stages' changes reach the queue
// through the triggers alone.
func TestRecomputeFollowsEverySource(t *testing.T) {
	h := newPGHome(t)
	lib := h.library("chain")
	v := h.video(lib)
	if h.jobs() != 0 {
		t.Fatal("a new video with nothing to rate it queued a job")
	}
	h.want(v, -1, "")
	ctx := context.Background()

	// the library's default (the admin route enqueues the library)
	h.exec(`UPDATE libraries SET age_tier = 9 WHERE id = $1`, lib)
	if err := h.rc.Enqueue(ctx, h.db, parental.KindLibrary, lib); err != nil {
		t.Fatal(err)
	}
	h.drain()
	h.want(v, 9, "library_default")

	// a plugin's content_rating, alone, never brings the video under the default
	h.exec(`UPDATE videos SET metadata = metadata || '{"content_rating":"G"}' WHERE id = $1`, v)
	if h.jobs() != 1 {
		t.Fatalf("a changed content_rating queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 9, "library_default")
	h.exec(`UPDATE videos SET title = 'renamed' WHERE id = $1`, v)
	if h.jobs() != 0 {
		t.Fatal("a change that rates nothing queued a job")
	}

	// the guide's rating of a programme that plays the video
	ch := uuid.NewString()
	h.exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 901, 'c', 'c-901', 'shuffle')`, ch)
	h.exec(`INSERT INTO channel_programs (channel_id, seq, start_at, end_at, source_duration, video_id, title_snapshot)
		VALUES ($1, 1, now(), now() + interval '1 hour', 3600, $2, '{"rating":"TV-PG"}')`, ch, v)
	if h.jobs() != 1 {
		t.Fatalf("a new rating queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 9, "guide")

	// Plex's kept rating
	h.exec(`INSERT INTO plex_items (server, rating_key, section_key, section_title, video_id, detail)
		VALUES ('s', '1', '1', 'Films', $1, '{"contentRating":"PG-13"}')`, v)
	if h.jobs() != 1 {
		t.Fatalf("a new rating queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 13, "plex")

	// YouTube's age limit, learned after the match was made
	h.exec(`INSERT INTO youtube_videos (youtube_id, title) VALUES ('yt1', 't')`)
	h.exec(`INSERT INTO media_metadata_enrichment (video_id, provider, external_id, confidence) VALUES ($1, 'youtube', 'youtube:yt1', 1)`, v)
	if h.jobs() != 0 {
		t.Fatal("a match to a YouTube video with no age limit queued a job")
	}
	h.exec(`UPDATE youtube_videos SET age_limit = 18 WHERE youtube_id = 'yt1'`)
	if h.jobs() != 1 {
		t.Fatalf("a learned age limit queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 18, "youtube")

	// an admin's tier on a collection the video is in beats every rating, looser or not
	col := uuid.NewString()
	h.exec(`INSERT INTO collections (id, library_id, name) VALUES ($1, $2, 'picks')`, col, lib)
	h.exec(`INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1, $2, 0)`, col, v)
	if h.jobs() != 0 {
		t.Fatal("joining a collection with no tier queued a job")
	}
	h.exec(`UPDATE collections SET age_tier = 9 WHERE id = $1`, col)
	if err := h.rc.Enqueue(ctx, h.db, parental.KindCollection, col); err != nil {
		t.Fatal(err)
	}
	h.drain()
	h.want(v, 9, "admin_container")

	// a second container: the strictest of the two
	ser := uuid.NewString()
	h.exec(`INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'show')`, ser, lib)
	h.exec(`UPDATE series SET age_tier = 16 WHERE id = $1`, ser)
	h.exec(`INSERT INTO series_episodes (series_id, video_id) VALUES ($1, $2)`, ser, v)
	if h.jobs() != 1 {
		t.Fatalf("joining a series with a tier queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 16, "admin_container")

	// the admin's own tier on the video
	h.exec(`UPDATE videos SET age_tier_admin = 0 WHERE id = $1`, v)
	res, err := h.rc.RecomputeVideo(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier == nil || *res.Tier != 0 || res.Source != parental.SourceAdminVideo {
		t.Fatalf("RecomputeVideo = %+v, want 0 from admin_video", res)
	}
	h.want(v, 0, "admin_video")

	// and back down the chain
	h.exec(`UPDATE videos SET age_tier_admin = NULL WHERE id = $1`, v)
	if _, err := h.rc.RecomputeVideo(ctx, v); err != nil {
		t.Fatal(err)
	}
	h.want(v, 16, "admin_container")
	h.exec(`DELETE FROM series_episodes WHERE video_id = $1`, v)
	if h.jobs() != 1 {
		t.Fatalf("leaving a series queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 9, "admin_container")
	h.exec(`DELETE FROM collection_items WHERE video_id = $1`, v)
	h.drain()
	h.want(v, 18, "youtube")
	h.exec(`DELETE FROM media_metadata_enrichment WHERE video_id = $1`, v)
	if h.jobs() != 1 {
		t.Fatalf("a dropped YouTube match queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 13, "plex")
	// a changed source rating never leaves the looser tier behind: Plex's rating
	// tightens, the programme's rating changes, the item is taken off the video, the
	// programme leaves the schedule; a write that changes neither queues nothing
	h.exec(`UPDATE plex_items SET detail = '{"contentRating":"R"}' WHERE video_id = $1`, v)
	if h.jobs() != 1 {
		t.Fatalf("a changed Plex rating queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 18, "plex")
	h.exec(`UPDATE plex_items SET detail = detail || '{"title":"x"}', seen_at = now() WHERE video_id = $1`, v)
	h.exec(`UPDATE channel_programs SET end_at = end_at + interval '1 minute', title_snapshot = title_snapshot || '{"title":"x"}' WHERE video_id = $1`, v)
	if h.jobs() != 0 {
		t.Fatalf("writes that change no rating queued %d jobs", h.jobs())
	}
	h.exec(`UPDATE plex_items SET video_id = NULL WHERE video_id = $1`, v)
	if h.jobs() != 1 {
		t.Fatalf("a Plex item taken off its video queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 9, "guide")
	h.exec(`UPDATE channel_programs SET title_snapshot = '{"rating":"TV-MA"}' WHERE video_id = $1`, v)
	if h.jobs() != 1 {
		t.Fatalf("a changed guide rating queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 18, "guide")
	h.exec(`DELETE FROM channel_programs WHERE video_id = $1`, v)
	if h.jobs() != 1 {
		t.Fatalf("a programme leaving the schedule queued %d jobs, want 1", h.jobs())
	}
	h.exec(`UPDATE videos SET metadata = metadata - 'content_rating' WHERE id = $1`, v)
	h.drain()
	h.want(v, 9, "library_default")
	h.exec(`UPDATE libraries SET age_tier = NULL WHERE id = $1`, lib)
	if err := h.rc.Enqueue(ctx, h.db, parental.KindLibrary, lib); err != nil {
		t.Fatal(err)
	}
	h.drain()
	h.want(v, -1, "")
}

// A new video in a library with a default, and a video moved to another library,
// are rated without anybody asking; a YouTube source's tier reaches its downloads.
func TestRecomputeNewAndMovedVideos(t *testing.T) {
	h := newPGHome(t)
	kids, grown := h.library("kids"), h.library("grown")
	h.exec(`UPDATE libraries SET age_tier = 0 WHERE id = $1`, kids)
	h.exec(`UPDATE libraries SET age_tier = 18 WHERE id = $1`, grown)
	v := h.video(kids)
	h.drain()
	h.want(v, 0, "library_default")
	h.exec(`UPDATE videos SET library_id = $2 WHERE id = $1`, v, grown)
	h.drain()
	h.want(v, 18, "library_default")

	src := uuid.NewString()
	h.exec(`INSERT INTO youtube_sources (id, library_id, kind, url, external_id, age_tier) VALUES ($1, $2, 'channel', 'https://example.test/c', 'UC1', 13)`, src, grown)
	h.exec(`INSERT INTO youtube_downloads (library_id, youtube_id, source_id, video_id) VALUES ($1, 'yt9', $2, $3)`, grown, src, v)
	h.drain()
	h.want(v, 13, "admin_container")
	if n, err := h.rc.CountVideos(context.Background(), parental.KindYouTubeSource, src); err != nil || n != 1 {
		t.Fatalf("CountVideos of the source = %d, %v", n, err)
	}
}

// The job the migration puts in becomes one job for each library, and a job
// nothing can work is dropped instead of holding the queue.
func TestRecomputeAllAndInvalidJobs(t *testing.T) {
	h := newPGHome(t)
	a, b := h.library("one"), h.library("two")
	va, vb := h.video(a), h.video(b)
	// a rating that arrived before the update: no trigger saw it
	h.exec(`ALTER TABLE videos DISABLE TRIGGER videos_parental_upd`)
	h.exec(`UPDATE videos SET metadata = '{"content_rating":"TV-MA"}' WHERE id = $1`, va)
	h.exec(`ALTER TABLE videos ENABLE TRIGGER videos_parental_upd`)
	h.exec(`INSERT INTO parental_recompute_jobs (kind, subject) VALUES ('library', 'not-an-id'), ('all', '')`)
	ctx := context.Background()
	if err := h.rc.Enqueue(ctx, h.db, parental.KindVideo, "not-an-id"); err == nil {
		t.Fatal("Enqueue took a video id that is no id")
	}
	h.drain()
	if h.jobs() != 0 {
		t.Fatalf("%d jobs left", h.jobs())
	}
	h.want(va, 18, "plugin")
	h.want(vb, -1, "")
}

// Acceptance: a kept Plex rating of gb/15 is 16 from plex; an admin's 18 on a
// collection the video is in is 18 from admin_container; a plugin's G against
// Plex's R stays 18.
func TestRecomputeAcceptance(t *testing.T) {
	h := newPGHome(t)
	lib := h.library("accept")
	v, w := h.video(lib), h.video(lib)
	h.exec(`INSERT INTO plex_items (server, rating_key, section_key, section_title, video_id, detail)
		VALUES ('s', '1', '1', 'Films', $1, '{"contentRating":"gb/15"}'), ('s', '2', '1', 'Films', $2, '{"contentRating":"R"}')`, v, w)
	h.exec(`UPDATE videos SET metadata = '{"content_rating":"G"}' WHERE id = $1`, w)
	h.drain()
	h.want(v, 16, "plex")
	h.want(w, 18, "plex")
	col := uuid.NewString()
	h.exec(`INSERT INTO collections (id, library_id, name, age_tier) VALUES ($1, $2, 'grown', 18)`, col, lib)
	h.exec(`INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1, $2, 0)`, col, v)
	h.drain()
	h.want(v, 18, "admin_container")
}

// The queue's function runs as its owner, so no other role may call it: a role with
// every right a stage has on the tables still cannot put a job in by hand, while
// its writes still reach the queue through the triggers.
func TestEnqueueFunctionIsTheOwnersAlone(t *testing.T) {
	h := newPGHome(t)
	lib := h.library("roles")
	v := h.video(lib)
	h.exec(`INSERT INTO plex_items (server, rating_key, section_key, section_title, video_id, detail)
		VALUES ('s', '1', '1', 'Films', $1, '{"contentRating":"PG"}')`, v)
	h.drain()
	role := "t1_parental_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	h.exec(`CREATE ROLE ` + role + ` NOLOGIN`)
	t.Cleanup(func() {
		_, _ = h.db.Exec(`DROP OWNED BY ` + role)
		_, _ = h.db.Exec(`DROP ROLE IF EXISTS ` + role)
	})
	h.exec(`GRANT SELECT, UPDATE ON plex_items TO ` + role)
	ctx := context.Background()
	conn, err := h.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `SET ROLE `+role); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, `RESET ROLE`) }()
	for _, q := range []string{
		`SELECT parental_recompute_enqueue('all', '')`,
		`INSERT INTO parental_recompute_jobs (kind) VALUES ('all')`,
	} {
		if _, err := conn.ExecContext(ctx, q); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("%s as another role: %v, want permission denied", q, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `UPDATE plex_items SET detail = '{"contentRating":"R"}' WHERE video_id = $1`, v); err != nil {
		t.Fatalf("the role's own write: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
	if h.jobs() != 1 {
		t.Fatalf("the role's write queued %d jobs through the trigger, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 18, "plex")
}
