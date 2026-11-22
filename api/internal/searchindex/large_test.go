package searchindex

// The large-table case (Epic 49 story 49.6, issue #1479): a home's 45,000 documents. The
// BM25 build runs after the start, in the background, with no time limit. It takes longer
// than any bound a start's migration has (5 minutes by default), real transcripts being far
// longer than the ones here. Meanwhile the table takes writes and reads and keyword search
// is not Ready. A build cut short leaves an invalid index that the next round drops and
// builds again. The document written during the build is found once the indexes are ready.
// Its size makes it the replica job's (MAKTABA_TEST_SEARCH_LARGE=1, on its PostgreSQL 18).

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

func TestLargeLibraryBuildsInTheBackground(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_SEARCH_LARGE") != "1" {
		t.Skip("MAKTABA_TEST_SEARCH_LARGE unset: the 45,000-document build is the replica job's")
	}
	db, _ := clustertest.ScratchDB(t)
	if !textsearchHere(t, db) {
		t.Fatal("MAKTABA_TEST_SEARCH_LARGE needs a server with pg_textsearch preloaded")
	}
	n := 45000
	if v, err := strconv.Atoi(os.Getenv("MAKTABA_TEST_SEARCH_DOCS")); err == nil && v > 0 {
		n = v
	}
	run(t, db, "the documents", []string{
		`INSERT INTO libraries (id, name, roots) VALUES ('00000000-0000-4000-8000-000000490601', 'large', ARRAY['/large'])`,
		`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime)
		 SELECT ('00000000-0000-4000-8000-' || lpad(to_hex(i), 12, '0'))::uuid, '00000000-0000-4000-8000-000000490601',
		        lpad(to_hex(i), 64, '0'), '/large/' || i, i || '.mp4', 1, now()
		   FROM generate_series(1, ` + strconv.Itoa(n) + `) i`,
		// 300 words a document, from a vocabulary of Arabic and English words
		`INSERT INTO video_search_docs (video_id, library_id, title, speakers, body, transcript_id)
		 SELECT ('00000000-0000-4000-8000-' || lpad(to_hex(i), 12, '0'))::uuid, '00000000-0000-4000-8000-000000490601',
		        'درس ' || i, 'الشيخ',
		        (SELECT string_agg((ARRAY['الصبر','الصلاة','الزكاة','المصيبة','العلم','الرحمة','القرآن','الحديث',
		                                  'الإيمان','التوبة','الذكر','الدعاء','الجنة','الصيام','الحج','الأخلاق',
		                                  'العدل','الصدق','الأمانة','patience','prayer','knowledge','mercy','charity'])
		                            [1 + ((i * 7 + g * 13) % 24)], ' ') FROM generate_series(1, 300) g),
		        gen_random_uuid()
		   FROM generate_series(1, ` + strconv.Itoa(n) + `) i`,
	})
	ctx := context.Background()
	// a migrated database has the table and no BM25 index: search is on its lines
	if ready, err := Ready(ctx, db); err != nil || ready {
		t.Fatalf("Ready before any build = %v (%v)", ready, err)
	}

	// 1. a build cut short (the bound a migration would have put on it): an invalid index
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, _, err := BuildOnce(short, db)
	cancel()
	if err == nil {
		t.Skip("the build finished within 2 s: this machine is too fast for the cut-short case; raise MAKTABA_TEST_SEARCH_DOCS")
	}
	if ready, _ := Ready(ctx, db); ready {
		t.Fatal("Ready after a build cut short")
	}
	invalid := scalar[int](t, db, `SELECT count(*) FROM pg_index x JOIN pg_class c ON c.oid = x.indexrelid
		WHERE c.relname LIKE 'video_search_docs_bm25%' AND NOT x.indisvalid`)
	t.Logf("a build cut short after 2 s left %d invalid index(es)", invalid)

	// 2. the background loop: the next round drops what was left and builds both
	Every = 24 * time.Hour // one round here
	loopCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	start := time.Now()
	go func() {
		Run(loopCtx, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	// while it builds: writes and reads go through at once, and search is not Ready
	const late = "00000000-0000-4000-8000-0000004906ff"
	var building bool
	for i := 0; ; i++ {
		var phase sql.NullString
		_ = db.QueryRow(`SELECT phase FROM pg_stat_progress_create_index WHERE relid = 'video_search_docs'::regclass`).Scan(&phase)
		if phase.Valid {
			building = true
			break
		}
		if ready, _ := Ready(ctx, db); ready || i > 600 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !building {
		t.Fatal("no build seen in progress")
	}
	timed := func(what, q string, args ...any) {
		t.Helper()
		t0 := time.Now()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s during the build: %v", what, err)
		}
		if d := time.Since(t0); d > 2*time.Second {
			t.Errorf("%s waited %s for the build", what, d)
		}
	}
	timed("a new video", `INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime)
		VALUES ($1, '00000000-0000-4000-8000-000000490601', repeat('f', 64), '/large/late', 'late.mp4', 1, now())`, late)
	timed("its document", `INSERT INTO video_search_docs (video_id, library_id, title, body, transcript_id)
		VALUES ($1, '00000000-0000-4000-8000-000000490601', 'محاضرة متأخرة', 'كتبت أثناء البناء والمصابرة', gen_random_uuid())`, late)
	timed("a document rebuilt", `UPDATE video_search_docs SET body = body || ' الشكر', built_at = now()
		WHERE video_id = '00000000-0000-4000-8000-000000000001'`)
	timed("a read", `SELECT count(*) FROM video_search_docs WHERE library_id = '00000000-0000-4000-8000-000000490601'`)
	if ready, _ := Ready(ctx, db); ready {
		t.Error("Ready while the build is still running")
	}

	deadline := time.Now().Add(20 * time.Minute)
	for {
		if ready, _ := Ready(ctx, db); ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the indexes were not ready after 20 minutes")
		}
		time.Sleep(time.Second)
	}
	t.Logf("%d documents: both indexes built in the background in %s", n, time.Since(start).Round(time.Second))
	stop()
	<-done

	// the document written during the build is in the index
	if _, err := db.Exec(`SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	q := `SELECT video_id::text FROM video_search_docs
		ORDER BY search_text OPERATOR(textsearch.<@>) textsearch.to_bm25query(public.maktaba_normalize($1), 'video_search_docs_bm25_text') LIMIT 1`
	// (its words as another spelling: أ and ة folded)
	if got := scalar[string](t, db, q, "محاضره متاخره"); got != late {
		t.Errorf("the document written during the build: top is %s", got)
	}
	var plan strings.Builder
	rows, err := db.Query(`EXPLAIN (COSTS OFF) `+q, "محاضره متاخره")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var l string
		_ = rows.Scan(&l)
		plan.WriteString(l + "\n")
	}
	_ = rows.Close()
	if !strings.Contains(plan.String(), "Index Scan using video_search_docs_bm25_text ") {
		t.Errorf("not answered by the index:\n%s", plan.String())
	}
}
