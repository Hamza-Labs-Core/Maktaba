package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// The activity feed names what was watched (the timeline links and labels
// each row), and merges watches with searches newest first.
func TestActivityCarriesVideoTitle(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, s := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, title TEXT, filename TEXT)`,
		`CREATE TABLE watch_sessions (video_id TEXT, user_id TEXT, started_at TIMESTAMP, percent_complete REAL)`,
		`CREATE TABLE search_history (user_id TEXT, query TEXT, last_used_at TIMESTAMP)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO videos VALUES ('v1', 'Tafsir 1', 'a.mp4'), ('v2', NULL, 'b.mp4')`)
	mustExec(`INSERT INTO watch_sessions VALUES ('v1','u1',$1,40), ('v2','u1',$2,100), ('v1','other',$3,10)`,
		t0, t0.Add(2*time.Hour), t0.Add(3*time.Hour))
	mustExec(`INSERT INTO search_history VALUES ('u1','fiqh',$1)`, t0.Add(time.Hour))

	r := chi.NewRouter()
	(&Handler{DB: db}).Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/api/me/activity", nil)
	req = req.WithContext(principal.WithPrincipal(context.Background(), user()))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			Kind string         `json:"kind"`
			Meta map[string]any `json:"meta"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 3 {
		t.Fatalf("got %d items, want 3 (the other user's watch is not theirs): %s", len(body.Items), rec.Body.String())
	}
	want := []struct{ kind, label string }{{"watched", "b.mp4"}, {"searched", "fiqh"}, {"watched", "Tafsir 1"}}
	for i, w := range want {
		it := body.Items[i]
		label, _ := it.Meta["title"].(string)
		if it.Kind == "searched" {
			label, _ = it.Meta["query"].(string)
		}
		if it.Kind != w.kind || label != w.label {
			t.Errorf("item %d = %s %q, want %s %q", i, it.Kind, label, w.kind, w.label)
		}
	}
}
