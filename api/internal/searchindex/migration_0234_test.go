package searchindex

// Slot 0234 (Epic 49, story 49.7): the documents' normalised text as stored generated
// columns, the BM25 indexes on them, and slot 0232's expression indexes dropped.

import (
	"context"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

func TestSlot0234UpAndDownOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t) // migrated to the head: 0234 is up
	up, down := gooseStatements(t, "0234_video_search_docs_text.sql")
	cols := func() int {
		return scalar[int](t, db, `SELECT count(*) FROM information_schema.columns
			WHERE table_name = 'video_search_docs' AND column_name IN ('search_text', 'stem_text') AND is_generated = 'ALWAYS'`)
	}
	if cols() != 2 {
		t.Fatalf("after the migrations: %d generated columns, want 2", cols())
	}
	// the columns follow the document's text, whoever writes it
	lib := scalar[string](t, db, `INSERT INTO libraries (name, roots) VALUES ('m0234', ARRAY['/tmp/m0234']) RETURNING id::text`)
	vid := scalar[string](t, db, `INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime)
		VALUES ($1, repeat('d', 64), '/tmp/m0234/a.mp4', 'a.mp4', 1, now()) RETURNING id::text`, lib)
	run(t, db, "a document", []string{`INSERT INTO video_search_docs (video_id, library_id, title, speakers, body, transcript_id)
		VALUES ('` + vid + `', '` + lib + `', 'درس في الصبر', 'الشيخ أحمد', 'والصابرون عند المصيبة', gen_random_uuid())`})
	got := scalar[string](t, db, `SELECT search_text || '|' || stem_text FROM video_search_docs`)
	if want := "درس في الصبر\nالشيخ احمد\nوالصابرون عند المصيبه|درس في الصبر\nالشيخ احمد\nصابرون عند المصيبه"; got != want {
		t.Fatalf("columns:\n%q\nwant\n%q", got, want)
	}
	run(t, db, "a title", []string{`UPDATE video_search_docs SET title = 'إسلام'`})
	if got := scalar[string](t, db, `SELECT split_part(search_text, E'\n', 1) FROM video_search_docs`); got != "اسلام" {
		t.Fatalf("search_text after the title changed: %q", got)
	}
	run(t, db, "down", down)
	if cols() != 0 {
		t.Fatalf("after down: %d columns left", cols())
	}
	run(t, db, "up", up)
	run(t, db, "up twice", up)
	if cols() != 2 {
		t.Fatalf("after up again: %d columns", cols())
	}
}

// A home whose API built slot 0232's expression indexes: once slot 0234 is up, Ensure
// builds the column indexes and drops the old ones.
func TestSlot0234ReplacesTheExpressionIndexes(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	if !textsearchHere(t, db) {
		t.Skip("no pg_textsearch on this server (PostgreSQL 16, or not preloaded): the replica job runs this on 18")
	}
	ctx := context.Background()
	_, down := gooseStatements(t, "0234_video_search_docs_text.sql")
	up, _ := gooseStatements(t, "0234_video_search_docs_text.sql")
	run(t, db, "down to 0233", down)
	if ran, err := Ensure(ctx, db); err != nil || len(ran) != 2 || !strings.Contains(ran[0], "ARRAY[") {
		t.Fatalf("slot 0232's indexes: %q (%v)", ran, err)
	}
	run(t, db, "up", up)
	ran, err := Ensure(ctx, db)
	if err != nil || len(ran) != 4 {
		t.Fatalf("Ensure after slot 0234: %q (%v), want two builds and two drops", ran, err)
	}
	if !strings.Contains(ran[0], "video_search_docs_bm25_text") || !strings.Contains(ran[1], "video_search_docs_bm25_stem_text") ||
		!strings.HasPrefix(ran[2], "DROP INDEX CONCURRENTLY IF EXISTS public.video_search_docs_bm25") ||
		!strings.HasPrefix(ran[3], "DROP INDEX CONCURRENTLY IF EXISTS public.video_search_docs_bm25") {
		t.Fatalf("Ensure after slot 0234 ran %q", ran)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM pg_class WHERE relname IN ('video_search_docs_bm25', 'video_search_docs_bm25_stem')`); n != 0 {
		t.Fatalf("%d of slot 0232's indexes left", n)
	}
	if ready, err := Ready(ctx, db); err != nil || !ready {
		t.Fatalf("Ready = %v (%v)", ready, err)
	}
	if ran, err := Ensure(ctx, db); err != nil || len(ran) != 0 {
		t.Fatalf("a second Ensure ran %q (%v)", ran, err)
	}
}
