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
	r := chi.NewRouter()
	(&Handler{DB: db}).Mount(r)
	return db, r
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
	logo := filepath.Join(t.TempDir(), "kids.png")
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
