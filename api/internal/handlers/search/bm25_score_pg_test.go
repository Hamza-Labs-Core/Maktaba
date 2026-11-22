package search

// A BM25 leg's scores are each row's own, whatever the plan (story 49.9). With the index
// scan they are the scan's (bm25_get_current_score); a plan without one (a small table read
// whole and sorted) has no scan to take them from, and bm25_get_current_score then gives the
// last score of the connection's last index scan, another search's. The leg checks its
// first row against the operator and asks again when they differ.

import (
	"context"
	"database/sql"
	"math"
	"testing"
)

func TestBM25_ScoresAreEachRowsOwn(t *testing.T) {
	db := bm25DB(t)
	db.SetMaxOpenConns(1) // one connection: the extension's last score is that connection's
	db.SetMaxIdleConns(1)
	for i, lines := range [][]string{
		repeat("الصبر عند المصيبة", 5),
		{"الصبر مرة", "كلام آخر", "حديث"},
		{"الصبر", "الصبر الصبر", "باب الصبر"},
		{"شيء آخر تماما", "الصبر نادر هنا", "وكلام كثير بعده", "وكلام", "وكلام"},
	} {
		v := pgUUID(601 + i)
		tr := pgVideo(t, db, v, libA, "درس")
		pgTranscript(t, db, pgUUID(701+i), v, tr, true, lines...)
		pgDoc(t, db, v)
	}
	buildIndexes(t, db)
	h := &Handler{DB: db}
	for _, leg := range [][3]string{
		{"video_search_docs_bm25_text", "search_text", "maktaba_normalize"},
		{"video_search_docs_bm25_stem_text", "stem_text", "maktaba_stem_prepare"},
	} {
		scoresAreEachRowsOwn(t, db, h, leg[0], leg[1], leg[2])
	}
}

// scoresAreEachRowsOwn checks one index's leg: with the index scan, and without it after
// another search's scan on the same connection.
func scoresAreEachRowsOwn(t *testing.T, db *sql.DB, h *Handler, index, col, fn string) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{`RESET enable_seqscan`, `RESET enable_sort`, `RESET enable_indexscan`, `RESET enable_bitmapscan`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	want, _, err := h.bm25LegRun(ctx, "الصبر", 10, Filters{}, index, col, fn, true)
	if err != nil || len(want) != 4 {
		t.Fatalf("%s: each row's score worked out: %v %v", index, want, err)
	}
	same := func(what string, got []savedVideo) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s %s: %d rows, want %d", index, what, len(got), len(want))
		}
		for i := range got {
			if got[i].V != want[i].V || math.Abs(got[i].S-want[i].S) > 1e-6*math.Abs(want[i].S) {
				t.Fatalf("%s %s: row %d is %s %v, want %s %v", index, what, i, got[i].V, got[i].S, want[i].V, want[i].S)
			}
		}
	}
	// the index scan's scores, the plan forced onto it
	if _, err := db.Exec(`SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`SET enable_sort = off`); err != nil {
		t.Fatal(err)
	}
	got, err := h.bm25Leg(ctx, "الصبر", 10, Filters{}, index, col, fn)
	if err != nil {
		t.Fatal(err)
	}
	same("with the index scan", got)
	// another search's index scan leaves its last score on the connection
	if _, err := h.bm25Leg(ctx, "المصيبة", 10, Filters{}, index, col, fn); err != nil {
		t.Fatal(err)
	}
	// a plan without the index scan: the four rows read whole and sorted
	for _, q := range []string{`RESET enable_seqscan`, `RESET enable_sort`, `SET enable_indexscan = off`, `SET enable_bitmapscan = off`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	got, err = h.bm25Leg(ctx, "الصبر", 10, Filters{}, index, col, fn)
	if err != nil {
		t.Fatal(err)
	}
	same("without the index scan, after another search's", got)
}
