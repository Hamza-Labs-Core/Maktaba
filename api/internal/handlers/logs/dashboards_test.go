package logs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Epic 53 story 53.12 T04/T05: the boards, as nobody (401), a viewer (403) and an admin;
// the role's boards are proxied with their range, Use is counted from watch_sessions
// (plays per day by app, people per day, the top videos) and names nobody.
func TestDashboardRoutes(t *testing.T) {
	var seen string
	role := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = io.WriteString(w, `{"board":"overview","series":[]}`)
	}))
	defer role.Close()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if _, err := db.Exec(`CREATE TABLE videos (id TEXT PRIMARY KEY, title TEXT);
		CREATE TABLE watch_sessions (id TEXT, user_id TEXT, video_id TEXT, started_at TIMESTAMP, platform TEXT);
		INSERT INTO videos VALUES ('v1', 'Tafsir 1'), ('v2', NULL)`); err != nil {
		t.Fatal(err)
	}
	for i, row := range [][3]string{{"u1", "v1", "web"}, {"u1", "v1", "web"}, {"u2", "v1", "android"}, {"u2", "v2", ""}} {
		if _, err := db.Exec(`INSERT INTO watch_sessions VALUES (?, ?, ?, ?, ?)`, i, row[0], row[1], now.Add(-time.Hour),
			row[2]); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{IndexAddr: strings.TrimPrefix(role.URL, "http://"), DB: db}
	r := chi.NewRouter()
	h.Mount(r)
	call := func(path string, p *principal.Principal) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	for _, p := range []string{"/api/admin/dashboards/overview?range=7d", "/api/admin/dashboards/use",
		"/api/admin/dashboards/overview?range=24h&machine=n07"} {
		if c := call(p, nil).Code; c != http.StatusUnauthorized {
			t.Errorf("%s as nobody: %d", p, c)
		}
		if c := call(p, &principal.Principal{UserID: "v"}).Code; c != http.StatusForbidden {
			t.Errorf("%s as a viewer: %d", p, c)
		}
	}
	admin := &principal.Principal{UserID: "a", IsAdmin: true}
	if rec := call("/api/admin/dashboards/overview?range=7d", admin); rec.Code != 200 || seen != "/dashboards/overview?range=7d" {
		t.Fatalf("overview: %d, the role saw %q", rec.Code, seen)
	}
	// one machine of many (machine=) reaches the role as asked
	if rec := call("/api/admin/dashboards/overview?range=24h&machine=n07", admin); rec.Code != 200 ||
		seen != "/dashboards/overview?range=24h&machine=n07" {
		t.Fatalf("overview of one machine: %d, the role saw %q", rec.Code, seen)
	}
	rec := call("/api/admin/dashboards/use?range=7d", admin)
	var doc useDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := map[string]float64{}
	for _, s := range doc.Series {
		for _, p := range s.Points {
			got[s.Name+"{"+s.Labels+"}"] += p[1]
		}
	}
	if got["maktaba_plays_per_day{platform=web}"] != 2 || got["maktaba_plays_per_day{platform=android}"] != 1 ||
		got["maktaba_plays_per_day{platform=other}"] != 1 || got["maktaba_people_per_day{}"] != 2 {
		t.Fatalf("use: %v", got)
	}
	if len(doc.Top) != 2 || doc.Top[0].Title != "Tafsir 1" || doc.Top[0].Plays != 3 || doc.Top[1].Title != "v2" {
		t.Fatalf("top: %+v", doc.Top)
	}
	if strings.Contains(rec.Body.String(), "u1") || strings.Contains(rec.Body.String(), "u2") {
		t.Error("Use names nobody")
	}
	if c := call("/api/admin/dashboards/use?range=2y", admin).Code; c != http.StatusBadRequest {
		t.Errorf("a bad range: %d", c)
	}
}

// Story 53.12: a joined machine that is offline ranks first on the Overview. The API asks
// the cluster which machines are offline and passes them to the role as offline=; a
// caller's own offline= never reaches it; other boards are passed as asked; it is still
// admins only.
func TestDashboardOverviewOffline(t *testing.T) {
	var seen string
	role := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = io.WriteString(w, `{"board":"overview","series":[],"machines":{"total":2,"shown":["n2","n1"],"all":["n1","n2"],"offline":["n2"]}}`)
	}))
	defer role.Close()
	offline, fail := []string{"n2", "n9"}, false
	h := &Handler{IndexAddr: strings.TrimPrefix(role.URL, "http://"), Offline: func(context.Context) ([]string, error) {
		if fail {
			return nil, errors.New("cluster store down")
		}
		return offline, nil
	}}
	r := chi.NewRouter()
	h.Mount(r)
	call := func(path string, p *principal.Principal) *httptest.ResponseRecorder {
		seen = ""
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if c := call("/api/admin/dashboards/overview?range=1h", &principal.Principal{UserID: "v"}).Code; c != http.StatusForbidden || seen != "" {
		t.Fatalf("a viewer: %d, the role saw %q", c, seen)
	}
	admin := &principal.Principal{UserID: "a", IsAdmin: true}
	rec := call("/api/admin/dashboards/overview?range=1h", admin)
	if rec.Code != 200 || seen != "/dashboards/overview?offline=n2%2Cn9&range=1h" {
		t.Fatalf("overview: %d, the role saw %q", rec.Code, seen)
	}
	var doc struct {
		Machines struct {
			Shown, Offline []string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || strings.Join(doc.Machines.Offline, ",") != "n2" ||
		doc.Machines.Shown[0] != "n2" {
		t.Fatalf("%v %s", err, rec.Body)
	}
	// the caller's own offline= is replaced by the cluster's
	if call("/api/admin/dashboards/overview?range=1h&offline=n1&machine=n2", admin); seen !=
		"/dashboards/overview?machine=n2&offline=n2%2Cn9&range=1h" {
		t.Fatalf("a caller's offline=: the role saw %q", seen)
	}
	// none offline: none passed, and a caller's dropped
	offline = []string{}
	if call("/api/admin/dashboards/overview?range=1h&offline=n1", admin); seen != "/dashboards/overview?range=1h" {
		t.Fatalf("none offline: the role saw %q", seen)
	}
	// the cluster unreadable: the board still answers, ranked by values
	fail = true
	if rec := call("/api/admin/dashboards/overview?range=1h", admin); rec.Code != 200 || seen != "/dashboards/overview?range=1h" {
		t.Fatalf("cluster down: %d, the role saw %q", rec.Code, seen)
	}
	// another board: passed as asked, never with offline=
	if call("/api/admin/dashboards/storage?range=1h&offline=n1", admin); seen != "/dashboards/storage?range=1h" {
		t.Fatalf("storage: the role saw %q", seen)
	}
}

// Story 53.12: the Overview names its machines as the Machines page does. The API adds
// names {id: name} for every id the answer mentions (machine=, machines.all/shown/offline,
// each line's machine= label) and no other; a name is bounded and stripped of control and
// bidi override characters; an id with no name, or every id when the cluster can't be
// read, is named by itself; other boards and a failing role pass unchanged.
func TestDashboardOverviewNames(t *testing.T) {
	answer := `{"board":"overview","machine":"n3","series":[{"name":"maktaba_host_load1","labels":"machine=home","kind":"gauge","points":[]},{"name":"x","labels":"id=core.api,machine=n4","kind":"gauge","points":[]}],"machines":{"total":3,"shown":["n2","n1"],"all":["home","n1","n2"],"offline":["n2"]}}`
	status := http.StatusOK
	role := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if strings.HasPrefix(r.URL.Path, "/dashboards/overview") {
			_, _ = io.WriteString(w, answer)
			return
		}
		_, _ = io.WriteString(w, `{"board":"storage","series":[]}`)
	}))
	defer role.Close()
	long := strings.Repeat("ب", 200)
	var asked []string
	fail := false
	h := &Handler{IndexAddr: strings.TrimPrefix(role.URL, "http://"),
		Names: func(_ context.Context, ids []string) (map[string]string, error) {
			asked = append([]string(nil), ids...)
			if fail {
				return nil, errors.New("cluster store down")
			}
			return map[string]string{"home": "Living room", "n1": " Attic\u202e NAS\n ", "n2": long,
				"n4": "Office", "n9": "never mentioned"}, nil
		}}
	r := chi.NewRouter()
	h.Mount(r)
	admin := &principal.Principal{UserID: "a", IsAdmin: true}
	call := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), admin))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	type named struct {
		Names    map[string]string
		Machines struct{ All []string }
	}
	rec := call("/api/admin/dashboards/overview?range=1h")
	var doc named
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); rec.Code != 200 || err != nil {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	sort.Strings(asked)
	if strings.Join(asked, ",") != "home,n1,n2,n3,n4" {
		t.Fatalf("asked names of %v, want only the ids the answer mentions", asked)
	}
	cut := strings.Repeat("ب", maxMachineName-1) + "…"
	want := map[string]string{"home": "Living room", "n1": "Attic NAS", "n2": cut, "n3": "n3", "n4": "Office"}
	if len(doc.Names) != len(want) {
		t.Fatalf("names %v, want %v", doc.Names, want)
	}
	for k, v := range want {
		if doc.Names[k] != v {
			t.Fatalf("names[%s] = %q, want %q", k, doc.Names[k], v)
		}
	}
	if strings.Join(doc.Machines.All, ",") != "home,n1,n2" {
		t.Fatalf("the rest of the answer changed: %s", rec.Body)
	}
	// the cluster unreadable: every id named by itself
	fail = true
	doc = named{}
	if err := json.Unmarshal(call("/api/admin/dashboards/overview?range=1h").Body.Bytes(), &doc); err != nil ||
		doc.Names["home"] != "home" || doc.Names["n1"] != "n1" || len(doc.Names) != 5 {
		t.Fatalf("cluster down: %v %v", doc.Names, err)
	}
	fail = false
	// other boards: no names
	if body := call("/api/admin/dashboards/storage?range=1h").Body.String(); strings.Contains(body, "names") {
		t.Fatalf("storage: %s", body)
	}
	// the role failing: its answer passes as it came
	status = http.StatusNotFound
	if rec := call("/api/admin/dashboards/overview?range=9y"); rec.Code != http.StatusNotFound ||
		strings.Contains(rec.Body.String(), "names") {
		t.Fatalf("role 404: %d %s", rec.Code, rec.Body)
	}
}

func TestMachineName(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  "",
		"   ":                               "",
		"Office":                            "Office",
		"a\x00b\tc":                         "abc",
		"\u2066evil\u2069":                  "evil",
		"\xff\xfeok":                        "ok",
		strings.Repeat("x", maxMachineName): strings.Repeat("x", maxMachineName),
	} {
		if got := machineName(in); got != want {
			t.Fatalf("machineName(%q) = %q, want %q", in, got, want)
		}
	}
}
