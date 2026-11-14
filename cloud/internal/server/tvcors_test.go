package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
)

// Epic 58 story 58.09: the Samsung and LG launcher calls the cloud's TV code routes from its
// packaged page. Through the api's own router (the user listener, the web allow-list too):
// a TV app's origin is answered on /v1/tv/codes and /v1/tv/codes/poll only, echoed and never
// with credentials; any other origin, and every other route, gets nothing from it.
func TestTVOriginsOnTheTVRoutesOnly(t *testing.T) {
	tvPaths := []string{"/v1/tv/codes", "/v1/tv/codes/poll"}
	r := NewRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, DefaultOrigins(config.Config{}),
		WithUserHosts(nil), WithTVOrigins(tvPaths))
	r.Group(func(app chi.Router) {
		ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }
		app.Post("/v1/tv/codes", ok)
		app.Post("/v1/tv/codes/poll", ok)
		app.Get("/v1/homes", ok)
		app.Post("/v1/auth/login", ok)
		app.Post("/v1/tv/codes/{code}/approve", ok)
	})

	cases := []struct {
		name, method, path, origin string
		wantOrigin                 string // "" = no Access-Control-Allow-Origin
		wantStatus                 int
	}{
		{"tizen preflight", http.MethodOptions, "/v1/tv/codes", "file://", "file://", http.StatusNoContent},
		{"webos preflight", http.MethodOptions, "/v1/tv/codes", "null", "null", http.StatusNoContent},
		{"app scheme preflight", http.MethodOptions, "/v1/tv/codes/poll", "app://maktaba.tv", "app://maktaba.tv", http.StatusNoContent},
		{"tizen create", http.MethodPost, "/v1/tv/codes", "file://", "file://", http.StatusCreated},
		{"webos poll", http.MethodPost, "/v1/tv/codes/poll", "null", "null", http.StatusCreated},
		// a web page that is not on the allow-list
		{"another site's create", http.MethodPost, "/v1/tv/codes", "https://evil.example", "", http.StatusCreated},
		{"another site's preflight", http.MethodOptions, "/v1/tv/codes/poll", "https://evil.example", "", http.StatusNoContent},
		{"a look-alike scheme", http.MethodOptions, "/v1/tv/codes", "file://evil.example", "", http.StatusNoContent},
		// a TV origin elsewhere: the account's routes, and approving from the web
		{"tv origin on homes", http.MethodGet, "/v1/homes", "null", "", http.StatusCreated},
		{"tv origin on login preflight", http.MethodOptions, "/v1/auth/login", "file://", "", http.StatusNoContent},
		{"tv origin on approve", http.MethodPost, "/v1/tv/codes/ABCD2345/approve", "null", "", http.StatusCreated},
		// the web app keeps its own answer
		{"the web app", http.MethodPost, "/v1/tv/codes", "https://app.maktaba.tv", "https://app.maktaba.tv", http.StatusCreated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, strings.NewReader("{}"))
			req.Header.Set("Origin", c.origin)
			if c.method == http.MethodOptions {
				req.Header.Set("Access-Control-Request-Method", "POST")
				req.Header.Set("Access-Control-Request-Headers", "content-type")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != c.wantStatus {
				t.Fatalf("status %d, want %d", rec.Code, c.wantStatus)
			}
			h := rec.Header()
			if got := h.Get("Access-Control-Allow-Origin"); got != c.wantOrigin {
				t.Fatalf("Access-Control-Allow-Origin %q, want %q", got, c.wantOrigin)
			}
			if c.wantOrigin == "" || c.origin == "https://app.maktaba.tv" {
				return
			}
			if h.Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("a TV app's origin was allowed credentials")
			}
			if !strings.Contains(h.Get("Vary"), "Origin") {
				t.Fatalf("Vary %q lacks Origin", h.Get("Vary"))
			}
			if c.method == http.MethodOptions {
				if m := h.Get("Access-Control-Allow-Methods"); m != "POST, OPTIONS" {
					t.Fatalf("methods %q", m)
				}
				if a := h.Get("Access-Control-Allow-Headers"); strings.Contains(strings.ToLower(a), "authorization") || !strings.Contains(a, "Content-Type") {
					t.Fatalf("headers %q", a)
				}
			}
		})
	}
}

// Without the option (the relay, the admin listener) a TV origin gets nothing anywhere.
func TestNoTVOriginsWithoutTheOption(t *testing.T) {
	r := NewRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, nil)
	r.Post("/v1/tv/codes", func(w http.ResponseWriter, _ *http.Request) {})
	req := httptest.NewRequest(http.MethodOptions, "/v1/tv/codes", nil)
	req.Header.Set("Origin", "null")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin %q without WithTVOrigins", got)
	}
}
