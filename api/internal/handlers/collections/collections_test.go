package collections

// Epic 45 Phase 3 (story 45.17): the routes the web's Collections use, on
// SQLite: a library's list with counts and posters, curation (admins only),
// item cards, the bulk append and saved-filter smart collections.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
)

const (
	libA   = "00000000-0000-4000-8000-00000000000a"
	libB   = "00000000-0000-4000-8000-00000000000b"
	viewer = "00000000-0000-4000-8000-0000000000e1"
	adm    = "00000000-0000-4000-8000-0000000000e2"
)

func vid(n int) string {
	return "00000000-0000-4000-8000-0000000001" + string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func asViewer() *principal.Principal {
	return &principal.Principal{UserID: viewer, Libraries: []string{libA}}
}

func asAdmin() *principal.Principal {
	return &principal.Principal{UserID: adm, IsAdmin: true, AccessAllLibraries: true}
}

type env struct {
	t  *testing.T
	db *sql.DB
	r  chi.Router
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT)`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, path TEXT, filename TEXT, title TEXT,
			description TEXT, state TEXT, detected_language TEXT, duration_sec REAL, size_bytes INTEGER,
			content_hash TEXT, metadata BLOB, created_at DATETIME, updated_at DATETIME, mtime DATETIME, deleted_at TEXT)`,
		`CREATE TABLE collections (id TEXT PRIMARY KEY, library_id TEXT NOT NULL, name TEXT NOT NULL, description TEXT,
			is_smart INTEGER NOT NULL DEFAULT 0, smart_query TEXT, created_by TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			origin TEXT NOT NULL DEFAULT 'user', auto_rule TEXT, dismissed_at TEXT, UNIQUE (library_id, name))`,
		`CREATE TABLE collection_items (collection_id TEXT NOT NULL, video_id TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0, added_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (collection_id, video_id))`,
		`CREATE TABLE playback_state (user_id TEXT, video_id TEXT, position_sec REAL, completed BOOLEAN, updated_at TEXT)`,
		`CREATE TABLE user_favorites (user_id TEXT, video_id TEXT, created_at TEXT)`,
		`INSERT INTO libraries VALUES ('` + libA + `','A'), ('` + libB + `','B')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// 1..4 in A (4 missing), 5 in B; 1 and 2 are long, 3 short
	for _, v := range []struct {
		n     int
		lib   string
		state string
		dur   float64
	}{{1, libA, "ready", 4000}, {2, libA, "ready", 3700}, {3, libA, "ready", 60}, {4, libA, "missing", 4000}, {5, libB, "ready", 4000}} {
		if _, err := db.Exec(`INSERT INTO videos (id, library_id, path, filename, title, state, duration_sec, size_bytes, content_hash, created_at, updated_at)
			VALUES ($1,$2,'p',$3,$4,$5,$6,0,'h','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z')`,
			vid(v.n), v.lib, "f"+vid(v.n), "Lesson "+string(rune('0'+v.n)), v.state, v.dur); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	h := &Handler{DB: db, ListVideos: (&videos.Handler{DB: db}).List}
	h.Mount(r)
	h.MountSmart(r)
	return &env{t: t, db: db, r: r}
}

func (e *env) do(p *principal.Principal, method, path string, body any, out any) int {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	if rec.Code >= 500 {
		e.t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
	}
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, rec.Body.String())
		}
	}
	return rec.Code
}

func (e *env) create(body map[string]any) Collection {
	e.t.Helper()
	var c Collection
	if code := e.do(asAdmin(), "POST", "/api/collections", body, &c); code != http.StatusCreated {
		e.t.Fatalf("create %v: %d", body, code)
	}
	return c
}

type listBody struct {
	Items []Collection `json:"items"`
}

type itemsBody struct {
	Items []CollectionItem `json:"items"`
}

func itemIDs(items []CollectionItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.VideoID)
	}
	return out
}

func TestCuration(t *testing.T) {
	e := newEnv(t)
	c := e.create(map[string]any{"library_id": libA, "name": "Ramadan"})
	e.create(map[string]any{"library_id": libB, "name": "B only"})

	// a bulk append, in order, a repeat counted once; a missing video is kept
	// out of what the collection shows
	var added struct{ Added int }
	if code := e.do(asAdmin(), "POST", "/api/collections/"+c.ID+"/items",
		map[string]any{"video_ids": []string{vid(2), vid(1), vid(2), vid(4)}}, &added); code != 200 || added.Added != 3 {
		t.Fatalf("append: %d %+v", code, added)
	}
	if code := e.do(asAdmin(), "POST", "/api/collections/"+c.ID+"/items",
		map[string]any{"video_ids": []string{vid(1), vid(3)}}, &added); code != 200 || added.Added != 1 {
		t.Fatalf("append again: %d %+v (1 is in it already)", code, added)
	}
	var items itemsBody
	e.do(asViewer(), "GET", "/api/collections/"+c.ID+"/items", nil, &items)
	if got, want := itemIDs(items.Items), []string{vid(2), vid(1), vid(3)}; !slices.Equal(got, want) {
		t.Fatalf("items %v, want %v", got, want)
	}
	if v := items.Items[0].Video; v == nil || v.Title == nil || *v.Title != "Lesson 2" || v.DurationSec == nil {
		t.Errorf("item card %+v", items.Items[0].Video)
	}

	// the library's list: counts and posters of the visible videos only
	var list listBody
	if code := e.do(asViewer(), "GET", "/api/collections?library="+libA, nil, &list); code != 200 || len(list.Items) != 1 {
		t.Fatalf("viewer list A: %d %+v", code, list)
	}
	if got := list.Items[0]; got.Videos == nil || *got.Videos != 3 || !slices.Equal(got.PosterVideoIDs, []string{vid(2), vid(1), vid(3)}) {
		t.Errorf("counts %+v", got)
	}

	// reorder (the replace-all PATCH), rename, remove one
	order := []map[string]any{{"video_id": vid(3), "position": 10}, {"video_id": vid(1), "position": 20}, {"video_id": vid(2), "position": 30}}
	if code := e.do(asAdmin(), "PATCH", "/api/collections/"+c.ID, map[string]any{"items": order, "name": " Ramadan 1447 "}, nil); code != 200 {
		t.Fatalf("patch: %d", code)
	}
	if code := e.do(asAdmin(), "DELETE", "/api/collections/"+c.ID+"/items/"+vid(1), nil, nil); code != http.StatusNoContent {
		t.Fatalf("remove: %d", code)
	}
	e.do(asViewer(), "GET", "/api/collections/"+c.ID+"/items", nil, &items)
	if got, want := itemIDs(items.Items), []string{vid(3), vid(2)}; !slices.Equal(got, want) {
		t.Errorf("after reorder and remove: %v, want %v", got, want)
	}
	var got Collection
	e.do(asViewer(), "GET", "/api/collections/"+c.ID, nil, &got)
	if got.Name != "Ramadan 1447" {
		t.Errorf("renamed to %q", got.Name)
	}
	if code := e.do(asAdmin(), "PATCH", "/api/collections/"+c.ID, map[string]any{"name": "  "}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("empty name: %d", code)
	}
}

func TestCollectionRoles(t *testing.T) {
	e := newEnv(t)
	a := e.create(map[string]any{"library_id": libA, "name": "A list"})
	b := e.create(map[string]any{"library_id": libB, "name": "B list"})
	e.do(asAdmin(), "POST", "/api/collections/"+a.ID+"/items", map[string]any{"video_ids": []string{vid(1)}}, nil)

	// a viewer sees only their libraries' collections
	var list listBody
	e.do(asViewer(), "GET", "/api/collections", nil, &list)
	if len(list.Items) != 1 || list.Items[0].ID != a.ID {
		t.Errorf("viewer list: %+v", list.Items)
	}
	for _, path := range []string{
		"/api/collections?library=" + libB,
		"/api/collections?library=nope",
		"/api/collections/" + b.ID,
		"/api/collections/" + b.ID + "/items",
	} {
		if code := e.do(asViewer(), "GET", path, nil, nil); code != http.StatusNotFound {
			t.Errorf("viewer GET %s: %d, want 404", path, code)
		}
	}
	// every write is an admin's
	for _, w := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/collections", map[string]any{"library_id": libA, "name": "x"}},
		{"PATCH", "/api/collections/" + a.ID, map[string]any{"name": "y"}},
		{"POST", "/api/collections/" + a.ID + "/items", map[string]any{"video_ids": []string{vid(2)}}},
		{"DELETE", "/api/collections/" + a.ID + "/items/" + vid(1), nil},
		{"DELETE", "/api/collections/" + a.ID, nil},
	} {
		if code := e.do(asViewer(), w.method, w.path, w.body, nil); code != http.StatusForbidden {
			t.Errorf("viewer %s %s: %d, want 403", w.method, w.path, code)
		}
		if code := e.do(nil, w.method, w.path, w.body, nil); code != http.StatusForbidden {
			t.Errorf("anonymous %s %s: %d, want 403", w.method, w.path, code)
		}
	}
	if code := e.do(nil, "GET", "/api/collections", nil, nil); code != http.StatusForbidden {
		t.Errorf("anonymous list: %d", code)
	}
	// one unknown video refuses the whole append
	if code := e.do(asAdmin(), "POST", "/api/collections/"+a.ID+"/items",
		map[string]any{"video_ids": []string{vid(2), "00000000-0000-4000-8000-000000000999"}}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("unknown video: %d", code)
	}
	var items itemsBody
	e.do(asAdmin(), "GET", "/api/collections/"+a.ID+"/items", nil, &items)
	if len(items.Items) != 1 {
		t.Errorf("a refused append wrote %v", itemIDs(items.Items))
	}
	// the same name twice in a library is a conflict
	if code := e.do(asAdmin(), "POST", "/api/collections", map[string]any{"library_id": libA, "name": "A list"}, nil); code != http.StatusConflict {
		t.Errorf("duplicate name: %d", code)
	}
}

func TestSavedFilterCollection(t *testing.T) {
	e := newEnv(t)
	long := e.create(map[string]any{"library_id": libA, "name": "Long ones", "is_smart": true,
		"smart_query": map[string]any{"filters": map[string]string{"duration": "long", "sort": "title", "order": "asc"}}})

	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if code := e.do(asViewer(), "GET", "/api/collections/"+long.ID+"/items", nil, &page); code != 200 {
		t.Fatalf("items: %d", code)
	}
	var got []string
	for _, it := range page.Items {
		got = append(got, it.ID)
	}
	// library A's long videos only: never B's 5 (the collection's library)
	if want := []string{vid(1), vid(2), vid(4)}; !slices.Equal(got, want) {
		t.Errorf("saved filter lists %v, want %v", got, want)
	}

	var list listBody
	e.do(asViewer(), "GET", "/api/collections?library="+libA, nil, &list)
	if len(list.Items) != 1 || !list.Items[0].IsSmart || list.Items[0].Videos != nil {
		t.Errorf("smart in the list: %+v", list.Items)
	}

	for name, sq := range map[string]any{
		"unknown key":  map[string]any{"filters": map[string]string{"library": libB}},
		"empty value":  map[string]any{"filters": map[string]string{"q": " "}},
		"no filters":   map[string]any{"filters": map[string]string{}},
		"favorite key": map[string]any{"filters": map[string]string{"favorite": "true"}},
	} {
		if code := e.do(asAdmin(), "POST", "/api/collections", map[string]any{"library_id": libA, "name": name, "is_smart": true, "smart_query": sq}, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422", name, code)
		}
	}
	// a smart collection's items are its query's
	if code := e.do(asAdmin(), "POST", "/api/collections/"+long.ID+"/items", map[string]any{"video_ids": []string{vid(1)}}, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("append to smart: %d", code)
	}
	if code := e.do(asAdmin(), "PATCH", "/api/collections/"+long.ID,
		map[string]any{"smart_query": map[string]any{"filters": map[string]string{"duration": "short"}}}, nil); code != 200 {
		t.Fatalf("edit filters: %d", code)
	}
	e.do(asViewer(), "GET", "/api/collections/"+long.ID+"/items", nil, &page)
	if len(page.Items) != 1 || page.Items[0].ID != vid(3) {
		t.Errorf("after the edit: %+v", page.Items)
	}
}
