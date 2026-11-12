package parental_test

// Epic 57 review: the recompute's row lock (finding 4), its queue order and failing
// jobs (finding 15), the YouTube match trigger (finding 19) and slot 0220's checks and
// indexes (findings 16 and 17).

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Finding 4: a recompute holds the video's row from its read to its commit, so a second
// recompute of the same video cannot read in between and write an older answer later.
func TestRecomputeLocksTheVideoItReads(t *testing.T) {
	t.Parallel()
	h := newPGHome(t)
	lib := h.library("lock")
	h.libraryTier(lib, 9)
	v := h.video(lib)
	h.drain()
	h.want(v, 9, "library_default")

	// another recompute is in the middle of this video: it has read it and not committed
	ctx := context.Background()
	other, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Rollback() }()
	if _, err := other.ExecContext(ctx, `SELECT 1 FROM videos WHERE id = $1 FOR NO KEY UPDATE`, v); err != nil {
		t.Fatal(err)
	}
	// nothing about the video changed, so this recompute has nothing to write: only
	// its read can wait
	done := make(chan error, 1)
	go func() {
		_, err := h.rc.RecomputeVideo(ctx, v)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the recompute read a video another recompute holds (err %v): the older reading can win", err)
	case <-time.After(400 * time.Millisecond):
	}
	if err := other.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the recompute did not go on after the other one committed")
	}
	// the lock blocks no plain reader
	h.want(v, 9, "library_default")
}

// Finding 15: a job whose batch fails every time goes behind the others, is worked
// alone, and is never dropped.
func TestRecomputeFailingJobDoesNotHoldTheQueue(t *testing.T) {
	t.Parallel()
	h := newPGHome(t)
	lib := h.library("poison")
	bad, good, later := h.video(lib), h.video(lib), h.video(lib)
	h.drain()
	// the tier of one video cannot be written (a fault that stays)
	h.exec(`CREATE FUNCTION test_poison() RETURNS trigger LANGUAGE plpgsql AS $f$
		BEGIN RAISE EXCEPTION 'this video cannot be rated'; END $f$`)
	h.exec(`CREATE TRIGGER test_poison BEFORE UPDATE OF age_tier ON videos
		FOR EACH ROW WHEN (OLD.id = '` + bad + `'::uuid) EXECUTE FUNCTION test_poison()`)
	h.exec(`UPDATE videos SET age_tier_admin = 16 WHERE id IN ($1, $2, $3)`, bad, good, later)
	for _, v := range []string{bad, good} { // the failing one is the oldest job
		if err := h.rc.Enqueue(context.Background(), h.db, parental.KindVideo, v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.rc.Drain(context.Background()); err == nil {
		t.Fatal("the drain reported no error for the video that cannot be rated")
	}
	// the video beside it in the queue was rated all the same
	h.want(good, 16, "admin_video")
	h.want(bad, -1, "")
	attempts := func() (n, jobs int) {
		t.Helper()
		if err := h.db.QueryRow(`SELECT COALESCE(max(attempts), 0), count(*) FROM parental_recompute_jobs
			WHERE kind = 'video' AND subject = $1`, bad).Scan(&n, &jobs); err != nil {
			t.Fatal(err)
		}
		return n, jobs
	}
	first, jobs := attempts()
	if jobs != 1 || first < 1 || h.jobs() != 1 {
		t.Fatalf("the failing job: %d rows with attempts %d, %d jobs in all; want it kept, counted, alone", jobs, first, h.jobs())
	}
	// a job queued later is worked before the one that keeps failing
	if err := h.rc.Enqueue(context.Background(), h.db, parental.KindVideo, later); err != nil {
		t.Fatal(err)
	}
	if _, err := h.rc.Drain(context.Background()); err == nil {
		t.Fatal("the drain reported no error the second time")
	}
	h.want(later, 16, "admin_video")
	if n, jobs := attempts(); jobs != 1 || n <= first {
		t.Fatalf("the failing job after a second drain: %d rows, attempts %d (was %d)", jobs, n, first)
	}
	// the fault passes: the job is still there and rates the video
	h.exec(`DROP TRIGGER test_poison ON videos`)
	h.drain()
	h.want(bad, 16, "admin_video")
	if h.jobs() != 0 {
		t.Fatalf("%d jobs left", h.jobs())
	}
}

// Finding 15: an admin's change is not worked after a long backfill but between its
// batches.
func TestRecomputeWorksNewJobsBetweenABackfillsBatches(t *testing.T) {
	t.Parallel()
	h := newPGHome(t)
	lib, small := h.library("backfill"), h.library("small")
	h.videos(lib, 2500)
	one := h.video(small)
	h.drain()
	ctx := context.Background()
	h.exec(`UPDATE libraries SET age_tier = 9 WHERE id = $1`, lib)
	if err := h.rc.Enqueue(ctx, h.db, parental.KindLibrary, lib); err != nil { // three batches
		t.Fatal(err)
	}
	if worked, err := h.rc.Step(ctx); err != nil || !worked {
		t.Fatalf("first batch: %v %v", worked, err)
	}
	// while the backfill is under way an admin rates one video elsewhere
	h.exec(`UPDATE videos SET age_tier_admin = 18 WHERE id = $1`, one)
	if err := h.rc.Enqueue(ctx, h.db, parental.KindVideo, one); err != nil {
		t.Fatal(err)
	}
	if worked, err := h.rc.Step(ctx); err != nil || !worked {
		t.Fatalf("next step: %v %v", worked, err)
	}
	h.want(one, 18, "admin_video")
	var left int
	if err := h.db.QueryRow(`SELECT count(*) FROM videos WHERE library_id = $1 AND age_tier IS NULL`, lib).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1500 {
		t.Fatalf("%d videos of the backfill still unrated, want 1500: the video's job waited for the backfill", left)
	}
	h.drain()
	if err := h.db.QueryRow(`SELECT count(*) FROM videos WHERE library_id = $1 AND age_tier IS DISTINCT FROM 9`, lib).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d videos of the backfill not rated 9 (%v)", left, err)
	}
}

// Finding 19: an enrichment refresh that writes every YouTube match back as it was
// queues nothing; a change to what picks the match still does.
func TestYouTubeMatchWrittenBackUnchangedQueuesNothing(t *testing.T) {
	t.Parallel()
	h := newPGHome(t)
	lib := h.library("yt")
	v, w := h.video(lib), h.video(lib)
	h.exec(`INSERT INTO youtube_videos (youtube_id, title, age_limit) VALUES ('abc', 'x', 18)`)
	h.exec(`INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, confidence)
		VALUES (gen_random_uuid(), $1, 'youtube', 'abc', 0.9)`, v)
	h.drain()
	h.want(v, 18, "youtube")

	h.exec(`UPDATE media_metadata_enrichment SET video_id = video_id, provider = provider,
		external_id = external_id, confidence = confidence WHERE video_id = $1`, v)
	h.exec(`UPDATE media_metadata_enrichment SET confidence = 0.9, external_id = 'abc' WHERE video_id = $1`, v)
	if n := h.jobs(); n != 0 {
		t.Fatalf("writing a match back as it was queued %d jobs, want 0", n)
	}
	for i, q := range []string{
		`UPDATE media_metadata_enrichment SET confidence = 0.5 WHERE video_id = $1`,
		`UPDATE media_metadata_enrichment SET external_id = 'youtube:abc' WHERE video_id = $1`,
		`UPDATE media_metadata_enrichment SET provider = 'tmdb' WHERE video_id = $1`,
		`UPDATE media_metadata_enrichment SET provider = 'youtube' WHERE video_id = $1`,
	} {
		h.exec(q, v)
		if n := h.jobs(); n != i+1 {
			t.Fatalf("after change %d to the match: %d jobs, want %d (%s)", i+1, n, i+1, q)
		}
	}
	h.drain()
	h.want(v, 18, "youtube")
	// the match moves to another video: both are rated again
	h.exec(`UPDATE media_metadata_enrichment SET video_id = $2 WHERE video_id = $1`, v, w)
	h.drain()
	h.want(v, -1, "")
	h.want(w, 18, "youtube")
	h.exec(`DELETE FROM media_metadata_enrichment WHERE video_id = $1`, w)
	h.drain()
	h.want(w, -1, "")
}

// Finding 16: the checks slot 0220 puts on videos are added NOT VALID and validated in
// a statement of their own, so the table is never read under the exclusive lock.
func TestSlot0220ChecksOnVideosAreValidatedApart(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "0220_age_tiers.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	add := regexp.MustCompile(`(?s)ALTER TABLE videos ADD CONSTRAINT (\w+)\s+CHECK .*?;`)
	found := add.FindAllStringSubmatch(up, -1)
	if len(found) != 3 {
		t.Fatalf("%d checks added to videos, want 3", len(found))
	}
	for _, m := range found {
		if !strings.Contains(m[0], "NOT VALID;") {
			t.Errorf("%s is added without NOT VALID: the whole table is read under ACCESS EXCLUSIVE", m[1])
		}
		validate := "ALTER TABLE videos VALIDATE CONSTRAINT " + m[1] + ";"
		if !strings.Contains(up, validate) {
			t.Errorf("%s is never validated", m[1])
		}
		if strings.Index(up, validate) < strings.Index(up, m[0]) {
			t.Errorf("%s is validated before it is added", m[1])
		}
	}
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" {
		t.Skip("MAKTABA_TEST_PG_URL not set: the migrated database is not looked at")
	}
	h := newPGHome(t)
	var valid int
	if err := h.db.QueryRow(`SELECT count(*) FROM pg_constraint WHERE convalidated AND conname IN
		('videos_age_tier_valid', 'videos_age_tier_source_valid', 'videos_age_tier_admin_valid')`).Scan(&valid); err != nil || valid != 3 {
		t.Fatalf("%d of the three checks on videos are valid after the migrations (%v)", valid, err)
	}
	lib := h.library("checks")
	v := h.video(lib)
	for _, q := range []string{
		`UPDATE videos SET age_tier = 12 WHERE id = $1`,
		`UPDATE videos SET age_tier_admin = 12 WHERE id = $1`,
		`UPDATE videos SET age_tier_source = 'guessed' WHERE id = $1`,
	} {
		if _, err := h.db.Exec(q, v); err == nil {
			t.Fatalf("accepted: %s", q)
		}
	}
}

// Finding 17: an index left INVALID by an interrupted concurrent build is built again
// when the slot runs again, in every slot of the epic that builds one.
func TestAnInterruptedIndexBuildIsRebuilt(t *testing.T) {
	h := newPGHome(t)
	valid := func(name string) (exists, ok bool) {
		t.Helper()
		err := h.db.QueryRow(`SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE c.relname = $1`, name).Scan(&ok)
		if err == sql.ErrNoRows {
			return false, false
		}
		if err != nil {
			t.Fatal(err)
		}
		return true, ok
	}
	slots := map[string][]string{
		"0220_age_tiers.sql":         {"videos_library_age_tier_idx", "channel_programs_rated_video_idx"},
		"0222_parental_requests.sql": {"parental_requests_one_pending", "parental_requests_by_status_idx", "parental_pin_failures_child_idx", "parental_events_at_idx", "parental_events_child_idx", "parental_events_once"},
		"0223_parental_time.sql":     {"parental_time_rules_user_idx", "parental_time_grants_user_day_idx"},
		"0224_content_signals.sql":   {"video_content_signals_review_idx"},
	}
	concurrently := regexp.MustCompile(`CREATE (?:UNIQUE )?INDEX CONCURRENTLY IF NOT EXISTS (\w+)`)
	for file, indexes := range slots {
		up, _ := gooseStatements(t, file)
		// the test names every index the slot builds concurrently
		built := map[string]bool{}
		for _, stmt := range up {
			if m := concurrently.FindStringSubmatch(stmt); m != nil {
				built[m[1]] = true
			}
		}
		if len(built) != len(indexes) {
			t.Fatalf("%s builds %d indexes concurrently, the test knows %d", file, len(built), len(indexes))
		}
		for _, name := range indexes {
			if !built[name] {
				t.Fatalf("%s does not build %s", file, name)
			}
			if exists, ok := valid(name); !exists || !ok {
				t.Fatalf("%s: exists=%v valid=%v after the migrations", name, exists, ok)
			}
			// what an interrupted CREATE INDEX CONCURRENTLY leaves behind
			h.exec(`UPDATE pg_index SET indisvalid = false, indisready = false
				WHERE indexrelid = $1::regclass`, name)
		}
		run(t, h.db, file+" up again", up)
		for _, name := range indexes {
			if exists, ok := valid(name); !exists || !ok {
				t.Errorf("%s (%s): exists=%v valid=%v after the slot ran again: the invalid index was skipped", name, file, exists, ok)
			}
		}
	}
}

// Slot 0224 edited in place (review findings 13 and 25): a home that ran the slot as it
// first was gets the new columns and the third not_checked value when the slot runs again.
func TestSlot0224RunAgainOnItsFirstForm(t *testing.T) {
	h := newPGHome(t)
	lib := h.library("first-form")
	v := h.video(lib)
	// the table as the slot first made it
	h.exec(`ALTER TABLE video_content_signals DROP COLUMN attempts, DROP COLUMN failed_hash, DROP COLUMN failed_runs`)
	h.exec(`ALTER TABLE video_content_signals DROP CONSTRAINT video_content_signals_not_checked_check`)
	h.exec(`ALTER TABLE video_content_signals ADD CONSTRAINT video_content_signals_not_checked_check
		CHECK (not_checked IS NULL OR not_checked IN ('no_transcript', 'unsupported_language'))`)
	if _, err := h.db.Exec(`INSERT INTO video_content_signals (video_id, not_checked) VALUES ($1, 'model_failed')`, v); err == nil {
		t.Fatal("the first form took model_failed: the test does not start where it means to")
	}
	up, _ := gooseStatements(t, "0224_content_signals.sql")
	run(t, h.db, "0224 up again", up)
	run(t, h.db, "0224 up a third time", up)
	h.exec(`INSERT INTO video_content_signals (video_id, not_checked, model, failed_hash, failed_runs, attempts)
		VALUES ($1, 'model_failed', 'm', 'h', 3, 0)`, v)
	for what, q := range map[string]string{
		"a tier beside model_failed": `UPDATE video_content_signals SET tier = 9 WHERE video_id = $1`,
		"an unknown reason":          `UPDATE video_content_signals SET not_checked = 'later' WHERE video_id = $1`,
		"a negative attempts":        `UPDATE video_content_signals SET attempts = -1 WHERE video_id = $1`,
	} {
		if _, err := h.db.Exec(q, v); err == nil {
			t.Fatalf("%s was accepted", what)
		}
	}
	// a video the model failed on rates nothing and queues nothing
	if n := h.jobs(); n != 0 {
		t.Fatalf("a not-checked signal queued %d recompute jobs", n)
	}
}
