package search

// Story 49.8 on PostgreSQL 18 (the replica job): suggestions, did-you-mean, facets and sort
// with keyword search ranked by BM25, and each of them kept to the caller's libraries.

import (
	"database/sql"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchterms"
)

// speakerKeys is the pipeline's search/terms.py speaker_keys: each name by each word
// start of it.
func speakerKeys(names string) map[[2]string]bool {
	out := map[[2]string]bool{}
	for _, line := range strings.Split(names, "\n") {
		parts := strings.Fields(line)
		name := strings.Join(parts, " ")
		for i := range parts {
			if k := searchterms.Key(strings.Join(parts[i:], " ")); k != "" {
				out[[2]string{k, name}] = true
			}
		}
	}
	return out
}

// pgTerms fills search_terms and search_speakers from every document, as the
// keyword_index stage counts them.
func pgTerms(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT library_id::text, title, speakers, body FROM video_search_docs`)
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ lib, term string }
	df, shown := map[key]int{}, map[key]string{}
	type sk struct{ lib, key, name string }
	sp := map[sk]int{}
	for rows.Next() {
		var lib, title, speakers, body string
		if err := rows.Scan(&lib, &title, &speakers, &body); err != nil {
			t.Fatal(err)
		}
		for k := range speakerKeys(speakers) {
			sp[sk{lib, k[0], k[1]}]++
		}
		for k, s := range searchterms.DocTerms(title, speakers, body) {
			df[key{lib, k}]++
			if _, ok := shown[key{lib, k}]; !ok {
				shown[key{lib, k}] = s
			}
		}
	}
	_ = rows.Close()
	for k, n := range df {
		if _, err := db.Exec(`INSERT INTO search_terms (library_id, term, shown, df) VALUES ($1, $2, $3, $4)`, k.lib, k.term, shown[k], n); err != nil {
			t.Fatal(err)
		}
	}
	for k, n := range sp {
		if _, err := db.Exec(`INSERT INTO search_speakers (library_id, key, name, df) VALUES ($1, $2, $3, $4)`, k.lib, k.key, k.name, n); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBM25_SearchHelp(t *testing.T) {
	db := bm25DB(t)
	va, vb, vc := pgUUID(301), pgUUID(302), pgUUID(303)
	// va: المدرسة three times, 10 minutes, published in 2023, the speaker named
	tr := pgVideo(t, db, va, libA, "درس المدرسة")
	pgTranscript(t, db, pgUUID(401), va, tr, true, "ذهبت إلى المدرسة", "المدرسة جميلة", "في المدرسة الصبر")
	// vb: المدرسة once, over an hour, published in 2024
	tr = pgVideo(t, db, vb, libA, "محاضرة طويلة")
	pgTranscript(t, db, pgUUID(402), vb, tr, true, "المدرسة الكبيرة", "كلام آخر")
	// vc: the other library, with a word of its own (الخفية)
	tr = pgVideo(t, db, vc, libB, "مكتبة أخرى")
	pgTranscript(t, db, pgUUID(403), vc, tr, true, "المدرسة الخفية", "الخفية مرة أخرى")
	for _, q := range []string{
		`UPDATE videos SET published_at = '2023-05-01T00:00:00Z' WHERE id = '` + va + `'`,
		`UPDATE videos SET published_at = '2024-02-01T00:00:00Z', duration_sec = 4000 WHERE id = '` + vb + `'`,
		`UPDATE videos SET published_at = '2025-03-01T00:00:00Z' WHERE id = '` + vc + `'`,
		`INSERT INTO speakers (video_id, name) VALUES ('` + va + `', 'الشيخ أحمد')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []string{va, vb, vc} {
		pgDoc(t, db, v)
	}
	if _, err := db.Exec(`UPDATE video_search_docs SET speakers = 'الشيخ أحمد' WHERE video_id = $1`, va); err != nil {
		t.Fatal(err)
	}
	pgTerms(t, db)
	buildIndexes(t, db)

	srv := router(&Handler{DB: db})
	limited := &principal.Principal{UserID: userA, Libraries: []string{libA}}
	search := func(p *principal.Principal, body string) Response {
		t.Helper()
		return decode[Response](t, call(t, srv, p, http.MethodPost, "/api/search", body), 200)
	}

	// facets count the videos found, and each bucket's filter narrows to it
	resp := search(adminB, `{"q":"المدرسة","mode":"fts"}`)
	f := resp.Facets
	if f == nil || f.Videos != 3 {
		t.Fatalf("facets %+v", f)
	}
	sum := 0
	for _, b := range f.Library {
		sum += b.Count
	}
	if sum != f.Videos || len(f.Library) != 2 || f.Library[0].Value != libA || f.Library[0].Count != 2 || f.Library[0].Label != "Lessons" {
		t.Fatalf("library facet %+v", f.Library)
	}
	years := map[string]int{}
	for _, b := range f.Year {
		years[b.Value] = b.Count
	}
	if len(f.Year) != 3 || f.Year[0].Value != "2025" || years["2023"] != 1 || years["2024"] != 1 {
		t.Fatalf("year facet %+v", f.Year)
	}
	if len(f.Duration) != 2 || f.Duration[0].Value != "5m_20m" || f.Duration[0].Count != 2 || f.Duration[1].Value != "over_60m" {
		t.Fatalf("duration facet %+v", f.Duration)
	}
	if len(f.Speaker) != 1 || f.Speaker[0].Value != "الشيخ أحمد" || f.Speaker[0].Count != 1 {
		t.Fatalf("speaker facet %+v", f.Speaker)
	}
	narrow := func(filter string) []string {
		t.Helper()
		got := order(search(adminB, `{"q":"المدرسة","mode":"fts","filters":`+filter+`}`).TranscriptVideos)
		slices.Sort(got)
		return got
	}
	if got := narrow(`{"date":{"gte":"2023-01-01","lt":"2024-01-01"}}`); !slices.Equal(got, []string{va}) {
		t.Fatalf("the 2023 bucket: %v", got)
	}
	if got := narrow(`{"duration_sec":{"gte":3600}}`); !slices.Equal(got, []string{vb}) {
		t.Fatalf("the over-an-hour bucket: %v", got)
	}
	if got := narrow(`{"speaker":["الشيخ أحمد"]}`); !slices.Equal(got, []string{va}) {
		t.Fatalf("the speaker bucket: %v", got)
	}
	// a limited user's facets are its libraries' alone
	if lf := search(limited, `{"q":"المدرسة","mode":"fts"}`).Facets; lf == nil || lf.Videos != 2 || len(lf.Library) != 1 || lf.Library[0].Value != libA {
		t.Fatalf("limited facets %+v", lf)
	}

	// sort: newest first; most matches first; the kept search's pages follow
	newest := search(limited, `{"q":"المدرسة","mode":"fts","sort":"newest"}`)
	if got := order(newest.TranscriptVideos); newest.Sort != SortNewest || !slices.Equal(got, []string{vb, va}) {
		t.Fatalf("newest: %s %v", newest.Sort, got)
	}
	page := decode[resultsPage](t, call(t, srv, limited, http.MethodGet, "/api/search/results/"+newest.SearchID+"?list=transcripts", ""), 200)
	if len(page.Items) != 2 || page.Items[0].VideoID != vb {
		t.Fatalf("the kept search's page: %+v", page)
	}
	if got := order(search(limited, `{"q":"المدرسة","mode":"fts","sort":"matches"}`).TranscriptVideos); !slices.Equal(got, []string{va, vb}) {
		t.Fatalf("matches: %v", got)
	}

	// did-you-mean: a misspelt Arabic word finds few videos and gets the word as written
	if got := search(limited, `{"q":"المدرصة","mode":"fts"}`).DidYouMean; got != "المدرسة" {
		t.Fatalf("did you mean %q, want المدرسة", got)
	}
	if got := search(limited, `{"q":"المدرسة","mode":"fts"}`).DidYouMean; got != "" {
		t.Fatalf("a word found everywhere got did you mean %q", got)
	}
	// ... from the caller's libraries only: الخفية is the other library's word
	if got := search(limited, `{"q":"الخفيا","mode":"fts"}`).DidYouMean; strings.Contains(got, "الخفية") {
		t.Fatalf("a limited user was offered the other library's word: %q", got)
	}
	if got := search(adminB, `{"q":"الخفيا","mode":"fts"}`).DidYouMean; got != "الخفية" {
		t.Fatalf("the admin: did you mean %q, want الخفية", got)
	}

	// suggestions: the query's last word completed, titles, speakers; the caller's
	// libraries only
	suggest := func(p *principal.Principal, q string) map[string]string {
		t.Helper()
		got := decode[struct {
			Suggestions []string     `json:"suggestions"`
			Items       []Suggestion `json:"items"`
		}](t, call(t, srv, p, http.MethodGet, "/api/search/suggest?q="+url.QueryEscape(q), ""), 200)
		out := map[string]string{}
		for i, s := range got.Items {
			out[s.Text] = s.Kind
			if got.Suggestions[i] != s.Text {
				t.Fatalf("suggestions %v and items %v differ", got.Suggestions, got.Items)
			}
		}
		return out
	}
	if got := suggest(limited, "المد"); got["المدرسة"] != "term" {
		t.Fatalf("suggest المد: %v", got)
	}
	if got := suggest(limited, "في المد"); got["في المدرسة"] != "term" {
		t.Fatalf("suggest في المد: %v", got)
	}
	if got := suggest(limited, "الشي"); got["الشيخ أحمد"] != "speaker" {
		t.Fatalf("suggest الشي: %v", got)
	}
	if got := suggest(limited, "محاض"); got["محاضرة طويلة"] != "title" {
		t.Fatalf("suggest محاض: %v", got)
	}
	if got := suggest(limited, "الخف"); len(got) != 0 {
		t.Fatalf("a limited user got the other library's word: %v", got)
	}
	if got := suggest(adminB, "الخف"); got["الخفية"] != "term" {
		t.Fatalf("the admin: suggest الخف: %v", got)
	}
	if got := suggest(limited, "مكتبة"); len(got) != 0 {
		t.Fatalf("a limited user got the other library's title: %v", got)
	}
}
