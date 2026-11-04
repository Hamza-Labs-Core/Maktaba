package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// T12: with livetv off, /api/tv/changes answers as the caller's role allows first: no
// one 401, a viewer 403, a cloud-linked play sign-in (even an admin's) 403, an admin 409
// component_off. Nothing reaches the database.
func TestTVRoutesAnswerComponentOffAfterTheRoleCheck(t *testing.T) {
	db, err := sql.Open("postgres", "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	loops := runner.New(runner.Options{Host: "api", Logger: slog.Default()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer loops.Run(ctx, runner.Source{})
	loops.Apply(map[string]store.Desired{"livetv": {ID: "livetv", Want: components.WantInstalled, Generation: 3}})

	r := chi.NewRouter()
	MountP33(r, P33Deps{DB: db, Logger: slog.Default(), Loops: loops})
	play := &principal.Principal{UserID: "00000000-0000-0000-0000-000000000001", IsAdmin: true, AccessAllLibraries: true}
	play.ApplyCloudScope(principal.CloudScopePlay, nil)
	for _, c := range []struct {
		who  string
		p    *principal.Principal
		want int
	}{
		{"no one", nil, http.StatusUnauthorized},
		{"viewer", &principal.Principal{UserID: "00000000-0000-0000-0000-000000000002"}, http.StatusForbidden},
		{"cloud play", play, http.StatusForbidden},
		{"admin", &principal.Principal{UserID: "00000000-0000-0000-0000-000000000003", IsAdmin: true}, http.StatusConflict},
	} {
		req := httptest.NewRequest("GET", "/api/tv/changes", nil)
		if c.p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), c.p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.who, rec.Code, c.want, rec.Body.String())
			continue
		}
		if c.want == http.StatusConflict {
			var body map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["code"] != "component_off" || body["component"] != "livetv" {
				t.Errorf("admin: %v", body)
			}
		}
	}
	// livetv on again: the admin reaches the handler (which needs the database it lacks)
	loops.Apply(map[string]store.Desired{"livetv": {ID: "livetv", Want: components.WantEnabled, Generation: 4}})
	req := httptest.NewRequest("GET", "/api/tv/changes", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "00000000-0000-0000-0000-000000000003", IsAdmin: true}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusConflict {
		t.Fatalf("livetv on: still 409")
	}
}
