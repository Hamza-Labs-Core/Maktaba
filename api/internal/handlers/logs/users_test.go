package logs

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
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Story 53.11: an admin gets each user's user_h with their name; a viewer gets 403.
func TestLogUsersMapsHashesToNames(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL);
		INSERT INTO users VALUES ('u-1', 'sara'), ('u-2', 'omar')`); err != nil {
		t.Fatal(err)
	}
	mlog.SetUserHashKey([]byte("test-key"))
	t.Cleanup(func() { mlog.SetUserHashKey(nil) })
	h := &Handler{DB: db}
	r := chi.NewRouter()
	h.Mount(r)
	call := func(p *principal.Principal) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/logs/users", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(&principal.Principal{UserID: "v"}); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer: %d", rec.Code)
	}
	rec := call(&principal.Principal{UserID: "a", IsAdmin: true})
	var out struct{ Users map[string]string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Users[mlog.HashUser("u-1")] != "sara" || out.Users[mlog.HashUser("u-2")] != "omar" || len(out.Users) != 2 {
		t.Fatalf("users: %d %s", rec.Code, rec.Body)
	}
}
