package parental_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Story 57.17: the detected tier in the recompute, slot 0224 and the stage's role.

// signal writes a video's content signal as the stage does (one row a video).
func (h *pgHome) signal(video string, tier any, confidence float64) {
	h.t.Helper()
	h.exec(`INSERT INTO video_content_signals (video_id, tier, confidence, model, input_hash)
		VALUES ($1, $2, $3, 'test-model', 'h1')
		ON CONFLICT (video_id) DO UPDATE SET tier = EXCLUDED.tier, confidence = EXCLUDED.confidence,
			reviewed_by = NULL, reviewed_at = NULL, updated_at = now()`, video, tier, confidence)
}

// libraryTier sets a library's default as the admin route does: the write and its job.
func (h *pgHome) libraryTier(lib string, tier any) {
	h.t.Helper()
	h.exec(`UPDATE libraries SET age_tier = $2 WHERE id = $1`, lib, tier)
	if err := h.rc.Enqueue(context.Background(), h.db, parental.KindLibrary, lib); err != nil {
		h.t.Fatal(err)
	}
	h.drain()
}

// recompute rates one video now, as a route that changed it does.
func (h *pgHome) recompute(video string) {
	h.t.Helper()
	if _, err := h.rc.RecomputeVideo(context.Background(), video); err != nil {
		h.t.Fatal(err)
	}
}

// T03 on the database: a signal counts at or above the line, and only to tighten.
func TestRecomputeDetectedTier(t *testing.T) {
	h := newPGHome(t)
	lib := h.library("detected")
	v := h.video(lib)
	h.drain()

	// confidence 0.5: under the line, the video stays unrated
	h.signal(v, 16, 0.5)
	if h.jobs() != 1 {
		t.Fatalf("a new signal queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, -1, "")

	// confidence 0.9: the detected tier, source detected
	h.signal(v, 16, 0.9)
	h.drain()
	h.want(v, 16, "detected")

	// exactly on the line counts (confidence is a real, the line a double)
	h.signal(v, 13, 0.8)
	h.drain()
	h.want(v, 13, "detected")

	// metadata 13 and detected 16: 16; metadata 16 and detected 9: 16
	h.exec(`INSERT INTO plex_items (server, rating_key, section_key, section_title, video_id, detail)
		VALUES ('srv-d', 'rk-d1', '1', 'Films', $1, '{"contentRating":"PG-13"}')`, v)
	h.signal(v, 16, 0.9)
	h.drain()
	h.want(v, 16, "detected")
	h.exec(`UPDATE plex_items SET detail = '{"contentRating":"gb/15"}' WHERE video_id = $1`, v)
	h.signal(v, 9, 0.95)
	h.drain()
	h.want(v, 16, "plex")
	h.exec(`DELETE FROM plex_items WHERE video_id = $1`, v)
	h.drain()
	h.want(v, 9, "detected")

	// alone, it never brings the video under the library's default
	h.libraryTier(lib, 13)
	h.want(v, 13, "library_default")
	h.libraryTier(lib, nil)
	h.want(v, 9, "detected")

	// an admin's tier on the video wins
	h.exec(`UPDATE videos SET age_tier_admin = 9 WHERE id = $1`, v)
	h.signal(v, 18, 0.99)
	h.drain()
	h.want(v, 9, "admin_video")
	h.exec(`UPDATE videos SET age_tier_admin = NULL WHERE id = $1`, v)
	h.recompute(v)
	h.want(v, 18, "detected")

	// a reviewed signal rates nothing any more
	h.exec(`UPDATE video_content_signals SET reviewed_at = now() WHERE video_id = $1`, v)
	if h.jobs() != 1 {
		t.Fatalf("a reviewed signal queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, -1, "")

	// a removed signal leaves no tier behind
	h.signal(v, 18, 0.99)
	h.drain()
	h.want(v, 18, "detected")
	h.exec(`DELETE FROM video_content_signals WHERE video_id = $1`, v)
	if h.jobs() != 1 {
		t.Fatalf("a removed signal queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, -1, "")
}

// A signal that cannot change a tier queues nothing; a change of the line asks for
// every video once.
func TestDetectedSignalQueuesOnlyWhatMatters(t *testing.T) {
	h := newPGHome(t)
	lib := h.library("queue")
	v, w := h.video(lib), h.video(lib)
	h.drain()

	// no tier: not checked, nothing to rate
	h.exec(`INSERT INTO video_content_signals (video_id, not_checked) VALUES ($1, 'no_transcript')`, v)
	if h.jobs() != 0 {
		t.Fatalf("a not-checked signal queued %d jobs, want 0", h.jobs())
	}
	// a write that changes neither tier, confidence nor review queues nothing
	h.signal(w, 16, 0.6)
	h.drain()
	h.exec(`UPDATE video_content_signals SET model = 'other', input_hash = 'h2', updated_at = now() WHERE video_id = $1`, w)
	if h.jobs() != 0 {
		t.Fatalf("a signal rewritten the same queued %d jobs, want 0", h.jobs())
	}
	h.want(w, -1, "")

	// the line moves down to 0.5: w is rated; twice the same value asks once
	h.exec(`INSERT INTO app_settings (key, value) VALUES ('parental.detect_min_confidence', '0.5'::jsonb)`)
	h.exec(`UPDATE app_settings SET value = '0.5'::jsonb WHERE key = 'parental.detect_min_confidence'`)
	if h.jobs() != 1 {
		t.Fatalf("a new line queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(w, 16, "detected")

	// a line that is not a number from 0 to 1 is the default 0.8, never a lower one
	h.exec(`UPDATE app_settings SET value = '"all"'::jsonb WHERE key = 'parental.detect_min_confidence'`)
	h.drain()
	h.want(w, -1, "")
	line, err := parental.DetectLine(context.Background(), h.db)
	if err != nil || line != parental.DefaultDetectLine {
		t.Fatalf("DetectLine = %v (%v), want the default", line, err)
	}

	// the key removed: the default again
	h.exec(`UPDATE app_settings SET value = '0.6'::jsonb WHERE key = 'parental.detect_min_confidence'`)
	h.drain()
	h.want(w, 16, "detected")
	h.exec(`DELETE FROM app_settings WHERE key = 'parental.detect_min_confidence'`)
	if h.jobs() != 1 {
		t.Fatalf("a removed line queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(w, -1, "")
}

// T06: the stage's role writes its own table and cannot write a video's tier, put a
// recompute job in by hand, or call the trigger functions.
func TestContentScreenRoleCannotWriteTiers(t *testing.T) {
	h := newPGHome(t)
	lib := h.library("role")
	v := h.video(lib)
	h.drain()
	var usable bool
	if err := h.db.QueryRow(`SELECT to_regrole('maktaba_fp_content_screen') IS NOT NULL
		AND pg_has_role(current_user, 'maktaba_fp_content_screen', 'MEMBER')`).Scan(&usable); err != nil {
		t.Fatal(err)
	}
	if !usable {
		t.Fatal("the scratch database has no maktaba_fp_content_screen role: slot 0224 did not make it")
	}
	as := func(q string, args ...any) error {
		tx, err := h.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SET LOCAL ROLE maktaba_fp_content_screen`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return err
		}
		return tx.Commit()
	}
	refused := func(what, q string, args ...any) {
		t.Helper()
		err := as(q, args...)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("%s as the stage's role: %v, want permission denied", what, err)
		}
	}
	refused("write videos.age_tier", `UPDATE videos SET age_tier = 0, age_tier_source = 'detected' WHERE id = $1`, v)
	refused("write videos.age_tier_admin", `UPDATE videos SET age_tier_admin = 0 WHERE id = $1`, v)
	refused("write a library's default", `UPDATE libraries SET age_tier = 0 WHERE id = $1`, lib)
	refused("delete a video", `DELETE FROM videos WHERE id = $1`, v)
	refused("put a recompute job in", `INSERT INTO parental_recompute_jobs (kind) VALUES ('all')`)
	refused("call the enqueue function", `SELECT parental_recompute_enqueue('all', '')`)
	refused("call the trigger function", `SELECT content_detect_line_changed()`)
	refused("move the line", `INSERT INTO app_settings (key, value) VALUES ('parental.detect_min_confidence', '0'::jsonb)`)
	refused("read users", `SELECT count(*) FROM users`)
	h.want(v, -1, "")

	// its own table it writes, and the trigger still queues the recompute for it
	if err := as(`INSERT INTO video_content_signals (video_id, tier, confidence, model, input_hash)
		VALUES ($1, 16, 0.9, 'm', 'h')`, v); err != nil {
		t.Fatalf("the stage's role writing its own table: %v", err)
	}
	if err := as(`SELECT title FROM videos WHERE id = $1`, v); err != nil {
		t.Fatalf("the stage's role reading videos: %v", err)
	}
	if h.jobs() != 1 {
		t.Fatalf("the stage's write queued %d jobs, want 1", h.jobs())
	}
	h.drain()
	h.want(v, 16, "detected")
}

// slot0224State is the slot's own objects, each by name.
func slot0224State(t *testing.T, db *sql.DB) (table bool, objects int, detected bool) {
	t.Helper()
	if err := db.QueryRow(`SELECT to_regclass('public.video_content_signals') IS NOT NULL,
		(SELECT count(*) FROM pg_proc WHERE proname IN ('content_signal_changed', 'content_detect_line_changed'))
		+ (SELECT count(*) FROM pg_trigger WHERE tgname IN ('video_content_signals_recompute',
			'app_settings_detect_line_ins', 'app_settings_detect_line_upd', 'app_settings_detect_line_del'))
		+ (SELECT count(*) FROM pg_class WHERE relname = 'video_content_signals_review_idx'),
		COALESCE((SELECT pg_get_constraintdef(oid) LIKE '%detected%' FROM pg_constraint
			WHERE conname = 'videos_age_tier_source_valid'), false)`).Scan(&table, &objects, &detected); err != nil {
		t.Fatal(err)
	}
	return table, objects, detected
}

// T05: slot 0224 up, down and up again on Postgres.
func TestSlot0224UpAndDownOnPostgres(t *testing.T) {
	h := newPGHome(t) // migrated to the head: 0224 is up
	db := h.db
	up, down := gooseStatements(t, "0224_content_signals.sql")
	if table, objects, detected := slot0224State(t, db); !table || objects != 7 || !detected {
		t.Fatalf("after the migrations: table=%v objects=%d detected=%v", table, objects, detected)
	}
	var public bool
	if err := db.QueryRow(`SELECT has_function_privilege('public', 'content_signal_changed()', 'EXECUTE')
		OR has_function_privilege('public', 'content_detect_line_changed()', 'EXECUTE')`).Scan(&public); err != nil || public {
		t.Fatalf("the trigger functions are callable by anyone: %v (%v)", public, err)
	}
	// the table's own checks
	lib := h.library("slot")
	v, kept := h.video(lib), h.video(lib)
	for what, q := range map[string]string{
		"a tier off the scale":       `INSERT INTO video_content_signals (video_id, tier) VALUES ($1, 12)`,
		"a confidence over 1":        `INSERT INTO video_content_signals (video_id, tier, confidence) VALUES ($1, 9, 1.5)`,
		"an unknown not_checked":     `INSERT INTO video_content_signals (video_id, not_checked) VALUES ($1, 'later')`,
		"a negative count of runs":   `INSERT INTO video_content_signals (video_id, failed_runs) VALUES ($1, -1)`,
		"a tier on a not-checked":    `INSERT INTO video_content_signals (video_id, tier, not_checked) VALUES ($1, 9, 'no_transcript')`,
		"an unknown source on video": `UPDATE videos SET age_tier = 9, age_tier_source = 'guessed' WHERE id = $1`,
	} {
		if _, err := db.Exec(q, v); err == nil {
			t.Fatalf("%s was accepted", what)
		}
	}
	// a detected tier and a library default, then down: the detected source goes, the
	// tier stays until the recompute, which is asked for, rates the video from what is
	// left (review finding 18: clearing the tier first showed the video, meanwhile, to
	// a child whose unrated content counts as all ages)
	h.libraryTier(lib, 9)
	h.want(kept, 9, "library_default")
	h.signal(v, 16, 0.9)
	h.exec(`INSERT INTO app_settings (key, value) VALUES ('parental.detect_min_confidence', '0.7'::jsonb)`)
	h.drain()
	h.want(v, 16, "detected")

	run(t, db, "down", down)
	if table, objects, detected := slot0224State(t, db); table || objects != 0 || detected {
		t.Fatalf("after down: table=%v objects=%d detected=%v", table, objects, detected)
	}
	var settings int
	if err := db.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'parental.detect_min_confidence'`).Scan(&settings); err != nil || settings != 0 {
		t.Fatalf("after down: %d settings of the slot (%v)", settings, err)
	}
	h.want(v, 16, "")
	h.want(kept, 9, "library_default")
	// the role keeps nothing of what the Up granted it (review finding 18)
	var role bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_fp_content_screen')`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role {
		var granted int
		if err := db.QueryRow(`SELECT count(*) FROM information_schema.role_table_grants
			WHERE grantee = 'maktaba_fp_content_screen' AND table_schema = 'public'`).Scan(&granted); err != nil || granted != 0 {
			t.Fatalf("after down the stage's role still has %d table grants (%v)", granted, err)
		}
	} else {
		t.Log("this Postgres has no maktaba_fp_content_screen role: its grants are not checked")
	}
	if _, err := db.Exec(`UPDATE videos SET age_tier = 9, age_tier_source = 'detected' WHERE id = $1`, v); err == nil {
		t.Fatal("after down the source check still takes detected")
	}
	if h.jobs() != 1 {
		t.Fatalf("down queued %d recompute jobs, want 1", h.jobs())
	}
	// app_settings works without the slot's triggers
	h.exec(`INSERT INTO app_settings (key, value) VALUES ('parental.detect_min_confidence', '0.9'::jsonb)`)
	h.exec(`DELETE FROM app_settings WHERE key = 'parental.detect_min_confidence'`)

	run(t, db, "up", up)
	run(t, db, "up twice", up) // every statement can run again
	if table, objects, detected := slot0224State(t, db); !table || objects != 7 || !detected {
		t.Fatalf("after up again: table=%v objects=%d detected=%v", table, objects, detected)
	}
	h.drain()
	h.want(v, 9, "library_default")
}

// T05: slot 0224 on SQLite.
func TestSlot0224OnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "s.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	depUp, _ := gooseStatements(t, "0029_users.sqlite.sql")
	run(t, db, "0029_users.sqlite.sql", depUp)
	// the one column of videos the table points at
	run(t, db, "videos", []string{`CREATE TABLE videos (id TEXT PRIMARY KEY)`, `INSERT INTO videos (id) VALUES ('v1'), ('v2')`})
	// the stage registry, with the columns the slot's seed names
	run(t, db, "job_stages", []string{`CREATE TABLE job_stages (stage TEXT PRIMARY KEY, label TEXT NOT NULL,
		subject_kinds TEXT NOT NULL, pool TEXT NOT NULL, lane TEXT NOT NULL, lane_order INTEGER NOT NULL,
		version INTEGER NOT NULL, debounce_sec INTEGER NOT NULL, debounce_max_sec INTEGER NOT NULL,
		max_attempts INTEGER NOT NULL, edges TEXT NOT NULL, may_request TEXT NOT NULL, entry_for TEXT NOT NULL,
		legacy INTEGER NOT NULL, hidden INTEGER NOT NULL, external_write INTEGER NOT NULL,
		write_mode TEXT NOT NULL, placement TEXT NOT NULL, enabled INTEGER NOT NULL, disabled_reason TEXT)`})
	up, down := gooseStatements(t, "0224_content_signals.sqlite.sql")
	run(t, db, "up", up)
	run(t, db, "up twice", up)
	var stages int
	if err := db.QueryRow(`SELECT count(*) FROM job_stages WHERE stage = 'content_screen' AND placement = 'home'`).Scan(&stages); err != nil || stages != 1 {
		t.Fatalf("the stage's row: %d (%v), want 1", stages, err)
	}
	run(t, db, "rows", []string{
		`INSERT INTO video_content_signals (video_id, tier, confidence, categories, model, input_hash)
			VALUES ('v1', 16, 0.9, '{"coarse_language":{"level":2,"evidence":[12.5]}}', 'm', 'h')`,
		`INSERT INTO video_content_signals (video_id, not_checked) VALUES ('v2', 'no_transcript')`,
	})
	for what, q := range map[string]string{
		"a tier off the scale":      `UPDATE video_content_signals SET tier = 12 WHERE video_id = 'v1'`,
		"a confidence over 1":       `UPDATE video_content_signals SET confidence = 2 WHERE video_id = 'v1'`,
		"an unknown not_checked":    `UPDATE video_content_signals SET not_checked = 'later' WHERE video_id = 'v2'`,
		"a tier on a not-checked":   `UPDATE video_content_signals SET tier = 9 WHERE video_id = 'v2'`,
		"a second row for a video":  `INSERT INTO video_content_signals (video_id) VALUES ('v1')`,
		"a video that is not there": `INSERT INTO video_content_signals (video_id) VALUES ('nope')`,
	} {
		if _, err := db.Exec(q); err == nil {
			t.Fatalf("%s was accepted", what)
		}
	}
	// the row goes with its video
	run(t, db, "delete", []string{`DELETE FROM videos WHERE id = 'v1'`})
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM video_content_signals`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("after the video went: %d signals (%v), want 1", n, err)
	}
	run(t, db, "down", down)
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('video_content_signals', 'video_content_signals_review_idx')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after down: %d of the slot's objects (%v)", n, err)
	}
	run(t, db, "up again", up)
}

func TestManifestListsSlot0224(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `0224` | `0224_content_signals.sql` |") {
		t.Fatal("MANIFEST.md has no row for slot 0224")
	}
	for _, f := range []string{"0224_content_signals.sql", "0224_content_signals.sqlite.sql"} {
		if _, err := os.Stat(filepath.Join(migrationsDir(t), f)); err != nil {
			t.Fatal(err)
		}
	}
}
