package logs

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/serversettings"
)

// Epic 59 story 59.04: an admin's bundle carries the hardware and its tier (machine.json);
// a user's does not.
func TestBundleCarriesTheMachine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/config/machine" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"tier":"light","machine":{"platform":"dsm"}}`)
	}))
	t.Cleanup(srv.Close)
	tf := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tf, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Ring: seedRing(t), Supervisor: &serversettings.Supervisor{URL: srv.URL, TokenFile: tf}}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, adminCtx(httptest.NewRequest(http.MethodGet, "/api/admin/logs/export", nil)))
	names := tarballEntries(t, rec.Body.Bytes())
	var doc map[string]any
	if err := json.Unmarshal(names["machine.json"], &doc); err != nil || doc["tier"] != "light" {
		t.Fatalf("machine.json = %s (%v)", names["machine.json"], err)
	}

	// the supervisor away: the file says so, and the bundle is still made
	srv.Close()
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, adminCtx(httptest.NewRequest(http.MethodGet, "/api/admin/logs/export", nil)))
	names = tarballEntries(t, rec.Body.Bytes())
	if err := json.Unmarshal(names["machine.json"], &doc); err != nil || doc["status"] != "unknown" {
		t.Fatalf("supervisor away: %s", names["machine.json"])
	}
}
