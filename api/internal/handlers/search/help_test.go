package search

// Story 49.8 on SQLite: facets, sort, did-you-mean (similarity worked out in Go) and
// suggestions, each kept to the caller's libraries. The Postgres side is
// bm25_help_pg_test.go.

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchterms"
)

// terms fills search_terms from each video's title and active transcript, as the
// keyword_index stage counts them.
func (f *fixture) terms() {
	f.t.Helper()
	rows, err := f.db.Query(`SELECT v.library_id, COALESCE(v.title, ''), COALESCE(group_concat(s.text, char(10)), '')
		FROM videos v LEFT JOIN transcripts t ON t.video_id = v.id AND t.is_active
		LEFT JOIN transcript_segments s ON s.transcript_id = t.id GROUP BY v.id`)
	if err != nil {
		f.t.Fatal(err)
	}
	type key struct{ lib, term string }
	df, shown := map[key]int{}, map[key]string{}
	for rows.Next() {
		var lib, title, body string
		if err := rows.Scan(&lib, &title, &body); err != nil {
			f.t.Fatal(err)
		}
		for k, s := range searchterms.DocTerms(title, body) {
			df[key{lib, k}]++
			if _, ok := shown[key{lib, k}]; !ok {
				shown[key{lib, k}] = s
			}
		}
	}
	_ = rows.Close()
	for k, n := range df {
		f.exec(`INSERT INTO search_terms (library_id, term, shown, df) VALUES ($1, $2, $3, $4)`, k.lib, k.term, shown[k], n)
	}
	// the named speakers by each word start of the name, as the stage counts them
	f.exec(`INSERT INTO search_speakers (library_id, key, name, df)
		SELECT v.library_id, lower(s.name), s.name, count(*) FROM speakers s JOIN videos v ON v.id = s.video_id
		 WHERE s.unknown_index IS NULL GROUP BY v.library_id, s.name`)
	f.exec(`INSERT INTO search_speakers (library_id, key, name, df)
		SELECT v.library_id, substr(s.name, instr(s.name, ' ') + 1), s.name, count(*) FROM speakers s JOIN videos v ON v.id = s.video_id
		 WHERE s.unknown_index IS NULL AND instr(s.name, ' ') > 0 GROUP BY v.library_id, s.name`)
}

func TestSearchHelp_SQLite(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "Lessons")
	f.library(libB, "Other")
	va, vb, vc := pgUUID(1), pgUUID(2), pgUUID(3)
	f.video(va, libA, "درس المدرسة", 600)
	f.transcript("ta", va, true, "ذهبت إلى المدرسة", "المدرسة جميلة", "في المدرسة")
	f.video(vb, libA, "محاضرة طويلة", 4000)
	f.transcript("tb", vb, true, "المدرسة الكبيرة", "كلام آخر")
	f.video(vc, libB, "مكتبة أخرى", 60)
	f.transcript("tc", vc, true, "المدرسة الخفية", "الخفية مرة أخرى")
	f.exec(`UPDATE videos SET published_at = '2023-05-01T00:00:00.000Z', created_at = '2026-01-01T00:00:00.000Z' WHERE id = $1`, va)
	f.exec(`UPDATE videos SET published_at = '2024-02-01T00:00:00.000Z', created_at = '2026-01-01T00:00:00.000Z' WHERE id = $1`, vb)
	f.exec(`UPDATE videos SET created_at = '2025-03-01T00:00:00.000Z' WHERE id = $1`, vc)
	f.exec(`INSERT INTO speakers (id, video_id, name) VALUES ('s1', $1, 'الشيخ أحمد')`, va)
	f.terms()

	srv := router(&Handler{DB: db})
	limitedUser := &principal.Principal{UserID: userA, Libraries: []string{libA}}
	search := func(p *principal.Principal, body string) Response {
		t.Helper()
		return decode[Response](t, call(t, srv, p, http.MethodPost, "/api/search", body), 200)
	}

	resp := search(adminB, `{"q":"المدرسة","mode":"fts"}`)
	fc := resp.Facets
	if fc == nil || fc.Videos != 3 || len(fc.Library) != 2 || fc.Library[0].Value != libA || fc.Library[0].Count != 2 {
		t.Fatalf("facets %+v", fc)
	}
	if len(fc.Year) != 3 || fc.Year[0].Value != "2025" || len(fc.Duration) != 3 || fc.Duration[0].Value != "under_5m" {
		t.Fatalf("year %+v, duration %+v", fc.Year, fc.Duration)
	}
	if len(fc.Speaker) != 1 || fc.Speaker[0].Value != "الشيخ أحمد" {
		t.Fatalf("speaker %+v", fc.Speaker)
	}
	narrow := func(filter string) []string {
		t.Helper()
		got := order(search(adminB, `{"q":"المدرسة","mode":"fts","filters":`+filter+`}`).TranscriptVideos)
		slices.Sort(got)
		return got
	}
	y := fc.Year[len(fc.Year)-1] // 2023
	if y.Value != "2023" || y.Filter == nil || y.Filter.Date == nil {
		t.Fatalf("the 2023 bucket %+v", y)
	}
	if got := narrow(`{"date":{"gte":"` + y.Filter.Date.Gte + `","lt":"` + y.Filter.Date.Lt + `"}}`); !slices.Equal(got, []string{va}) {
		t.Fatalf("the 2023 bucket: %v", got)
	}
	if got := narrow(`{"duration_sec":{"gte":300,"lt":1200}}`); !slices.Equal(got, []string{va}) {
		t.Fatalf("the 5-20 minutes bucket: %v", got)
	}
	if got := narrow(`{"speaker":["الشيخ أحمد"]}`); !slices.Equal(got, []string{va}) {
		t.Fatalf("the speaker bucket: %v", got)
	}
	if lf := search(limitedUser, `{"q":"المدرسة","mode":"fts"}`).Facets; lf == nil || lf.Videos != 2 || len(lf.Library) != 1 {
		t.Fatalf("limited facets %+v", lf)
	}
	if none := search(limitedUser, `{"q":"zzzz-nothing","mode":"fts"}`); none.Facets != nil || none.Sort != SortRelevance {
		t.Fatalf("a search with no video: facets %+v, sort %q", none.Facets, none.Sort)
	}

	if got := order(search(limitedUser, `{"q":"المدرسة","mode":"fts","sort":"newest"}`).TranscriptVideos); !slices.Equal(got, []string{vb, va}) {
		t.Fatalf("newest: %v", got)
	}
	if got := order(search(limitedUser, `{"q":"المدرسة","mode":"fts","sort":"matches"}`).TranscriptVideos); !slices.Equal(got, []string{va, vb}) {
		t.Fatalf("matches: %v", got)
	}

	if got := search(limitedUser, `{"q":"المدرصة","mode":"fts"}`).DidYouMean; got != "المدرسة" {
		t.Fatalf("did you mean %q, want المدرسة", got)
	}
	if got := search(limitedUser, `{"q":"الخفيا","mode":"fts"}`).DidYouMean; got != "" {
		t.Fatalf("a limited user was offered %q (the other library's word)", got)
	}
	if got := search(adminB, `{"q":"الخفيا","mode":"fts"}`).DidYouMean; got != "الخفية" {
		t.Fatalf("the admin: did you mean %q, want الخفية", got)
	}

	suggest := func(p *principal.Principal, q string) map[string]string {
		t.Helper()
		got := decode[struct {
			Items []Suggestion `json:"items"`
		}](t, call(t, srv, p, http.MethodGet, "/api/search/suggest?q="+url.QueryEscape(q), ""), 200)
		out := map[string]string{}
		for _, s := range got.Items {
			out[s.Text] = s.Kind
		}
		return out
	}
	if got := suggest(limitedUser, "في المد"); got["في المدرسة"] != "term" {
		t.Fatalf("suggest في المد: %v", got)
	}
	if got := suggest(limitedUser, "الشي"); got["الشيخ أحمد"] != "speaker" {
		t.Fatalf("suggest الشي: %v", got)
	}
	if got := suggest(limitedUser, "محاض"); got["محاضرة طويلة"] != "title" {
		t.Fatalf("suggest محاض: %v", got)
	}
	if got := suggest(limitedUser, "الخف"); len(got) != 0 {
		t.Fatalf("a limited user got the other library's word: %v", got)
	}
	if got := suggest(adminB, "الخف"); got["الخفية"] != "term" {
		t.Fatalf("the admin: suggest الخف: %v", got)
	}
	if got := suggest(&principal.Principal{UserID: userA}, "المد"); len(got) != 0 {
		t.Fatalf("an account with no library got %v", got)
	}

	// a word in two libraries shows the spelling of the library that holds it most
	// (ة sorts before ه: the rarer spelling is the smaller, so the smallest would be wrong)
	f.exec(`INSERT INTO search_terms (library_id, term, shown, df) VALUES ($1, 'مكتوبه', 'مكتوبة', 1), ($2, 'مكتوبه', 'مكتوبه', 5)`, libA, libB)
	if got := suggest(adminB, "مكتو"); got["مكتوبه"] != "term" {
		t.Fatalf("suggest مكتو across libraries: %v", got)
	}
	// a long query is cut on a letter, not inside one
	long := strings.Repeat("ب", 250)
	if rec := call(t, srv, adminB, http.MethodGet, "/api/search/suggest?q="+url.QueryEscape(long), ""); rec.Code != http.StatusOK {
		t.Fatalf("a 250-letter query: %d %s", rec.Code, rec.Body.String())
	}
}
