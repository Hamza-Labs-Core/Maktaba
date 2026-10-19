package channels

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func TestValidateLogoPath(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "logo.png")
	for _, ok := range []string{"", "  ", "https://example.com/a.png", "http://10.0.0.2:8002/images/x", abs} {
		if err := ValidateLogoPath(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	bad := []string{"logo.png", "ftp://x/a.png", filepath.Join(t.TempDir(), "secrets.txt"), filepath.Join(t.TempDir(), "noext")}
	for _, b := range bad {
		if err := ValidateLogoPath(b); err == nil {
			t.Errorf("%q should be invalid", b)
		}
	}
}

// newLogoServer opens a SQLite channels table (the slot-0081 columns the
// repo reads) and mounts the channel routes.
func newLogoServer(t *testing.T) (*sql.DB, http.Handler) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// One connection: concurrent handler calls in tests would otherwise
	// race SQLite's file lock (SQLITE_BUSY) instead of queueing.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE channels (
		id TEXT PRIMARY KEY, library_id TEXT, number INTEGER NOT NULL,
		name TEXT NOT NULL, slug TEXT NOT NULL, logo_path TEXT,
		category TEXT NOT NULL DEFAULT 'general', mode TEXT NOT NULL,
		mode_config TEXT NOT NULL DEFAULT '{}', source_filter TEXT,
		transition TEXT NOT NULL DEFAULT 'cut', enabled BOOLEAN NOT NULL DEFAULT 1,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE channel_art_overrides (
		channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
		kind TEXT NOT NULL CHECK (kind IN ('logo','watermark','loading')),
		path TEXT, settings TEXT, base TEXT,
		updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
		PRIMARY KEY (channel_id, kind))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE channel_plans (id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL DEFAULT '', planner_version INTEGER,
		input_hash TEXT NOT NULL, plan TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	(&Handler{DB: db}).Mount(r)
	return db, r
}

// planLogoDir makes dir the latest plan's logos folder (dz_logo_dir): an
// art root logos are served from.
func planLogoDir(t *testing.T, db *sql.DB, dir string) {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"config": map[string]any{"dz_logo_dir": dir}, "channels": []any{}})
	if _, err := db.Exec(`INSERT INTO channel_plans (input_hash, plan) VALUES ('roots', $1)`, string(cfg)); err != nil {
		t.Fatal(err)
	}
}

func insertChannel(t *testing.T, db *sql.DB, id string, logo any) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO channels (id, number, name, slug, logo_path, mode, created_at, updated_at)
		VALUES ($1, 1, 'Kids', 'kids', $2, 'shuffle', $3, $3)`, id, logo, now); err != nil {
		t.Fatal(err)
	}
}

func TestLogoServesFileWithCacheHeaders(t *testing.T) {
	db, srv := newLogoServer(t)
	logos := t.TempDir()
	planLogoDir(t, db, logos)
	logo := filepath.Join(logos, "kids.png")
	if err := os.WriteFile(logo, []byte("\x89PNG fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	insertChannel(t, db, "c1", logo)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/c1/logo", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=") {
		t.Errorf("cache-control %q", cc)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || rec.Body.String() != "\x89PNG fake" {
		t.Fatalf("etag %q body %q", etag, rec.Body.String())
	}

	// Revalidation answers 304.
	req := httptest.NewRequest("GET", "/api/channels/c1/logo", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("revalidate code %d", rec.Code)
	}
}

func TestLogoRedirectsURLAndMissing(t *testing.T) {
	db, srv := newLogoServer(t)
	insertChannel(t, db, "remote", "https://cdn.example/kids.png")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/remote/logo", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://cdn.example/kids.png" {
		t.Fatalf("redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}

	if _, err := db.Exec(`UPDATE channels SET id='none', logo_path=NULL WHERE id='remote'`); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/channels/none/logo", "/api/channels/nope/logo"} {
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: code %d", p, rec.Code)
		}
	}
}

func TestLogoRefusesNonImageFile(t *testing.T) {
	db, srv := newLogoServer(t)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(secret, []byte("nope"), 0o600)
	insertChannel(t, db, "c1", secret) // written behind the API's back
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/c1/logo", nil))
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "nope") {
		t.Fatalf("non-image served: %d %q", rec.Code, rec.Body.String())
	}
}

func TestLogoOnlyFromAnArtFolder(t *testing.T) {
	db, srv := newLogoServer(t)
	logos, elsewhere := t.TempDir(), t.TempDir()
	planLogoDir(t, db, logos)
	outside := filepath.Join(elsewhere, "other.png")
	_ = os.WriteFile(outside, []byte("\x89PNG other"), 0o600)
	inside := filepath.Join(logos, "kids.png")
	_ = os.WriteFile(inside, []byte("\x89PNG kids"), 0o600)
	for id, p := range map[string]string{
		"out":    outside, // an image, but not in an art folder
		"dotdot": filepath.Join(logos, "..", filepath.Base(elsewhere), "other.png"),
		"unc":    `\\fileserver\share\logo.png`,
	} {
		insertChannel(t, db, id, p)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/"+id+"/logo", nil))
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "PNG") {
			t.Errorf("%s (%s) served: %d", id, p, rec.Code)
		}
	}
	insertChannel(t, db, "in", inside)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/in/logo", nil))
	if rec.Code != 200 || rec.Body.String() != "\x89PNG kids" {
		t.Fatalf("inside the logos folder: %d %q", rec.Code, rec.Body.String())
	}
}

func TestWithinAndUNC(t *testing.T) {
	root := t.TempDir()
	if got, ok := within(root, filepath.Join(root, "a", "b.png")); !ok || got != filepath.Join(root, "a", "b.png") {
		t.Errorf("inside: %q %v", got, ok)
	}
	for _, p := range []string{root, filepath.Join(root, ".."), filepath.Join(root, "..", "x.png"),
		root + "-sibling/x.png", `\\h\s\x.png`, "//h/s/x.png"} {
		if _, ok := within(root, p); ok {
			t.Errorf("%q accepted", p)
		}
	}
	if _, ok := within(filepath.VolumeName(root)+string(filepath.Separator), filepath.Join(root, "x.png")); ok {
		t.Error("a filesystem root is not an art root")
	}
	for _, p := range []string{`\\host\share\a.png`, "//host/share/a.png", `\\?\C:\a.png`} {
		if !isUNC(p) || ValidateLogoPath(p) == nil {
			t.Errorf("%q must be refused as UNC", p)
		}
	}
}

func TestChannelPathsHiddenFromNonAdmins(t *testing.T) {
	db, srv := newLogoServer(t)
	insertChannel(t, db, "c1", "/srv/art/kids.png")
	if _, err := db.Exec(`UPDATE channels SET mode_config = $1`,
		`{"slots":[],"look":{"loading_path":"/srv/art/loading-kids.png","watermark":{"path":"/srv/art/wm.gif","position":"top-left"}}}`); err != nil {
		t.Fatal(err)
	}
	get := func(p *principal.Principal, target string) string {
		req := httptest.NewRequest("GET", target, nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	viewer := &principal.Principal{UserID: "v", AccessAllLibraries: true}
	for _, target := range []string{"/api/channels/c1", "/api/channels"} {
		body := get(viewer, target)
		if strings.Contains(body, "/srv/art") {
			t.Errorf("%s leaks a path to a non-admin: %s", target, body)
		}
		if !strings.Contains(body, "/api/channels/c1/logo") || !strings.Contains(body, "/api/channels/c1/art/watermark") ||
			!strings.Contains(body, `"position":"top-left"`) {
			t.Errorf("%s lost the servable URLs / settings: %s", target, body)
		}
	}
	admin := &principal.Principal{UserID: "a", IsAdmin: true, AccessAllLibraries: true}
	if body := get(admin, "/api/channels/c1"); !strings.Contains(body, "/srv/art/kids.png") || !strings.Contains(body, "/srv/art/wm.gif") {
		t.Errorf("admin must still see the paths: %s", body)
	}
	// a URL logo is not a server path: it stays (the web app falls back to it)
	if _, err := db.Exec(`UPDATE channels SET logo_path = 'https://cdn.example/k.png'`); err != nil {
		t.Fatal(err)
	}
	if body := get(viewer, "/api/channels/c1"); !strings.Contains(body, "https://cdn.example/k.png") {
		t.Errorf("URL logo dropped: %s", body)
	}
}

func TestGetCarriesLogoURL(t *testing.T) {
	db, srv := newLogoServer(t)
	insertChannel(t, db, "c1", "https://cdn.example/kids.png")
	req := httptest.NewRequest("GET", "/api/channels/c1", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(),
		&principal.Principal{UserID: "u", AccessAllLibraries: true}))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var c Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	if c.LogoURL != "/api/channels/c1/logo" || c.LogoPath == nil {
		t.Fatalf("logo fields: %+v", c)
	}
}

func TestPatchLogoPath(t *testing.T) {
	db, srv := newLogoServer(t)
	insertChannel(t, db, "c1", nil)
	admin := &principal.Principal{UserID: "a", IsAdmin: true, AccessAllLibraries: true}
	patch := func(body string) (int, Channel) {
		req := httptest.NewRequest("PATCH", "/api/channels/c1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(principal.WithPrincipal(req.Context(), admin))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		var c Channel
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return rec.Code, c
	}
	if code, _ := patch(`{"logo_path":"relative.png"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad logo accepted: %d", code)
	}
	code, c := patch(`{"logo_path":"https://cdn.example/k.png"}`)
	if code != 200 || c.LogoURL == "" || c.LogoPath == nil || *c.LogoPath != "https://cdn.example/k.png" {
		t.Fatalf("set: %d %+v", code, c)
	}
	code, c = patch(`{"logo_path":""}`)
	if code != 200 || c.LogoPath != nil || c.LogoURL != "" {
		t.Fatalf("clear: %d %+v", code, c)
	}
}
