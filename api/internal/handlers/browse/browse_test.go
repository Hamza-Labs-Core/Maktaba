package browse

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/libraries"
)

const (
	libA     = "00000000-0000-4000-8000-00000000000a"
	libB     = "00000000-0000-4000-8000-00000000000b"
	libC     = "00000000-0000-4000-8000-00000000000c" // empty
	viewer   = "00000000-0000-4000-8000-0000000000e1" // library A only
	adminID  = "00000000-0000-4000-8000-0000000000e2"
	other    = "00000000-0000-4000-8000-0000000000e3"
	seriesA  = "00000000-0000-4000-8000-0000000005a1"
	seriesB  = "00000000-0000-4000-8000-0000000005b1"
	unknownL = "00000000-0000-4000-8000-0000000000ff"
)

// v names fixture videos: a1..a5 in library A, b1, b2 in library B.
func v(name string) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", []byte(name))
}

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// testDB is the tables the routes read, as the SQLite migrations make
// them (UUIDs and times as text).
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, s := range []string{
		`CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT NOT NULL, roots TEXT, settings TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, title TEXT, filename TEXT, duration_sec REAL,
			state TEXT, created_at TEXT, mtime TEXT, deleted_at TEXT, detected_language TEXT)`,
		`CREATE TABLE playback_state (user_id TEXT, video_id TEXT, position_sec REAL, completed INTEGER, updated_at TEXT,
			PRIMARY KEY (user_id, video_id))`,
		`CREATE TABLE watch_sessions (id TEXT PRIMARY KEY, user_id TEXT, video_id TEXT, started_at TEXT)`,
		`CREATE TABLE series (id TEXT PRIMARY KEY, name TEXT, name_override TEXT, numbering TEXT NOT NULL DEFAULT 'season',
			created_at TEXT)`,
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT, updated_by TEXT)`,
		`CREATE TABLE series_episodes (series_id TEXT, video_id TEXT, season INTEGER, episode INTEGER, absolute_number INTEGER,
			season_override INTEGER, episode_override INTEGER)`,
		`CREATE TABLE tags (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE video_tags (video_id TEXT, tag_id TEXT, PRIMARY KEY (video_id, tag_id))`,
		`CREATE TABLE speakers (id TEXT PRIMARY KEY, video_id TEXT, cluster_label TEXT, name TEXT NOT NULL)`,
		`CREATE TABLE users (id TEXT PRIMARY KEY)`,
		`CREATE TABLE media_info (video_id TEXT PRIMARY KEY, width INTEGER, height INTEGER)`,
		`CREATE TABLE user_library_pins (user_id TEXT NOT NULL, library_id TEXT NOT NULL, position INTEGER NOT NULL,
			created_at TEXT NOT NULL, PRIMARY KEY (user_id, library_id))`,
	} {
		exec(s)
	}
	execMigration(t, db, "0162_user_favorites_playlists.sqlite.sql")
	// slot 0163's column (its file runs through the SQLite migrator, which
	// reads ADD COLUMN IF NOT EXISTS: TestSQLiteMigrate_AppliesEveryTwinToAFreshDatabase)
	exec(`ALTER TABLE user_playlists ADD COLUMN hand_ordered INTEGER NOT NULL DEFAULT 0`)
	exec(`INSERT INTO libraries (id, name) VALUES ($1,'Alpha'), ($2,'Beta'), ($3,'Gamma')`, libA, libB, libC)
	day := func(d int) string { return iso(now.AddDate(0, 0, -d)) }
	for _, r := range []struct {
		name, lib, state string
		dur              any
		created, mtime   string
		deleted          any
	}{
		{"a1", libA, "ready", 1000.0, day(5), "2024-03-01T00:00:00.000Z", nil},
		{"a2", libA, "ready", 600.0, day(4), "2024-11-01T00:00:00.000Z", nil},
		{"a3", libA, "ready", nil, day(3), "2023-01-05T00:00:00.000Z", nil},
		{"a4", libA, "missing", 500.0, day(1), "2024-01-01T00:00:00.000Z", nil},
		{"a5", libA, "ready", 500.0, day(1), "2024-01-01T00:00:00.000Z", day(1)},
		{"a6", libA, "superseded", 500.0, day(1), "2024-01-01T00:00:00.000Z", nil},
		{"b1", libB, "ready", 1000.0, day(2), "2019-06-01T00:00:00.000Z", nil},
		{"b2", libB, "ready", 1000.0, day(0), "2019-06-01T00:00:00.000Z", nil},
	} {
		exec(`INSERT INTO videos (id, library_id, title, filename, duration_sec, state, created_at, mtime, deleted_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			v(r.name), r.lib, "Title "+r.name, r.name+".mp4", r.dur, r.state, r.created, r.mtime, r.deleted)
	}
	// the viewer: a1 under way, a2 done, a3 barely started, b1 under way
	// (library B: never theirs to see), a4 under way but missing
	exec(`INSERT INTO playback_state VALUES ($1,$2,100,0,$6), ($1,$3,600,1,$7), ($1,$4,10,0,$8), ($1,$5,100,0,$9), ($1,$10,100,0,$9)`,
		viewer, v("a1"), v("a2"), v("a3"), v("b1"), day(1), day(2), day(3), day(0), v("a4"))
	ws := 0
	session := func(user, video, at string) {
		ws++
		exec(`INSERT INTO watch_sessions VALUES ($1,$2,$3,$4)`, fmt.Sprintf("s%d", ws), user, video, at)
	}
	for range 3 {
		session(other, v("a1"), day(10))
	}
	session(other, v("a2"), day(10))
	for range 5 {
		session(other, v("a3"), day(200)) // outside the 90 days
	}
	for range 10 {
		session(other, v("b1"), day(1))
	}
	session(other, v("a4"), day(1)) // missing: never listed
	session(viewer, v("b2"), day(0))
	session(viewer, v("a3"), day(0)) // newer than a3's playback state
	exec(`INSERT INTO series (id, name, name_override) VALUES ($1,'Tafsir',NULL), ($2,'B series','B renamed')`, seriesA, seriesB)
	exec(`INSERT INTO series_episodes (series_id, video_id, episode) VALUES ($1,$2,1), ($1,$3,2), ($1,$4,3), ($5,$6,1)`,
		seriesA, v("a1"), v("a2"), v("a4"), seriesB, v("b1"))
	return db
}

func asViewer() *principal.Principal {
	return &principal.Principal{UserID: viewer, Libraries: []string{libA}}
}

func asAdmin() *principal.Principal {
	return &principal.Principal{UserID: adminID, IsAdmin: true, AccessAllLibraries: true}
}

type env struct {
	t  *testing.T
	db *sql.DB
	r  chi.Router
}

func newEnv(t *testing.T) *env {
	db := testDB(t)
	r := chi.NewRouter()
	// the libraries handler first, as the router mounts them
	(&libraries.Handler{DB: db}).Mount(r)
	(&Handler{DB: db, NowFunc: func() time.Time { return now }}).Mount(r)
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
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, rec.Body.String())
		}
	}
	if rec.Code >= 500 {
		e.t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
	}
	return rec.Code
}

func ids(cards []Card) []string {
	out := []string{}
	for _, c := range cards {
		out = append(out, c.ID)
	}
	return out
}

func names(ns ...string) []string {
	out := []string{}
	for _, n := range ns {
		out = append(out, v(n))
	}
	return out
}

func TestSummary(t *testing.T) {
	e := newEnv(t)
	var body struct{ Items []LibrarySummary }
	if code := e.do(asViewer(), "GET", "/api/libraries/summary", nil, &body); code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(body.Items) != 1 || body.Items[0].ID != libA {
		t.Fatalf("viewer sees %+v, want library A only", body.Items)
	}
	a := body.Items[0]
	if a.Name != "Alpha" || a.Videos != 3 || a.DurationSec != 1600 {
		t.Errorf("card %+v: want 3 visible videos of 1600 s", a)
	}
	if want := names("a3", "a2", "a1"); !slices.Equal(a.PosterVideoIDs, want) {
		t.Errorf("posters %v, want %v (newest visible first)", a.PosterVideoIDs, want)
	}
	if a.LatestAddedAt == nil || !a.LatestAddedAt.Equal(now.AddDate(0, 0, -3)) {
		t.Errorf("latest_added_at %v, want a3's", a.LatestAddedAt)
	}
	if a.Pinned || a.PinPosition != nil {
		t.Errorf("not pinned yet: %+v", a)
	}

	if code := e.do(asAdmin(), "GET", "/api/libraries/summary", nil, &body); code != 200 {
		t.Fatalf("admin: status %d", code)
	}
	var got []string
	for _, it := range body.Items {
		got = append(got, it.Name)
	}
	if !slices.Equal(got, []string{"Alpha", "Beta", "Gamma"}) {
		t.Fatalf("admin sees %v", got)
	}
	if c := body.Items[2]; c.Videos != 0 || len(c.PosterVideoIDs) != 0 || c.LatestAddedAt != nil {
		t.Errorf("empty library card %+v", c)
	}

	// no library at all, and nobody signed in
	if code := e.do(&principal.Principal{UserID: other}, "GET", "/api/libraries/summary", nil, &body); code != 200 || len(body.Items) != 0 {
		t.Errorf("no library: %d %+v", code, body.Items)
	}
	if code := e.do(nil, "GET", "/api/libraries/summary", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", code)
	}
}

type pinsBody struct {
	LibraryIDs []string        `json:"library_ids"`
	Items      []PinnedLibrary `json:"items"`
}

func TestPins(t *testing.T) {
	e := newEnv(t)
	var body pinsBody
	if code := e.do(asViewer(), "PUT", "/api/me/pinned-libraries", map[string]any{"library_ids": []string{libA, libA}}, &body); code != 200 {
		t.Fatalf("pin A: %d", code)
	}
	if !slices.Equal(body.LibraryIDs, []string{libA}) || body.Items[0].Name != "Alpha" || body.Items[0].Position != 0 {
		t.Fatalf("pins %+v: the repeat counts once", body)
	}
	for name, ids := range map[string][]string{
		"another user's library": {libA, libB},
		"unknown":                {unknownL},
		"malformed":              {"x"},
	} {
		if code := e.do(asViewer(), "PUT", "/api/me/pinned-libraries", map[string]any{"library_ids": ids}, nil); code != 404 {
			t.Errorf("%s: %d, want 404", name, code)
		}
	}
	var many []string
	for i := range MaxPins + 1 {
		many = append(many, fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
	}
	if code := e.do(asAdmin(), "PUT", "/api/me/pinned-libraries", map[string]any{"library_ids": many}, nil); code != 422 {
		t.Errorf("21 pins: %d, want 422", code)
	}
	// a refused PUT changed nothing
	if code := e.do(asViewer(), "GET", "/api/me/pinned-libraries", nil, &body); code != 200 || !slices.Equal(body.LibraryIDs, []string{libA}) {
		t.Fatalf("after refusals: %d %+v", code, body)
	}

	// the admin's order is kept, and the summary shows it
	if code := e.do(asAdmin(), "PUT", "/api/me/pinned-libraries", map[string]any{"library_ids": []string{libC, libA}}, &body); code != 200 ||
		!slices.Equal(body.LibraryIDs, []string{libC, libA}) {
		t.Fatalf("admin pins: %d %+v", code, body)
	}
	var sum struct{ Items []LibrarySummary }
	e.do(asAdmin(), "GET", "/api/libraries/summary", nil, &sum)
	for _, it := range sum.Items {
		want := map[string]int{libC: 0, libA: 1}
		pos, pinned := want[it.ID]
		if it.Pinned != pinned || (pinned && (it.PinPosition == nil || *it.PinPosition != pos)) {
			t.Errorf("summary %s: pinned %v at %v, want %v at %d", it.Name, it.Pinned, it.PinPosition, pinned, pos)
		}
	}

	// a pin to a library the viewer lost is kept but not shown
	if _, err := e.db.Exec(`INSERT INTO user_library_pins VALUES ($1,$2,1,'x')`, viewer, libB); err != nil {
		t.Fatal(err)
	}
	if e.do(asViewer(), "GET", "/api/me/pinned-libraries", nil, &body); !slices.Equal(body.LibraryIDs, []string{libA}) {
		t.Errorf("viewer sees pins %v, want A only", body.LibraryIDs)
	}
	// an empty list clears them
	if e.do(asViewer(), "PUT", "/api/me/pinned-libraries", map[string]any{"library_ids": []string{}}, &body); len(body.LibraryIDs) != 0 {
		t.Errorf("cleared: %v", body.LibraryIDs)
	}

	if code := e.do(nil, "GET", "/api/me/pinned-libraries", nil, nil); code != 401 {
		t.Errorf("anonymous GET: %d", code)
	}
	if code := e.do(nil, "PUT", "/api/me/pinned-libraries", map[string]any{"library_ids": []string{}}, nil); code != 401 {
		t.Errorf("anonymous PUT: %d", code)
	}
	// the admin token has no user row to pin for
	if code := e.do(&principal.Principal{UserID: "admin-token", AccessAllLibraries: true}, "PUT", "/api/me/pinned-libraries",
		map[string]any{"library_ids": []string{}}, nil); code != 403 {
		t.Errorf("admin token PUT: %d", code)
	}
}

type homeBody struct {
	ContinueWatching []Card       `json:"continue_watching"`
	RecentlyAdded    []Card       `json:"recently_added"`
	RecentlyWatched  []Card       `json:"recently_watched"`
	Pinned           []PinnedRows `json:"pinned"`
}

func TestHome(t *testing.T) {
	e := newEnv(t)
	if _, err := e.db.Exec(`INSERT INTO user_library_pins VALUES ($1,$2,0,'x'), ($1,$3,1,'x')`, viewer, libA, libB); err != nil {
		t.Fatal(err)
	}
	var h homeBody
	if code := e.do(asViewer(), "GET", "/api/home", nil, &h); code != 200 {
		t.Fatalf("status %d", code)
	}
	if got := ids(h.ContinueWatching); !slices.Equal(got, names("a1")) {
		t.Errorf("continue watching %v, want a1 only (b1 is library B, a4 missing, a3 under 30 s, a2 done)", got)
	}
	if c := h.ContinueWatching[0]; c.PositionSec == nil || *c.PositionSec != 100 || c.LastWatchedAt == nil {
		t.Errorf("continue card %+v", c)
	}
	if got := ids(h.RecentlyAdded); !slices.Equal(got, names("a3", "a2", "a1")) {
		t.Errorf("recently added %v", got)
	}
	// a3's session today is newer than the playback states
	if got := ids(h.RecentlyWatched); !slices.Equal(got, names("a3", "a1", "a2")) {
		t.Errorf("recently watched %v", got)
	}
	if len(h.Pinned) != 1 || h.Pinned[0].Library.ID != libA || h.Pinned[0].Library.Name != "Alpha" {
		t.Fatalf("pinned %+v: B is not the viewer's", h.Pinned)
	}
	if got := ids(h.Pinned[0].RecentlyAdded); !slices.Equal(got, names("a3", "a2", "a1")) {
		t.Errorf("pinned recently added %v", got)
	}
	if got := ids(h.Pinned[0].ContinueWatching); !slices.Equal(got, names("a1")) {
		t.Errorf("pinned continue %v", got)
	}

	// the admin sees every library's recently added, but only their own
	// (empty) playback
	if code := e.do(asAdmin(), "GET", "/api/home", nil, &h); code != 200 {
		t.Fatalf("admin: %d", code)
	}
	if got := ids(h.RecentlyAdded); !slices.Equal(got, names("b2", "b1", "a3", "a2", "a1")) {
		t.Errorf("admin recently added %v", got)
	}
	if len(h.ContinueWatching)+len(h.RecentlyWatched)+len(h.Pinned) != 0 {
		t.Errorf("admin sees another user's playback: %+v", h)
	}
	if code := e.do(nil, "GET", "/api/home", nil, nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	if code := e.do(&principal.Principal{UserID: other}, "GET", "/api/home", nil, &h); code != 200 || len(h.RecentlyAdded) != 0 {
		t.Errorf("no library: %d %+v", code, h)
	}
}

type hubBody struct {
	Library LibraryRef `json:"library"`
	Rows    struct {
		ContinueWatching []Card      `json:"continue_watching"`
		RecentlyAdded    []Card      `json:"recently_added"`
		RecentlyWatched  []Card      `json:"recently_watched"`
		MostWatched      []Card      `json:"most_watched"`
		Series           []SeriesRef `json:"series"`
		Years            []YearCount `json:"years"`
	} `json:"rows"`
}

func TestHub(t *testing.T) {
	e := newEnv(t)
	var h hubBody
	if code := e.do(asViewer(), "GET", "/api/libraries/"+libA+"/hub", nil, &h); code != 200 {
		t.Fatalf("status %d", code)
	}
	if h.Library.Name != "Alpha" || h.Library.Videos == nil || *h.Library.Videos != 3 {
		t.Errorf("library %+v", h.Library)
	}
	rows := h.Rows
	if got := ids(rows.ContinueWatching); !slices.Equal(got, names("a1")) {
		t.Errorf("continue %v", got)
	}
	if got := ids(rows.RecentlyAdded); !slices.Equal(got, names("a3", "a2", "a1")) {
		t.Errorf("recently added %v", got)
	}
	if got := ids(rows.RecentlyWatched); !slices.Equal(got, names("a3", "a1", "a2")) {
		t.Errorf("recently watched %v", got)
	}
	// a3's five old plays are outside the 90 days (its one today counts,
	// tied with a2's: the more recently added, a3, comes first); a4 is missing
	if got := ids(rows.MostWatched); !slices.Equal(got, names("a1", "a3", "a2")) {
		t.Errorf("most watched %v", got)
	} else if *rows.MostWatched[0].Plays != 3 || *rows.MostWatched[1].Plays != 1 || *rows.MostWatched[2].Plays != 1 {
		t.Errorf("plays %d, %d", *rows.MostWatched[0].Plays, *rows.MostWatched[1].Plays)
	}
	// added after a3, a2 now comes first of the two (the id would not move it)
	if _, err := e.db.Exec(`UPDATE videos SET created_at = $1 WHERE id = $2`, iso(now), v("a2")); err != nil {
		t.Fatal(err)
	}
	var again hubBody
	e.do(asViewer(), "GET", "/api/libraries/"+libA+"/hub", nil, &again)
	if got := ids(again.Rows.MostWatched); !slices.Equal(got, names("a1", "a2", "a3")) {
		t.Errorf("most watched, a2 added last: %v", got)
	}
	if len(rows.Series) != 1 || rows.Series[0].ID != seriesA || rows.Series[0].Episodes != 2 || rows.Series[0].Name != "Tafsir" {
		t.Errorf("series %+v: a4 is missing", rows.Series)
	}
	// the card's picture: its first visible episode, as GET /api/series gives it
	if len(rows.Series) == 1 && rows.Series[0].PosterVideoID != v("a1") {
		t.Errorf("series poster %q, want episode 1 (%s)", rows.Series[0].PosterVideoID, v("a1"))
	}
	if want := []YearCount{{2024, 2}, {2023, 1}}; !slices.Equal(rows.Years, want) {
		t.Errorf("years %+v, want %+v", rows.Years, want)
	}

	// library B's hub: the admin sees it, renamed series and all
	if code := e.do(asAdmin(), "GET", "/api/libraries/"+libB+"/hub", nil, &h); code != 200 {
		t.Fatalf("admin B: %d", code)
	}
	if len(h.Rows.Series) != 1 || h.Rows.Series[0].Name != "B renamed" ||
		len(h.Rows.MostWatched) != 2 || *h.Rows.MostWatched[0].Plays != 10 {
		t.Errorf("B hub %+v", h.Rows)
	}

	// one answer for another library, an unknown one and a malformed id
	for _, id := range []string{libB, unknownL, "nope"} {
		if code := e.do(asViewer(), "GET", "/api/libraries/"+id+"/hub", nil, nil); code != 404 {
			t.Errorf("viewer hub %s: %d, want 404", id, code)
		}
	}
	if code := e.do(asAdmin(), "GET", "/api/libraries/"+unknownL+"/hub", nil, nil); code != 404 {
		t.Errorf("admin unknown hub: %d", code)
	}
	if code := e.do(nil, "GET", "/api/libraries/"+libA+"/hub", nil, nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
}

// The summary route is a static segment beside the libraries handler's
// /api/libraries/{id}: chi must give it to browse, and {id} must still
// reach the libraries handler.
func TestSummaryRouteBesideLibraryID(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("GET", "/api/libraries/summary", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), asAdmin()))
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`"poster_video_ids"`)) {
		t.Fatalf("summary: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/libraries/not-a-uuid", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), asAdmin()))
	rec = httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("libraries {id}: %d %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`poster_video_ids`)) {
		t.Fatal("{id} reached browse")
	}
}
