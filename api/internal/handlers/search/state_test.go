package search

// Story 49.10 follow-up: a search keeps how it was asked (slot 0236), so a Recent search
// and a page opened by its search_id come back with their order, filters and facets.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// stateCase runs on a database with libraries A ("Lessons" on Postgres, "L" on SQLite)
// and B, a 60 s video in A and a 600 s one in B whose titles and transcripts say "salah".
func stateCase(t *testing.T, db *sql.DB) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	h := router(&Handler{DB: db, NowFunc: c.now})
	post := func(p *principal.Principal, body string) Response {
		c.t = c.t.Add(time.Second)
		return decode[Response](t, call(t, h, p, http.MethodPost, "/api/search", body), 200)
	}
	rawRecent := func(p *principal.Principal) []map[string]json.RawMessage {
		var out struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		rec := call(t, h, p, http.MethodGet, "/api/search/recent", "")
		if rec.Code != 200 {
			t.Fatalf("recent: %d %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Items
	}

	// an order and filters asked for are kept with the search and listed with it
	asked := post(admin, `{"q":"salah","mode":"fts","sort":"newest","filters":{"library_id":["`+libA+`"],"duration_sec":{"gte":0,"lt":300}}}`)
	if asked.SearchID == "" || asked.Sort != SortNewest {
		t.Fatalf("search: %+v", asked)
	}
	recent := decode[recentList](t, call(t, h, admin, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(recent) != 1 || recent[0].Sort != SortNewest || recent[0].Filters == nil {
		t.Fatalf("recent %+v", recent)
	}
	f := recent[0].Filters
	if !slices.Equal(f.LibraryID, []string{libA}) || f.DurationSec == nil || f.DurationSec.Lt == nil || *f.DurationSec.Lt != 300 ||
		f.DurationSec.Gte == nil || *f.DurationSec.Gte != 0 || f.Date != nil || len(f.Language) != 0 {
		t.Fatalf("recent filters %+v", f)
	}

	// a page by search_id says its order; its facets and did-you-mean only when asked
	type pageHelp struct {
		Sort   string  `json:"sort"`
		Total  int     `json:"total"`
		Facets *Facets `json:"facets"`
	}
	page := decode[pageHelp](t, call(t, h, admin, http.MethodGet, "/api/search/results/"+asked.SearchID+"?list=titles", ""), 200)
	if page.Sort != SortNewest || page.Facets != nil || page.Total != 1 {
		t.Fatalf("page %+v", page)
	}
	page = decode[pageHelp](t, call(t, h, admin, http.MethodGet, "/api/search/results/"+asked.SearchID+"?list=titles&facets=1", ""), 200)
	if page.Facets == nil || page.Facets.Videos != 1 || len(page.Facets.Library) != 1 ||
		page.Facets.Library[0].Value != libA || page.Facets.Library[0].Label == "" || page.Facets.Library[0].Count != 1 {
		t.Fatalf("page facets %+v", page.Facets)
	}
	if asked.Facets == nil || !slices.EqualFunc(asked.Facets.Library, page.Facets.Library, func(a, b Bucket) bool {
		return a.Value == b.Value && a.Label == b.Label && a.Count == b.Count
	}) {
		t.Fatalf("facets by id %+v, first answer %+v", page.Facets, asked.Facets)
	}

	// relevance with no filters: the order is said, the filters are an empty object
	plain := post(admin, `{"q":"salah","mode":"hybrid"}`)
	items := rawRecent(admin)
	if len(items) != 2 || string(items[0]["search_id"]) != `"`+plain.SearchID+`"` ||
		string(items[0]["sort"]) != `"relevance"` || string(items[0]["filters"]) != `{}` {
		t.Fatalf("plain recent %v", items[0])
	}

	// a restricted account: the libraries put in for it are not a choice, so its recent
	// search has no library filter; the one it chose is kept as asked
	restricted := &principal.Principal{UserID: userB, Libraries: []string{libA, libB}}
	scoped := post(restricted, `{"q":"salah","mode":"fts"}`)
	var stored, keptAsked string
	if err := db.QueryRow(`SELECT filters, asked_filters FROM search_runs WHERE id = $1`, scoped.SearchID).Scan(&stored, &keptAsked); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, libA) || keptAsked != "{}" {
		t.Fatalf("restricted: filters %s, asked %s", stored, keptAsked)
	}
	post(restricted, `{"q":"salah","mode":"fts","filters":{"library_id":["`+libB+`"]}}`)
	other := decode[recentList](t, call(t, h, restricted, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(other) != 2 || other[0].Filters == nil || !slices.Equal(other[0].Filters.LibraryID, []string{libB}) ||
		other[1].Filters == nil || len(other[1].Filters.LibraryID) != 0 {
		t.Fatalf("restricted recent %+v", other)
	}

	// a search kept before slot 0236 (and an order this code does not know): neither is
	// listed, and its page still opens
	if _, err := db.Exec(`UPDATE search_runs SET sort_order = NULL, asked_filters = NULL WHERE id = $1`, asked.SearchID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE search_runs SET sort_order = 'oldest', asked_filters = 'not json' WHERE id = $1`, plain.SearchID); err != nil {
		t.Fatal(err)
	}
	for _, it := range rawRecent(admin) {
		if _, ok := it["sort"]; ok {
			t.Fatalf("an old search lists a sort: %v", it)
		}
		if _, ok := it["filters"]; ok {
			t.Fatalf("an old search lists filters: %v", it)
		}
	}
	page = decode[pageHelp](t, call(t, h, admin, http.MethodGet, "/api/search/results/"+asked.SearchID+"?list=titles&facets=1", ""), 200)
	if page.Sort != "" || page.Total != 1 || page.Facets == nil {
		t.Fatalf("old page %+v", page)
	}

	// a saved search keeps its order and filters in its query, given back as saved
	query := `{"q":"salah","mode":"fts","sort":"matches","filters":{"language":["ar"],"date":{"gte":"2024-01-01T00:00:00.000Z","lt":"2025-01-01T00:00:00.000Z"}}}`
	if rec := call(t, h, admin, http.MethodPost, "/api/search/save", `{"name":"salah · Most matches","query":`+query+`}`); rec.Code != 201 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, h, admin, http.MethodPost, "/api/search/save", `{"name":"salah","query":{"q":"salah","mode":"hybrid"}}`); rec.Code != 201 {
		t.Fatalf("save plain: %d %s", rec.Code, rec.Body.String())
	}
	saved := decode[struct {
		Items []SavedSearch `json:"items"`
	}](t, call(t, h, admin, http.MethodGet, "/api/search/saved", ""), 200).Items
	if len(saved) != 2 {
		t.Fatalf("saved %+v", saved)
	}
	byName := map[string]Request{}
	for _, s := range saved {
		if s.CreatedAt.IsZero() {
			t.Fatalf("saved %q has no created_at", s.Name)
		}
		var r Request
		if err := json.Unmarshal(s.Query, &r); err != nil {
			t.Fatalf("saved query %s: %v", s.Query, err)
		}
		byName[s.Name] = r
	}
	withState := byName["salah · Most matches"]
	if withState.Sort != SortMatches || !slices.Equal(withState.Filters.Language, []string{"ar"}) ||
		withState.Filters.Date == nil || withState.Filters.Date.Lt != "2025-01-01T00:00:00.000Z" {
		t.Fatalf("saved with its order and filters: %+v", withState)
	}
	if old := byName["salah"]; old.Sort != "" || old.Filters.Date != nil || old.Mode != "hybrid" {
		t.Fatalf("saved without them: %+v", old)
	}
}

func TestSearchState_SQLite(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "L")
	f.library(libB, "M")
	f.video("va", libA, "salah short", 60)
	f.transcript("ta", "va", true, "salah")
	f.video("vb", libB, "salah long", 600)
	f.transcript("tb", "vb", true, "salah")
	if _, err := db.Exec(`CREATE TABLE saved_searches (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, name TEXT NOT NULL,
		query TEXT NOT NULL, is_smart INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')), UNIQUE (user_id, name))`); err != nil {
		t.Fatal(err)
	}
	stateCase(t, db)
}

func TestSearchState_Postgres(t *testing.T) {
	db := pgSearchDB(t)
	va, vb := pgUUID(1), pgUUID(2)
	tr := pgVideo(t, db, va, libA, "salah short")
	pgTranscript(t, db, pgUUID(11), va, tr, true, "salah")
	tr = pgVideo(t, db, vb, libB, "salah long")
	pgTranscript(t, db, pgUUID(12), vb, tr, true, "salah")
	if _, err := db.Exec(`UPDATE videos SET duration_sec = 60 WHERE id = $1`, va); err != nil {
		t.Fatal(err)
	}
	stateCase(t, db)
}
