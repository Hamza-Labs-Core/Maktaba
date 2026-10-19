package channels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// A channel created, edited, renumbered or deleted by hand tells
// OnLineupChange (the router points it at the Plex Live TV refresh); a
// refused request does not.
func TestLineupChangeHookFollowsHandEdits(t *testing.T) {
	db, _ := newLogoServer(t)
	calls := 0
	r := chi.NewRouter()
	(&Handler{DB: db, OnLineupChange: func(context.Context) { calls++ }}).Mount(r)
	send := func(p *principal.Principal, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if _, err := db.Exec(`INSERT INTO channels (id, number, name, slug, mode, created_at, updated_at)
		VALUES ('11111111-1111-1111-1111-111111111111', 5, 'Five', 'five', 'shuffle', $1, $1)`,
		time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-1111-1111-111111111111"

	user := &principal.Principal{UserID: "u", AccessAllLibraries: true}
	if rec := send(user, "DELETE", "/api/channels/"+id, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("user delete: %d", rec.Code)
	}
	if calls != 0 {
		t.Fatalf("a refused request must not notify: %d", calls)
	}
	if rec := send(artAdmin, "PATCH", "/api/channels/"+id, `{"number":6}`); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if calls != 1 {
		t.Fatalf("after patch: %d", calls)
	}
	if rec := send(artAdmin, "POST", "/api/channels/reorder", `[{"id":"`+id+`","number":7}]`); rec.Code != http.StatusOK {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body)
	}
	if rec := send(artAdmin, "DELETE", "/api/channels/"+id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if calls != 3 {
		t.Fatalf("after reorder + delete: %d", calls)
	}
}
