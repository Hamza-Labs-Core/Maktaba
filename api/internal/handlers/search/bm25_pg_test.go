package search

// Story 49.7 on PostgreSQL 18 with pg_textsearch (the replica job's server; skipped where
// the extension is not): keyword search ranks videos by BM25 over their documents.

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchindex"
)

// bm25DB is pgSearchDB with pg_textsearch made as the bundled server makes it (its own
// schema), or a skip where the server cannot hold it.
func bm25DB(t *testing.T) *sql.DB {
	t.Helper()
	db := pgSearchDB(t)
	var here bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT FROM pg_available_extensions WHERE name = 'pg_textsearch')
		AND current_setting('shared_preload_libraries') ~ '(^|[\s,])pg_textsearch([\s,]|$)'`).Scan(&here); err != nil {
		t.Fatal(err)
	}
	if !here {
		t.Skip("no pg_textsearch on this server (PostgreSQL 16, or not preloaded): the replica job runs this on 18")
	}
	for _, q := range []string{`CREATE SCHEMA IF NOT EXISTS textsearch`,
		`CREATE EXTENSION IF NOT EXISTS pg_textsearch SCHEMA textsearch`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// pgDoc writes the video's search document from its active transcript, as the
// keyword_index stage builds it (story 49.6).
func pgDoc(t *testing.T, db *sql.DB, video string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO video_search_docs (video_id, library_id, title, body, transcript_id, doc_version)
		SELECT v.id, v.library_id, coalesce(v.title, ''), string_agg(s.text, E'\n' ORDER BY s.seq), t.id, 1
		  FROM videos v JOIN transcripts t ON t.video_id = v.id AND t.is_active
		  JOIN transcript_segments s ON s.transcript_id = t.id
		 WHERE v.id = $1 GROUP BY v.id, t.id`, video); err != nil {
		t.Fatal(err)
	}
}

func buildIndexes(t *testing.T, db *sql.DB) {
	t.Helper()
	ran, locked, err := searchindex.BuildOnce(context.Background(), db)
	if err != nil || !locked || len(ran) != 2 {
		t.Fatalf("BuildOnce: %q %v %v", ran, locked, err)
	}
}

func order(vs []TranscriptVideo) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.VideoID
	}
	return out
}

func indexOf(ids []string, id string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}

func TestBM25_Postgres(t *testing.T) {
	db := bm25DB(t)
	va, vb, vc, vd, ve, vf := pgUUID(101), pgUUID(102), pgUUID(103), pgUUID(104), pgUUID(105), pgUUID(106)
	// va: الله often, and the sentence said as a phrase
	tr := pgVideo(t, db, va, libA, "درس الصبر")
	pgTranscript(t, db, pgUUID(201), va, tr, true, append(repeat("قال الله تعالى", 6), "إن الصبر عند المصيبة الأولى", "والله أعلم")...)
	// vb: the sentence's words scattered over its lines, الله once
	tr = pgVideo(t, db, vb, libA, "متفرقات")
	pgTranscript(t, db, pgUUID(202), vb, tr, true, "الصبر جميل", "كنا عند الباب", "المصيبة كبيرة", "سبحان الله")
	// vc: another library, الله more than anyone (a limited user never sees it)
	tr = pgVideo(t, db, vc, libB, "مكتبة أخرى")
	pgTranscript(t, db, pgUUID(203), vc, tr, true, repeat("الله الله الله", 10)...)
	// vd: a rare word, once
	tr = pgVideo(t, db, vd, libA, "نادر")
	pgTranscript(t, db, pgUUID(204), vd, tr, true, "كلمة نادرة هي الاستغفار", "ثم كلام آخر")
	// ve: another form of الصبر only (صبروا: its stem is صبر)
	tr = pgVideo(t, db, ve, libA, "صيغة")
	pgTranscript(t, db, pgUUID(205), ve, tr, true, "الذين صبروا على الأذى")
	for _, v := range []string{va, vb, vc, vd, ve} {
		pgDoc(t, db, v)
	}
	// vf: transcribed, its document not built yet (story 49.6's backfill running)
	tr = pgVideo(t, db, vf, libA, "لم يفهرس")
	pgTranscript(t, db, pgUUID(206), vf, tr, true, "الله أكبر", "الاستغفار بعد الصلاة")
	buildIndexes(t, db)

	h := &Handler{DB: db}
	srv := router(h)
	limited := &principal.Principal{UserID: userA, Libraries: []string{libA}}
	search := func(p *principal.Principal, q, mode string) Response {
		t.Helper()
		return decode[Response](t, call(t, srv, p, http.MethodPost, "/api/search",
			`{"q":"`+q+`","mode":"`+mode+`","view":"list"}`), 200)
	}
	if ready, missing := h.bm25Now(context.Background()); !ready || !missing {
		t.Fatalf("bm25Now = %v, %v: want ready, a document missing", ready, missing)
	}

	// الله: the common word ranks by the documents; the other library stays out
	resp := search(limited, "الله", "fts")
	got := order(resp.TranscriptVideos)
	if indexOf(got, vc) >= 0 {
		t.Fatalf("a limited user found the other library's video: %v", got)
	}
	if indexOf(got, va) < 0 || indexOf(got, vb) < 0 || indexOf(got, va) > indexOf(got, vb) {
		t.Fatalf("الله: %v, want %s (often) before %s (once)", got, va, vb)
	}
	// the video with no document yet is found by its lines
	if indexOf(got, vf) < 0 {
		t.Fatalf("الله: %v lacks %s, which has no document yet", got, vf)
	}
	for _, v := range resp.TranscriptVideos {
		if v.VideoID == va {
			// its matching lines counted for the page, as the line path counts them: the 6
			// lines that say الله ("والله" is one word to maktaba_search)
			if v.Matches != 6 || len(v.Passages) == 0 || !strings.Contains(v.Passages[0].Snippet, "<mark>") {
				t.Fatalf("%s: %d matches, passages %+v", va, v.Matches, v.Passages)
			}
		}
	}
	// the admin sees the other library's video, first: it says الله the most
	if got := order(search(adminB, "الله", "fts").TranscriptVideos); len(got) == 0 || got[0] != vc {
		t.Fatalf("admin الله: %v, want %s first", got, vc)
	}

	// a sentence: the video that says it as a phrase ranks above the one whose words are
	// scattered
	got = order(search(limited, "الصبر عند المصيبة", "fts").TranscriptVideos)
	if indexOf(got, va) < 0 || indexOf(got, vb) < 0 || indexOf(got, va) > indexOf(got, vb) {
		t.Fatalf("the sentence: %v, want %s (the phrase) before %s (scattered)", got, va, vb)
	}

	// a rare word: its one video (and the one without a document, by its lines)
	got = order(search(limited, "الاستغفار", "fts").TranscriptVideos)
	if len(got) != 2 || indexOf(got, vd) < 0 || indexOf(got, vf) < 0 {
		t.Fatalf("the rare word: %v, want %s and %s", got, vd, vf)
	}

	// word forms: الصبر finds the videos that say it, then the one with another form
	got = order(search(limited, "الصبر", "fts").TranscriptVideos)
	if indexOf(got, ve) < 0 || indexOf(got, va) > indexOf(got, ve) || indexOf(got, vb) > indexOf(got, ve) {
		t.Fatalf("الصبر: %v, want %s and %s before %s (another form)", got, va, vb, ve)
	}

	// hybrid, where the meaning search agrees on the obvious case: the order is BM25's
	sem := &stubSemantic{hits: []Hit{{SegmentID: 1, VideoID: vd, Score: 0.9, Snippet: "كلمة نادرة هي الاستغفار"}}}
	h2 := &Handler{DB: db, Semantic: sem}
	resp = decode[Response](t, call(t, router(h2), limited, http.MethodPost, "/api/search",
		`{"q":"الاستغفار","mode":"hybrid","view":"list"}`), 200)
	if got := order(resp.TranscriptVideos); len(got) == 0 || got[0] != vd {
		t.Fatalf("hybrid: %v, want %s first", got, vd)
	}

	// once every video has its document, nothing is read by its lines
	pgDoc(t, db, vf)
	h3 := &Handler{DB: db}
	if ready, missing := h3.bm25Now(context.Background()); !ready || missing {
		t.Fatalf("bm25Now after the backfill = %v, %v", ready, missing)
	}
	got = order(decode[Response](t, call(t, router(h3), limited, http.MethodPost, "/api/search",
		`{"q":"الاستغفار","mode":"fts","view":"list"}`), 200).TranscriptVideos)
	if len(got) != 2 || indexOf(got, vf) < 0 {
		t.Fatalf("the rare word after the backfill: %v", got)
	}
}

// Without the indexes (PostgreSQL 16, or before the API built them) keyword search ranks
// the matching lines: the BM25 path is not taken.
func TestBM25_NotReadyUsesTheLines(t *testing.T) {
	db := pgSearchDB(t)
	va := pgUUID(111)
	tr := pgVideo(t, db, va, libA, "درس")
	pgTranscript(t, db, pgUUID(211), va, tr, true, "الصبر")
	h := &Handler{DB: db}
	if ready, _ := h.bm25Now(context.Background()); ready {
		t.Fatal("bm25Now ready with no index")
	}
	resp := decode[Response](t, call(t, router(h), adminB, http.MethodPost, "/api/search",
		`{"q":"الصبر","mode":"fts","view":"list"}`), 200)
	if len(resp.TranscriptVideos) != 1 || resp.TranscriptVideos[0].Matches != 1 {
		t.Fatalf("the line path: %+v", resp.TranscriptVideos)
	}
}

// A video found through a spelling variant (story 49.4's point: اسلام finds إسلام) or
// another form of the word shows its matching lines and a passage that marks the word as
// it is written, not 0 matches and no passage.
func TestBM25_VariantsHaveMatchesAndPassages(t *testing.T) {
	db := bm25DB(t)
	cases := []struct{ q, said, line string }{
		{"اسلام", "إسلام", "حديث عن الإيمان و إسلام المرء"},
		{"هدي", "هدى", "هذا هدى للمتقين"},
		{"مدرسه", "مدرسة", "ذهب إلى مدرسة القرية، ثم عاد"},
		{"صبروا", "الصبر", "إن الصبر عند المصيبة الأولى"},  // another form of the word
		{"cafe", "café", "a talk at the café"},             // the exact configuration's unaccent
		{"Qur'an", "Qur'an", "reading the Qur'an at dawn"}, // one lexeme to the parser
		{"comes", "coming", "he keeps coming back"},        // an English stem Snowball ends with e
	}
	for i, c := range cases {
		v := pgUUID(121 + i)
		tr := pgVideo(t, db, v, libA, "درس "+strconv.Itoa(i))
		pgTranscript(t, db, pgUUID(221+i), v, tr, true, "مقدمة", c.line, "خاتمة")
		pgDoc(t, db, v)
	}
	buildIndexes(t, db)
	srv := router(&Handler{DB: db})
	for i, c := range cases {
		v := pgUUID(121 + i)
		resp := decode[Response](t, call(t, srv, adminB, http.MethodPost, "/api/search",
			`{"q":"`+c.q+`","mode":"fts","view":"list"}`), 200)
		var got *TranscriptVideo
		for j := range resp.TranscriptVideos {
			if resp.TranscriptVideos[j].VideoID == v {
				got = &resp.TranscriptVideos[j]
			}
		}
		if got == nil {
			t.Fatalf("%s: the video saying %s is not found: %+v", c.q, c.said, order(resp.TranscriptVideos))
		}
		if got.Matches < 1 || len(got.Passages) == 0 || !strings.Contains(got.Passages[0].Snippet, "<mark>"+c.said+"</mark>") {
			t.Fatalf("%s: %d matches, passages %+v: want ≥ 1 and %s marked", c.q, got.Matches, got.Passages, c.said)
		}
		// the video's own passages page reads them the same way
		page := decode[passagesPage](t, call(t, srv, adminB, http.MethodGet,
			"/api/search/passages?sid="+resp.SearchID+"&video_id="+v, ""), 200)
		if page.Total != 1 || !strings.Contains(page.Items[0].Snippet, "<mark>"+c.said+"</mark>") {
			t.Fatalf("%s: passages page %+v", c.q, page)
		}
	}
}

// A common word inside many others (الله in والله, بالله) sends many lines to Postgres's
// stemmer: past maybeCap the rest are not asked, and the lines matched as typed still count
// and are marked.
func TestBM25_CommonWordWithinOthers(t *testing.T) {
	db := bm25DB(t)
	v := pgUUID(141)
	tr := pgVideo(t, db, v, libA, "درس")
	lines := []string{"قال الله تعالى", "الله أكبر"}
	for i := 0; i < 20; i++ {
		lines = append(lines, "والله "+strconv.Itoa(i), "بالله "+strconv.Itoa(i))
	}
	pgTranscript(t, db, pgUUID(241), v, tr, true, lines...)
	pgDoc(t, db, v)
	buildIndexes(t, db)
	old := maybeCap
	maybeCap = 5
	t.Cleanup(func() { maybeCap = old })
	h := &Handler{DB: db}
	hits, counts, marks, ok, err := h.normalisedLines(context.Background(), "الله", []string{v}, 10)
	if err != nil || !ok {
		t.Fatalf("normalisedLines: %v, ok %v", err, ok)
	}
	if counts[v] != 2 || len(hits[v]) != 2 {
		t.Fatalf("counts %v, %d passages: want the 2 lines saying الله", counts, len(hits[v]))
	}
	for _, hit := range hits[v] {
		if len(marks[hit.SegmentID]) == 0 || marks[hit.SegmentID][0] != "الله" {
			t.Fatalf("marks of %q: %q", hit.Snippet, marks[hit.SegmentID])
		}
	}
}
