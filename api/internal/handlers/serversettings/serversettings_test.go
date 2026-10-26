package serversettings

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const adminID = "6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"

// fakeSupervisor answers /config/ as maktaba-server does, with the token "tok".
type fakeSupervisor struct {
	calls   atomic.Int32
	saved   string // the saved document's JSON
	putCode int
	putBody string
	restart int
	lastPut string
}

func (f *fakeSupervisor) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /config/settings":
			_, _ = io.WriteString(w, f.doc())
		case "PUT /config/settings":
			b, _ := io.ReadAll(r.Body)
			f.lastPut = string(b)
			if f.putCode != 0 && f.putCode != http.StatusOK {
				w.WriteHeader(f.putCode)
				_, _ = io.WriteString(w, f.putBody)
				return
			}
			f.saved = f.putBody
			_, _ = io.WriteString(w, f.doc())
		case "POST /config/restart":
			w.WriteHeader(f.restart)
			_, _ = io.WriteString(w, `{"restarting":true}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func (f *fakeSupervisor) doc() string {
	return `{"file":"/x/server.toml","saved":` + f.saved + `,"running":` + f.saved +
		`,"restart_needed":false,"can_restart":true}`
}

func savedDoc(pathMap string, listen string) string {
	return `{"media":{"path_map":` + pathMap + `,"roots":[]},"server":{"mdns":true,"listen":"` + listen +
		`","web_listen":":8088","stream_addr":":8081","public_url":"","web_public_url":""}}`
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT NOT NULL, roots TEXT)`,
		`CREATE TABLE audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, category TEXT NOT NULL,
			action TEXT NOT NULL, actor_user_id TEXT, target_id TEXT, payload TEXT NOT NULL DEFAULT '{}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func fixture(t *testing.T, f *fakeSupervisor) (*Handler, http.Handler, *sql.DB) {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	tf := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tf, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := testDB(t)
	h := &Handler{Supervisor: &Supervisor{URL: srv.URL, TokenFile: tf}, DB: db,
		Shares: func(p string) string { return p }}
	r := chi.NewRouter()
	h.Mount(r)
	return h, r, db
}

func call(h http.Handler, p *principal.Principal, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var admin = &principal.Principal{UserID: adminID, IsAdmin: true}

// TestRefusesNonAdmins: the API enforces roles. An anonymous caller and a viewer get 403
// on every route before the supervisor is called.
func TestRefusesNonAdmins(t *testing.T) {
	f := &fakeSupervisor{saved: savedDoc("[]", ":8080"), restart: http.StatusAccepted}
	_, r, db := fixture(t, f)
	viewer := &principal.Principal{UserID: "u2", Libraries: []string{"lib"}}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/server/settings", ""},
		{"PUT", "/api/server/settings", `{"server":{"mdns":false}}`},
		{"POST", "/api/server/restart", ""},
	} {
		for _, p := range []*principal.Principal{nil, viewer} {
			if rec := call(r, p, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %v: %d, want 403", c.method, c.path, p, rec.Code)
			}
		}
	}
	if n := f.calls.Load(); n != 0 {
		t.Errorf("the supervisor was called %d times", n)
	}
	var rows int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&rows)
	if rows != 0 {
		t.Errorf("%d audit rows", rows)
	}
}

func TestWithoutSupervisor(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{}).Mount(r)
	rec := call(r, admin, "GET", "/api/server/settings", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"available":false`) ||
		!strings.Contains(rec.Body.String(), `"reason"`) {
		t.Errorf("get: %d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct{ method, path string }{{"PUT", "/api/server/settings"}, {"POST", "/api/server/restart"}} {
		if rec := call(r, admin, c.method, c.path, `{}`); rec.Code != http.StatusConflict {
			t.Errorf("%s: %d", c.path, rec.Code)
		}
	}
	// a supervisor that does not answer is no supervisor either
	t.Setenv("MAKTABA_SUPERVISOR_URL", "")
	if SupervisorFromEnv() != nil {
		t.Error("a supervisor from an empty environment")
	}
	h := &Handler{Supervisor: &Supervisor{URL: "http://127.0.0.1:1", TokenFile: filepath.Join(t.TempDir(), "none")}}
	r = chi.NewRouter()
	h.Mount(r)
	if rec := call(r, admin, "GET", "/api/server/settings", ""); !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Errorf("unreachable: %s", rec.Body)
	}
}

func TestGetReachability(t *testing.T) {
	share := t.TempDir()
	if err := os.MkdirAll(filepath.Join(share, "Talks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeSupervisor{saved: savedDoc(`[{"db":"/Volumes/O/","local":"`+share+`"}]`, ":8080")}
	h, r, db := fixture(t, f)
	h.Shares = func(p string) string {
		if p == "smb://nas/O/Talks" {
			return "/Volumes/O/Talks" // the home's mount of the share, then the path map
		}
		return p
	}
	if _, err := db.Exec(`INSERT INTO libraries VALUES
		('a', 'Alpha', '["/Volumes/O/Talks","/Volumes/O/missing"]'),
		('b', 'Beta', '{"/Volumes/O/file",smb://nas/O/Talks}')`); err != nil {
		t.Fatal(err)
	}
	rec := call(r, admin, "GET", "/api/server/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var doc struct {
		Available    bool    `json:"available"`
		File         string  `json:"file"`
		Reachability []Reach `json:"reachability"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Available || doc.File != "/x/server.toml" {
		t.Errorf("doc = %s", rec.Body)
	}
	want := []struct {
		root, local, problem string
	}{
		{"/Volumes/O/Talks", filepath.Join(share, "Talks"), ""},
		{"/Volumes/O/missing", filepath.Join(share, "missing"), "not-found"},
		{"/Volumes/O/file", filepath.Join(share, "file"), "not-a-directory"},
		{"smb://nas/O/Talks", filepath.Join(share, "Talks"), ""},
	}
	if len(doc.Reachability) != len(want) {
		t.Fatalf("reachability = %+v", doc.Reachability)
	}
	for i, w := range want {
		got := doc.Reachability[i]
		if got.Root != w.root || filepath.Clean(got.Local) != w.local || got.Problem != w.problem ||
			got.Reachable != (w.problem == "") {
			t.Errorf("[%d] = %+v, want %+v", i, got, w)
		}
	}
	if doc.Reachability[0].LibraryName != "Alpha" || doc.Reachability[3].LibraryID != "b" {
		t.Errorf("libraries: %+v", doc.Reachability)
	}
}

func TestPutAuditsTheChange(t *testing.T) {
	f := &fakeSupervisor{saved: savedDoc("[]", ":8080")}
	_, r, db := fixture(t, f)
	f.putBody = savedDoc(`[{"db":"/Volumes/O/","local":"/mnt/o"}]`, ":8090")
	body := `{"media":{"path_map":[{"db":"/Volumes/O/","local":"/mnt/o"}]},"server":{"listen":":8090"}}`
	rec := call(r, admin, "PUT", "/api/server/settings", body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"available":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f.lastPut != body {
		t.Errorf("forwarded %q", f.lastPut)
	}
	var category, action, actor, payload string
	if err := db.QueryRow(`SELECT category, action, actor_user_id, payload FROM audit_log`).
		Scan(&category, &action, &actor, &payload); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Changed map[string]any `json:"changed"`
	}
	_ = json.Unmarshal([]byte(payload), &p)
	if category != "server" || action != ActionSettingsChanged || actor != adminID ||
		len(p.Changed) != 2 || p.Changed["server.listen"] != ":8090" || p.Changed["media.path_map"] == nil {
		t.Errorf("audit row: %s %s %s %s", category, action, actor, payload)
	}

	// a bad value: the supervisor's 422 as it is, no audit row
	f.putCode, f.putBody = http.StatusUnprocessableEntity, `{"errors":[{"field":"server.listen","message":"in use"}]}`
	rec = call(r, admin, "PUT", "/api/server/settings", `{"server":{"listen":":7000"}}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"field":"server.listen"`) {
		t.Errorf("422: %d %s", rec.Code, rec.Body)
	}
	if rec := call(r, admin, "PUT", "/api/server/settings", `nope`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", rec.Code)
	}
	var rows int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&rows)
	if rows != 1 {
		t.Errorf("%d audit rows, want 1", rows)
	}
}

func TestRestart(t *testing.T) {
	f := &fakeSupervisor{saved: savedDoc("[]", ":8080"), restart: http.StatusConflict}
	_, r, db := fixture(t, f)
	// not under the launcher: the supervisor's 409, nothing audited
	if rec := call(r, admin, "POST", "/api/server/restart", ""); rec.Code != http.StatusConflict {
		t.Fatalf("409: %d %s", rec.Code, rec.Body)
	}
	f.restart = http.StatusAccepted
	if rec := call(r, admin, "POST", "/api/server/restart", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("restart: %d %s", rec.Code, rec.Body)
	}
	var action string
	var rows int
	_ = db.QueryRow(`SELECT count(*), max(action) FROM audit_log WHERE category = 'server'`).Scan(&rows, &action)
	if rows != 1 || action != ActionRestartRequested {
		t.Errorf("audit: %d %q", rows, action)
	}
}

func TestParseRoots(t *testing.T) {
	for in, want := range map[string]string{
		`{/a,"/b c","/d\"e"}`: `/a|/b c|/d"e`,
		`["/x","/y"]`:         `/x|/y`,
		`{}`:                  ``,
		``:                    ``,
	} {
		if got := strings.Join(parseRoots(in), "|"); got != want {
			t.Errorf("parseRoots(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestProblem(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	h := &Handler{}
	if got := h.problem("relative/path"); got != "not-found" {
		t.Errorf("relative: %q", got)
	}
	if os.Geteuid() != 0 {
		if got := h.problem(locked); got != "not-readable" {
			t.Errorf("locked: %q", got)
		}
	}
	if got := h.problem(dir); got != "" {
		t.Errorf("readable: %q", got)
	}
}
