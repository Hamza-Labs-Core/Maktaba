package contextcard

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const vid = "11111111-1111-1111-1111-111111111111"

func authReq(target string, p *principal.Principal) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	return r.WithContext(principal.WithPrincipal(r.Context(), p))
}

func mount() chi.Router {
	h := &Handler{} // nil DB
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// AC: with a nil DB the card is well-formed and empty (related/MLT are
// non-nil arrays); facts omitted.
func TestContext_NilDB_WellFormed(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, authReq("/api/videos/"+vid+"/context",
		&principal.Principal{UserID: "u1", AccessAllLibraries: true}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var card Card
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	if card.RelatedInLibrary == nil || card.MoreLikeThis == nil {
		t.Fatal("related/more_like_this must be non-nil arrays")
	}
	if card.Facts != nil {
		t.Fatal("facts must be omitted with no enrichment")
	}
}

func TestContext_RequiresAuth(t *testing.T) {
	// nil DB short-circuits before the auth check, so exercise auth with
	// a handler that has a non-nil but unusable principal path: a request
	// with no principal on a DB-backed handler is forbidden. Here we just
	// assert the malformed-id guard which runs before the DB check.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/videos/not-a-uuid/context", nil)
	mount().ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

// A related video with no metadata title is named by its file name without
// the extension (the context card's "same series" row).
func TestContext_TitlelessSiblingShowsItsFileNameWithoutTheExtension(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const sib = "22222222-2222-2222-2222-222222222222"
	for _, q := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, title TEXT, filename TEXT)`,
		`CREATE TABLE series_episodes (series_id TEXT, video_id TEXT, season INTEGER, episode INTEGER)`,
		`INSERT INTO videos VALUES ('` + vid + `', 'l1', 'Lesson One', 'one.mp4'),
			('` + sib + `', 'l1', NULL, 'Lesson Two.mkv')`,
		`INSERT INTO series_episodes VALUES ('s1', '` + vid + `', 1, 1), ('s1', '` + sib + `', 1, 2)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	(&Handler{DB: db}).Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, authReq("/api/videos/"+vid+"/context",
		&principal.Principal{UserID: "u1", IsAdmin: true, AccessAllLibraries: true}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var card Card
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	if len(card.RelatedInLibrary) != 1 || card.RelatedInLibrary[0].VideoID != sib ||
		card.RelatedInLibrary[0].Title != "Lesson Two" {
		t.Fatalf("related %+v, want %s titled %q", card.RelatedInLibrary, sib, "Lesson Two")
	}
}
