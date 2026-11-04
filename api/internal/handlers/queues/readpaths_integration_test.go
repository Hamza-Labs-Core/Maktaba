//go:build integration

package queues

// The queue reads behind the library dashboard and the queue snapshot against
// a real, migrated Postgres (slot 0175):
//
//   - processing_done_subjects, which the processing_jobs triggers keep, says
//     what count(DISTINCT subject_id) over the done rows says, through every way
//     a row turns done, moves or goes;
//   - on a seeded table the hot reads use bounded plans (no sequential scan of
//     processing_jobs, no index read whole unless it is a partial index of the
//     live, failed or awaiting rows), and answer well under a second.
//
//	MAKTABA_TEST_PG_URL=postgres://u:p@127.0.0.1:5432/postgres?sslmode=disable \
//	    go test -tags integration ./internal/handlers/queues/ -run ReadPaths -v
//
// MAKTABA_QUEUE_SEED_VIDEOS sets the seed's size (default 5000 videos, about
// 105k jobs; 150000 makes the home's 3M).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// rawDoneSQL is what GET /api/queues/libraries counted before slot 0175, the
// reference the summary must match.
const rawDoneSQL = `
SELECT library_id::text, stage, count(DISTINCT subject_id), max(COALESCE(finished_at, created_at))
  FROM processing_jobs WHERE state = 'done' AND library_id IS NOT NULL
 GROUP BY library_id, stage`

const summaryDoneSQL = `
SELECT library_id::text, stage, count(*), max(last_at)
  FROM processing_done_subjects GROUP BY library_id, stage`

func doneByStage(t *testing.T, db *sql.DB, q string) map[string]string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var lib, stage string
		var n int
		var last time.Time
		if err := rows.Scan(&lib, &stage, &n, &last); err != nil {
			t.Fatal(err)
		}
		out[lib+"/"+stage] = fmt.Sprintf("%d @ %s", n, last.UTC().Format(time.RFC3339Nano))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func sameDone(t *testing.T, db *sql.DB, step string) {
	t.Helper()
	raw, sum := doneByStage(t, db, rawDoneSQL), doneByStage(t, db, summaryDoneSQL)
	keys := map[string]bool{}
	for k := range raw {
		keys[k] = true
	}
	for k := range sum {
		keys[k] = true
	}
	var diff []string
	for k := range keys {
		if raw[k] != sum[k] {
			diff = append(diff, fmt.Sprintf("%s: raw %q summary %q", k, raw[k], sum[k]))
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Fatalf("%s: the summary differs from the raw counts:\n%s", step, strings.Join(diff, "\n"))
	}
}

func TestReadPaths_DoneSummaryMatchesRawCounts(t *testing.T) {
	db := parityDB(t)
	const (
		libA = "17500000-0000-4000-8000-00000000000a"
		libB = "17500000-0000-4000-8000-00000000000b"
	)
	mustExec(t, db, `INSERT INTO libraries (id, name, roots) VALUES ($1, 'A', ARRAY['/a']), ($2, 'B', ARRAY['/b'])`, libA, libB)
	vid := func(i int) string { return fmt.Sprintf("17500000-0000-4000-8001-%012d", i) }
	for i := 1; i <= 6; i++ {
		lib := libA
		if i > 4 {
			lib = libB
		}
		mustExec(t, db, `INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime)
			VALUES ($1, $2, repeat(chr(96 + $3::int), 64), '/v/' || $3::text || '.mp4', $3::text || '.mp4', 1, now())`, vid(i), lib, i)
	}
	sameDone(t, db, "empty")

	// a job that runs and finishes (the workers' path: pending, running, done)
	var id int64
	if err := db.QueryRow(`INSERT INTO processing_jobs (video_id, stage, state) VALUES ($1, 'probe', 'pending') RETURNING id`, vid(1)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE processing_jobs SET state = 'running', started_at = now() WHERE id = $1`, id)
	sameDone(t, db, "running")
	mustExec(t, db, `UPDATE processing_jobs SET state = 'done', finished_at = now() - interval '1 hour', outcome = 'changed' WHERE id = $1`, id)
	sameDone(t, db, "done")
	// a heartbeat-like write on a done row changes nothing
	mustExec(t, db, `UPDATE processing_jobs SET progress = '{"x":1}' WHERE id = $1`, id)
	sameDone(t, db, "progress write")

	// the same subject done again later: one subject, a later time
	mustExec(t, db, `INSERT INTO processing_jobs (video_id, stage, state, finished_at, outcome) VALUES ($1, 'probe', 'done', now(), 'unchanged')`, vid(1))
	sameDone(t, db, "done again")
	// an older run inserted done does not move the time back
	mustExec(t, db, `INSERT INTO processing_jobs (video_id, stage, state, finished_at, outcome) VALUES ($1, 'probe', 'done', now() - interval '3 days', 'unchanged')`, vid(1))
	sameDone(t, db, "an older run")

	// many subjects and stages in one statement; a failed and a cancelled row add nothing
	mustExec(t, db, `INSERT INTO processing_jobs (video_id, stage, state, finished_at, outcome)
		SELECT v.id, s.stage, 'done', now() - (random() * interval '5 days'), 'changed'
		  FROM videos v CROSS JOIN (VALUES ('probe'), ('link'), ('thumb_small')) s(stage)`)
	mustExec(t, db, `INSERT INTO processing_jobs (video_id, stage, state, finished_at, error) VALUES ($1, 'transcribe', 'failed', now(), 'x')`, vid(2))
	mustExec(t, db, `INSERT INTO processing_jobs (video_id, stage, state, finished_at) VALUES ($1, 'transcribe', 'cancelled', now())`, vid(3))
	sameDone(t, db, "bulk")

	// the retention: done rows older than a newer done row go, the subject stays
	mustExec(t, db, `DELETE FROM processing_jobs j WHERE j.state = 'done' AND EXISTS (SELECT 1 FROM processing_jobs k
		WHERE k.subject_kind = j.subject_kind AND k.subject_id = j.subject_id AND k.stage = j.stage
		  AND k.state = 'done' AND k.id > j.id AND k.finished_at >= j.finished_at)`)
	sameDone(t, db, "retention")

	// a library merge moves the rows (merge.go), then the merged library goes
	mustExec(t, db, `UPDATE processing_jobs SET library_id = $1 WHERE library_id = $2 AND subject_kind <> 'library'`, libA, libB)
	sameDone(t, db, "rows moved to another library")
	mustExec(t, db, `UPDATE processing_jobs SET library_id = $1 WHERE video_id = $2`, libB, vid(5))
	sameDone(t, db, "moved back")

	// a removed video takes its subject away (ON DELETE CASCADE)
	mustExec(t, db, `DELETE FROM videos WHERE id = $1`, vid(4))
	sameDone(t, db, "video removed")

	// a done row deleted alone, the subject's last
	mustExec(t, db, `DELETE FROM processing_jobs WHERE video_id = $1 AND stage = 'link'`, vid(3))
	sameDone(t, db, "last done row deleted")

	// the read path: the library rows built from the summary match the raw counts
	h := &Handler{DB: db, Hub: ws.NewHub(), NowFunc: time.Now}
	rows, err := h.buildLibraryRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw := doneByStage(t, db, rawDoneSQL)
	for _, r := range rows {
		for _, c := range r.Strip {
			want := 0
			if v, ok := raw[r.LibraryID+"/"+c.Stage]; ok {
				want, _ = strconv.Atoi(strings.SplitN(v, " ", 2)[0])
			}
			if want > c.Total {
				want = c.Total
			}
			if c.Done != want {
				t.Errorf("%s %s: done %d, raw %d", r.Name, c.Stage, c.Done, want)
			}
		}
	}

	// a removed library takes its rows with it
	mustExec(t, db, `DELETE FROM libraries WHERE id = $1`, libB)
	sameDone(t, db, "library removed")
}

func seedVideos(t *testing.T) int {
	if v := os.Getenv("MAKTABA_QUEUE_SEED_VIDEOS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 100 {
			t.Fatalf("MAKTABA_QUEUE_SEED_VIDEOS=%q", v)
		}
		return n
	}
	return 5000
}

// seedQueue fills processing_jobs the way the home's looks: ~20 done rows per
// video over 30 days (some stages run many times), one pending row per video,
// a few running and failed. The bulk insert skips the row triggers (as a
// migration's backfill would see the rows) and then runs slot 0175's backfill.
func seedQueue(t *testing.T, db *sql.DB, videos int) {
	t.Helper()
	start := time.Now()
	for _, q := range []string{
		`ALTER TABLE processing_jobs DISABLE TRIGGER USER`,
		`INSERT INTO libraries (id, name, roots)
		 SELECT ('17500000-0000-4000-8000-' || lpad(to_hex(l), 12, '0'))::uuid, 'lib ' || l, ARRAY['/x' || l]
		   FROM generate_series(1, 20) l`,
		fmt.Sprintf(`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime)
		 SELECT ('17500000-0000-4000-8002-' || lpad(to_hex(i), 12, '0'))::uuid,
		        ('17500000-0000-4000-8000-' || lpad(to_hex(1 + i %% 20), 12, '0'))::uuid,
		        md5(i::text) || md5((i + 1)::text), '/x/' || i || '.mp4', i || '.mp4', 1, now()
		   FROM generate_series(1, %d) i`, videos),
		`INSERT INTO processing_jobs (video_id, library_id, subject_kind, subject_id, stage, state, outcome, created_at, finished_at)
		 SELECT v.id, v.library_id, 'video', v.id, s.stage, 'done',
		        CASE WHEN r % 3 = 0 THEN 'skipped_unchanged' ELSE 'changed' END,
		        now() - (random() * interval '30 days') - interval '1 minute', now() - (random() * interval '30 days')
		   FROM videos v
		   CROSS JOIN (VALUES ('probe', 1), ('content_hash', 1), ('thumb_small', 3), ('link', 3), ('thumb_hash', 2),
		                      ('transcribe', 1), ('subtitle_gen', 1), ('index', 1), ('chapter_infer', 1),
		                      ('chapter_thumbs', 1), ('topic_assign', 1), ('fingerprint', 4)) s(stage, runs)
		   CROSS JOIN LATERAL generate_series(1, s.runs) r`,
		`INSERT INTO processing_jobs (video_id, library_id, subject_kind, subject_id, stage, state, created_at)
		 SELECT v.id, v.library_id, 'video', v.id, 'thumbnail', 'pending', now() - (random() * interval '10 days') FROM videos v`,
		`INSERT INTO processing_jobs (video_id, library_id, subject_kind, subject_id, stage, state, created_at, claimed_at, started_at, last_heartbeat_at)
		 SELECT v.id, v.library_id, 'video', v.id, 'transcribe', 'running', now(), now(), now(), now()
		   FROM videos v WHERE hashtext(v.id::text) % 500 = 0`,
		`INSERT INTO processing_jobs (video_id, library_id, subject_kind, subject_id, stage, state, error_cause, error, created_at, finished_at)
		 SELECT v.id, v.library_id, 'video', v.id, 'subtitle_gen', 'failed', 'nas_timeout', 'timed out',
		        now() - interval '2 days', now() - interval '1 day'
		   FROM videos v WHERE hashtext(v.id::text) % 30 = 0`,
		`ALTER TABLE processing_jobs ENABLE TRIGGER USER`,
		// slot 0175's backfill
		`INSERT INTO processing_done_subjects AS d (library_id, stage, subject_id, subject_kind, last_at)
		 SELECT library_id, stage, subject_id, min(subject_kind), max(COALESCE(finished_at, created_at))
		   FROM processing_jobs WHERE state = 'done' AND library_id IS NOT NULL
		  GROUP BY library_id, stage, subject_id
		 ON CONFLICT (library_id, stage, subject_id) DO UPDATE SET last_at = GREATEST(d.last_at, EXCLUDED.last_at)`,
		`VACUUM ANALYZE processing_jobs`,
		`VACUUM ANALYZE processing_done_subjects`,
	} {
		mustExec(t, db, q)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM processing_jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d jobs for %d videos in %s", n, videos, time.Since(start).Round(time.Millisecond))
}

// boundedPartial are the partial indexes a plan may read whole: each holds
// only live, failed or awaiting rows, never the done history.
var boundedPartial = map[string]bool{
	"processing_jobs_live_idx":                   true,
	"processing_jobs_one_live_per_subject_stage": true,
	"processing_jobs_claimable_idx":              true,
	"processing_jobs_reaper_idx":                 true,
	"processing_jobs_failed_cause_idx":           true,
	"processing_jobs_awaiting_review_idx":        true,
	"processing_jobs_parked_idx":                 true,
}

type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	IndexName    string     `json:"Index Name"`
	IndexCond    string     `json:"Index Cond"`
	Plans        []planNode `json:"Plans"`
}

// unbounded lists the plan's reads of processing_jobs that follow the table's
// size: a sequential scan, or a whole read of an index of every row.
func unbounded(n planNode, out *[]string) {
	if n.RelationName == "processing_jobs" {
		switch n.NodeType {
		case "Seq Scan":
			*out = append(*out, "Seq Scan")
		case "Index Scan", "Index Only Scan", "Bitmap Index Scan":
			if n.IndexCond == "" && !boundedPartial[n.IndexName] {
				*out = append(*out, n.NodeType+" of "+n.IndexName+" with no condition")
			}
		}
	}
	if n.NodeType == "Bitmap Index Scan" && n.IndexCond == "" && !boundedPartial[n.IndexName] &&
		strings.HasPrefix(n.IndexName, "processing_jobs_") {
		*out = append(*out, "Bitmap Index Scan of "+n.IndexName+" with no condition")
	}
	for _, c := range n.Plans {
		unbounded(c, out)
	}
}

func TestReadPaths_BoundedOnALargeTable(t *testing.T) {
	db := parityDB(t)
	seedQueue(t, db, seedVideos(t))
	now := time.Now()

	// every hot read, with the arguments its caller passes
	reads := []struct {
		name string
		q    string
		args []any
	}{
		{"library done", libraryDoneSQL, nil},
		{"library live", libraryLiveSQL, nil},
		{"library ended", libraryEndedSQL, nil},
		{"library failed", libraryFailedSQL, nil},
		{"live counts", liveCountsSQL, []any{now}},
		{"done counts", doneCountsSQL, []any{now.Add(-24 * time.Hour), now.Add(-rateWindow)}},
		{"wave", waveDoneSQL, nil},
		{"health window done", windowDoneSQL, []any{now.Add(-time.Hour)}},
		{"failure groups", failureGroupsSQL, []any{""}},
		{"legacy failures", legacyFailuresSQL, []any{""}},
		{"awaiting review", awaitingReviewSQL, []any{""}},
		{"stale ready", staleReadySQL, []any{now, "{thumbnail,probe}"}},
		{"parked", parkedSQL, nil},
	}
	for _, r := range reads {
		var raw string
		if err := db.QueryRow("EXPLAIN (FORMAT JSON) "+r.q, r.args...).Scan(&raw); err != nil {
			t.Fatalf("%s: explain: %v", r.name, err)
		}
		var plans []struct {
			Plan planNode `json:"Plan"`
		}
		if err := json.Unmarshal([]byte(raw), &plans); err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		var bad []string
		unbounded(plans[0].Plan, &bad)
		if len(bad) > 0 {
			t.Errorf("%s reads processing_jobs whole: %s\n%s", r.name, strings.Join(bad, "; "), raw)
		}
	}

	// the reads themselves, built fresh (no cache)
	h := &Handler{DB: db, Hub: ws.NewHub(), NowFunc: time.Now}
	ctx := context.Background()
	for _, b := range []struct {
		name string
		run  func() error
	}{
		{"library rows", func() error { _, err := h.buildLibraryRows(ctx); return err }},
		{"snapshot", func() error { _, err := h.buildSnapshot(ctx); return err }},
	} {
		if err := b.run(); err != nil { // warm
			t.Fatalf("%s: %v", b.name, err)
		}
		start := time.Now()
		if err := b.run(); err != nil {
			t.Fatalf("%s: %v", b.name, err)
		}
		d := time.Since(start)
		t.Logf("%s: %s", b.name, d.Round(time.Millisecond))
		if d > time.Second {
			t.Errorf("%s took %s, want well under a second", b.name, d)
		}
	}

	// through the cache: concurrent readers, one build
	rd := h.reads()
	before := rd.libraries.Builds()
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			_, err := rd.libraries.Get(ctx, now, h.buildLibraryRows)
			errs <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := rd.libraries.Builds() - before; n != 1 {
		t.Fatalf("16 concurrent reads ran %d builds, want 1", n)
	}
}
