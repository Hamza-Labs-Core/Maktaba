package videos

// Epic 48 story 48.5 (G4): GET /api/videos/{id}/poster?size=small serves the
// pipeline's small copy, else the full image, behind the same library guard.

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func writePosterFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// posterRouter is the videos router over a scratch SQLite videos table
// holding rows of (id, library_id, content_hash, poster_path).
func posterRouter(t *testing.T, rows [][]any) http.Handler {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, content_hash TEXT, poster_path TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO videos VALUES ($1,$2,$3,$4)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	(&Handler{DB: db}).Mount(r)
	return r
}

func getPoster(r http.Handler, p *principal.Principal, id, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/videos/"+id+"/poster"+query, nil)
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestPosterSizes(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	t.Setenv("MAKTABA_THUMBNAIL_ROOT", cache)
	// 1: poster and small copy in the cache; 2: its own poster, no small
	// copy; 3: hash pending, the import's staged small copy; 4: a hash that
	// climbs out of the cache; 5: no image at all
	writePosterFile(t, filepath.Join(cache, "h1", "poster.jpg"), "full-1")
	writePosterFile(t, filepath.Join(cache, "h1", "small.jpg"), "small-1")
	own := filepath.Join(root, "media", "poster.jpg")
	writePosterFile(t, own, "own-2")
	writePosterFile(t, filepath.Join(cache, "imported", xvid(3)+".jpg"), "staged-3")
	writePosterFile(t, filepath.Join(root, "secret", "small.jpg"), "secret")
	r := posterRouter(t, [][]any{
		{xvid(1), extLibA, "h1", nil},
		{xvid(2), extLibA, "h2", own},
		{xvid(3), extLibA, "", nil},
		{xvid(4), extLibA, "../secret", nil},
		{xvid(5), extLibA, "h5", nil},
	})
	const small, full = "?size=small", ""

	for _, c := range []struct {
		name  string
		n     int
		query string
		want  string
	}{
		{"small copy", 1, small, "small-1"},
		{"full image", 1, full, "full-1"},
		{"full image, asked for by name", 1, "?size=full", "full-1"},
		{"no small copy: the video's own poster", 2, small, "own-2"},
		{"own poster, full", 2, full, "own-2"},
		{"hash pending: the staged small copy", 3, small, "staged-3"},
		{"hash pending, full", 3, full, "staged-3"},
	} {
		rec := getPoster(r, viewerA(), xvid(c.n), c.query)
		if rec.Code != http.StatusOK || rec.Body.String() != c.want {
			t.Errorf("%s: %d %q, want 200 %q", c.name, rec.Code, rec.Body.String(), c.want)
		}
		// both sizes are cacheable: a grid's repeat visit fetches nothing
		if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
			t.Errorf("%s: Cache-Control %q", c.name, cc)
		}
	}
	for _, q := range []string{small, full} {
		if rec := getPoster(r, viewerA(), xvid(4), q); rec.Code != http.StatusNotFound {
			t.Errorf("a hash out of the cache, %q: %d %q", q, rec.Code, rec.Body.String())
		}
		if rec := getPoster(r, viewerA(), xvid(5), q); rec.Code != http.StatusNotFound {
			t.Errorf("no image, %q: %d", q, rec.Code)
		}
	}
	if rec := getPoster(r, viewerA(), xvid(1), "?size=huge"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown size: %d", rec.Code)
	}
}

// Role test (48.5): a viewer without the library gets 404 for both sizes,
// exactly as for a video that does not exist; the admin and a viewer of the
// library get the image; no one signed in is refused.
func TestPosterSizesFollowLibraryAccess(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cache")
	t.Setenv("MAKTABA_THUMBNAIL_ROOT", cache)
	writePosterFile(t, filepath.Join(cache, "hb", "poster.jpg"), "full-b")
	writePosterFile(t, filepath.Join(cache, "hb", "small.jpg"), "small-b")
	r := posterRouter(t, [][]any{{xvid(1), extLibB, "hb", nil}})
	viewerB := &principal.Principal{UserID: extUser, Libraries: []string{extLibB}}
	missing := "00000000-0000-4000-8000-0000000000ff"

	for _, c := range []struct{ query, want string }{{"?size=small", "small-b"}, {"", "full-b"}} {
		denied := getPoster(r, viewerA(), xvid(1), c.query)
		absent := getPoster(r, viewerA(), missing, c.query)
		if denied.Code != http.StatusNotFound || absent.Code != http.StatusNotFound {
			t.Errorf("viewer of A, %q: %d (a missing video: %d), want 404", c.query, denied.Code, absent.Code)
		}
		if strings.Contains(denied.Body.String(), c.want) {
			t.Errorf("viewer of A, %q: the image leaked", c.query)
		}
		for who, p := range map[string]*principal.Principal{"admin": admin(), "viewer of B": viewerB} {
			if rec := getPoster(r, p, xvid(1), c.query); rec.Code != http.StatusOK || rec.Body.String() != c.want {
				t.Errorf("%s, %q: %d %q, want 200 %q", who, c.query, rec.Code, rec.Body.String(), c.want)
			}
		}
		if rec := getPoster(r, nil, xvid(1), c.query); rec.Code != http.StatusForbidden {
			t.Errorf("anonymous, %q: %d, want 403", c.query, rec.Code)
		}
	}
}
