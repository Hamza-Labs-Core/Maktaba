package videos

// Epic 45 Phase 3 (story 45.19): GET /api/videos/{id}/trickplay and its
// sheet, for a sheet with an index (found in the thumbnail cache), an older
// one without (found by sprite_path), none, and another library's video.

import (
	"database/sql"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func writeSheet(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
}

func TestTrickplay(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MAKTABA_THUMBNAIL_ROOT", filepath.Join(root, "cache"))
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, content_hash TEXT,
		sprite_path TEXT, duration_sec REAL)`); err != nil {
		t.Fatal(err)
	}
	// 1: a new 10x10 sheet with its index, in the cache (its sprite_path is
	// another machine's); 2: an old 5x5 sheet by sprite_path, no index;
	// 3: no sheet; 4: an old sheet, length unknown; 5: library B; 6: a hash
	// that climbs out of the cache
	writeSheet(t, filepath.Join(root, "cache", "h1", "sprite.jpg"), 1600, 900)
	idx := `{"version":1,"columns":10,"rows":10,"count":100,"interval_sec":36,"tile_width":160}`
	if err := os.WriteFile(filepath.Join(root, "cache", "h1", "sprite.json"), []byte(idx), 0o644); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, "old", "sprite.jpg")
	writeSheet(t, old, 800, 450)
	writeSheet(t, filepath.Join(root, "secret", "sprite.jpg"), 100, 100)
	for _, row := range [][]any{
		{xvid(1), extLibA, "h1", "/elsewhere/sprite.jpg", 3600.0},
		{xvid(2), extLibA, "h2", old, 500.0},
		{xvid(3), extLibA, "h3", nil, 100.0},
		{xvid(4), extLibA, "h4", old, nil},
		{xvid(5), extLibB, "h1", nil, 3600.0},
		{xvid(6), extLibA, "../secret", nil, 100.0},
	} {
		if _, err := db.Exec(`INSERT INTO videos VALUES ($1,$2,$3,$4,$5)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	(&Handler{DB: db}).Mount(r)
	get := func(p *principal.Principal, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	index := func(n int) (Trickplay, int) {
		rec := get(viewerA(), "/api/videos/"+xvid(n)+"/trickplay")
		var tp Trickplay
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &tp); err != nil {
				t.Fatal(err)
			}
		}
		return tp, rec.Code
	}

	tp, code := index(1)
	want := Trickplay{SpriteURL: "/api/videos/" + xvid(1) + "/trickplay/sprite", Columns: 10, Rows: 10, Count: 100,
		IntervalSec: 36, TileWidth: 160, TileHeight: 90, SheetWidth: 1600, SheetHeight: 900}
	if code != http.StatusOK || tp != want {
		t.Errorf("indexed sheet: %d %+v, want %+v", code, tp, want)
	}
	tp, code = index(2)
	want = Trickplay{SpriteURL: "/api/videos/" + xvid(2) + "/trickplay/sprite", Columns: 5, Rows: 5, Count: 25,
		IntervalSec: 20, TileWidth: 160, TileHeight: 90, SheetWidth: 800, SheetHeight: 450}
	if code != http.StatusOK || tp != want {
		t.Errorf("old sheet: %d %+v, want %+v", code, tp, want)
	}
	for _, n := range []int{3, 4, 6} {
		if _, code := index(n); code != http.StatusNotFound {
			t.Errorf("video %d: %d, want 404", n, code)
		}
	}
	if rec := get(viewerA(), "/api/videos/"+xvid(6)+"/trickplay/sprite"); rec.Code != http.StatusNotFound {
		t.Errorf("a hash out of the cache served %d", rec.Code)
	}

	rec := get(viewerA(), "/api/videos/"+xvid(1)+"/trickplay/sprite")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Body.Len() == 0 {
		t.Errorf("sheet: %d %q %d bytes", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	// another library's video, and no one signed in
	for _, path := range []string{"/api/videos/" + xvid(5) + "/trickplay", "/api/videos/" + xvid(5) + "/trickplay/sprite"} {
		if rec := get(viewerA(), path); rec.Code != http.StatusForbidden {
			t.Errorf("viewer of A, %s: %d, want 403", path, rec.Code)
		}
		if rec := get(nil, path); rec.Code != http.StatusForbidden {
			t.Errorf("anonymous, %s: %d, want 403", path, rec.Code)
		}
	}
	if rec := get(admin(), "/api/videos/"+xvid(5)+"/trickplay"); rec.Code != http.StatusOK {
		t.Errorf("admin, library B: %d", rec.Code)
	}
	if rec := get(viewerA(), "/api/videos/nope/trickplay"); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed id: %d", rec.Code)
	}
}
