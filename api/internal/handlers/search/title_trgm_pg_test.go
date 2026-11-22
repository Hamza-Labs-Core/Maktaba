package search

// Story 49.15 on Postgres (the replica job's servers are --locale=C, as the bundled
// server's database is): title matches rank by similarity for an Arabic query as for a
// Latin one.

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestBM25_ArabicTitlesRankBySimilarity(t *testing.T) {
	db := pgSearchDB(t)
	var ctype string
	if err := db.QueryRow(`SELECT datctype FROM pg_database WHERE datname = current_database()`).Scan(&ctype); err != nil {
		t.Fatal(err)
	}
	if ctype != "C" {
		t.Skipf("the database's ctype is %q, not C (the bundled server's): pg_trgm reads Arabic here", ctype)
	}
	// each query's closest title is added first: newest first would rank it last
	for i, v := range []struct{ id, title string }{
		{pgUUID(501), "درس الصبر"},
		{pgUUID(502), "محاضرة طويلة في الصبر والشكر والرضا بالقضاء"},
		{pgUUID(503), "Patience"},
		{pgUUID(504), "A long lecture on patience, gratitude and contentment"},
	} {
		pgVideo(t, db, v.id, libA, v.title)
		if _, err := db.Exec(`UPDATE videos SET created_at = $2 WHERE id = $1`, v.id, time.Now().Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{DB: db}
	for q, want := range map[string][]string{
		"الصبر":    {pgUUID(501), pgUUID(502)},
		"patience": {pgUUID(503), pgUUID(504)},
	} {
		ts, err := h.runTitles(context.Background(), q, 10, Filters{})
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(ts))
		for i, x := range ts {
			got[i] = x.V
		}
		if !slices.Equal(got, want) || ts[0].S <= ts[1].S {
			t.Errorf("%s: %v (scores %+v), want %v", q, got, ts, want)
		}
	}
}
