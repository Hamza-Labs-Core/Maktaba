package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	tvchangeapi "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// Story 35.6 (review H1): the home's supervisor asks its own API for the update's TV change
// with its per-boot local token alone, through the real security stack: the global gate
// lets that token through on these routes (and only with it), never a session's absence.
func TestApplySecurity_SupervisorReachesTheTVChange(t *testing.T) {
	priv, pub := mustPEMPair(t)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", priv)
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pub)
	t.Setenv("MAKTABA_ADMIN_TOKEN", "")
	t.Setenv("MAKTABA_CORS_ALLOWED_ORIGINS", "")
	t.Setenv("MAKTABA_HSTS", "")
	tok := filepath.Join(t.TempDir(), "local.token")
	if err := os.WriteFile(tok, []byte("t0k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", tok)
	st, err := initAuth(noopLogger())
	if err != nil {
		t.Fatalf("initAuth: %v", err)
	}

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
	ctl := tvchange.NewController(db, nil)
	ctl.Enqueue = func(context.Context, string, map[string]any, string) error { return nil }
	r := chi.NewRouter()
	tvchangeapi.New(db, ctl).Mount(r)
	stack := st.applySecurity(r, nil, nil, nil)

	call := func(method, path, token, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("X-Maktaba-Local-Token", token)
		}
		rec := httptest.NewRecorder()
		stack.ServeHTTP(rec, req)
		return rec.Code
	}
	begin := `{"run_id":"run-1","version":"v1.5.0","sha256":"` + strings.Repeat("ab", 32) + `"}`
	for _, c := range []struct {
		method, path, token, body string
		want                      int
	}{
		{http.MethodPost, "/internal/updates/tv-change", "", begin, http.StatusUnauthorized},
		{http.MethodPost, "/internal/updates/tv-change", "nope", begin, http.StatusUnauthorized},
		{http.MethodPost, "/internal/updates/tv-change", "t0k", begin, http.StatusOK},
		{http.MethodGet, "/internal/updates/tv-change/run-1", "", "", http.StatusUnauthorized},
		{http.MethodGet, "/internal/updates/tv-change/run-1", "t0k", "", http.StatusOK},
		{http.MethodPost, "/internal/updates/tv-change/run-1/withdraw", "", `{"reason":"x"}`, http.StatusUnauthorized},
		{http.MethodPost, "/internal/updates/tv-change/run-1/withdraw", "t0k", `{"reason":"watch_timeout"}`, http.StatusOK},
		// the token opens these routes alone, not the business surface
		{http.MethodGet, "/api/tv/changes", "t0k", "", http.StatusUnauthorized},
	} {
		if got := call(c.method, c.path, c.token, c.body); got != c.want {
			t.Errorf("%s %s (token %q): %d, want %d", c.method, c.path, c.token, got, c.want)
		}
	}
}
