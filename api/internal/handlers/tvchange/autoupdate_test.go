package tvchange

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	core "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// Story 35.6: the update's TV change is this machine's supervisor's to ask for (its
// per-boot local token); anyone else, an admin's session included, is refused.
func TestAutoUpdateRoutesAreTheSupervisors(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("t0k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", tok)
	db := tvchangetest.SQLite(t)
	live := uuid.NewString()
	for _, q := range []string{
		`INSERT INTO tv_releases (id, digest, sched_gen, streaming_version) VALUES ('` + live + `', 'sha256:live', 0, 'v1.4.0')`,
		`INSERT INTO tv_routing (scope, live_release_id) VALUES ('cluster', '` + live + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ctl := core.NewController(db, nil)
	ctl.Enqueue = func(context.Context, string, map[string]any, string) error { return nil }
	r := chi.NewRouter()
	New(db, ctl).Mount(r)
	call := func(method, path, token, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("X-Maktaba-Local-Token", token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	body := `{"run_id":"run-1","version":"v1.5.0","sha256":"` + strings.Repeat("ab", 32) + `"}`
	if code, _ := call(http.MethodPost, "/internal/updates/tv-change", "", body); code != http.StatusForbidden {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := call(http.MethodPost, "/internal/updates/tv-change", "nope", body); code != http.StatusForbidden {
		t.Fatalf("a wrong token: %d", code)
	}
	code, st := call(http.MethodPost, "/internal/updates/tv-change", "t0k", body)
	if code != http.StatusOK || st["outcome"] != core.AutoOutcomeQueued || st["change_id"] == "" {
		t.Fatalf("begin: %d %v", code, st)
	}
	if code, got := call(http.MethodGet, "/internal/updates/tv-change/run-1", "t0k", ""); code != http.StatusOK || got["change_id"] != st["change_id"] {
		t.Fatalf("status: %d %v", code, got)
	}
	code, got := call(http.MethodPost, "/internal/updates/tv-change/run-1/withdraw", "t0k", `{"reason":"watch_timeout"}`)
	if code != http.StatusOK || got["outcome"] != core.AutoOutcomeCancelled || got["withdrawn"] != "watch_timeout" {
		t.Fatalf("withdraw: %d %v", code, got)
	}
	if code, _ := call(http.MethodPost, "/internal/updates/tv-change", "t0k", `{"run_id":"r","version":"x","sha256":"y"}`); code != http.StatusConflict {
		t.Fatalf("a bad request: %d", code)
	}
}
