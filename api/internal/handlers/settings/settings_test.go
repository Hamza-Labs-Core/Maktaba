package settings

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/go-chi/chi/v5"
)

func TestRedactSecrets(t *testing.T) {
	in := map[string]any{
		"stt.openai.api_key": "sk-xxx",
		"normal.value":       42,
		"some.password":      "pw",
		"empty.token":        "",
	}
	out := redactSecrets(in)
	if out["stt.openai.api_key"] != "<redacted>" || out["stt.openai.api_key_present"] != true {
		t.Errorf("api_key not redacted: %v", out)
	}
	if out["normal.value"].(int) != 42 {
		t.Errorf("non-secret was modified")
	}
	if out["some.password"] != "<redacted>" {
		t.Errorf("password not redacted")
	}
	if out["empty.token_present"] != false {
		t.Errorf("empty token should be reported absent")
	}
}

func TestRedactSecrets_NeverEmitsValueLooking(t *testing.T) {
	in := map[string]any{
		"foo.token":  "shhh-1234",
		"bar.secret": "moresecret",
	}
	out := redactSecrets(in)
	for _, v := range out {
		if s, ok := v.(string); ok {
			if strings.Contains(s, "shhh") || strings.Contains(s, "moresecret") {
				t.Errorf("leaked secret in %q", s)
			}
		}
	}
}

// Every settings route is the home admins': reading the server's settings and its
// speech backends, and the speech test (which spends the cloud budget), as much as saving
// (the API enforces roles). Nothing reaches the database or the pipeline for anyone else.
func TestEveryRouteIsAdminOnly(t *testing.T) {
	h := &Handler{} // no DB, no pipeline: a refused call never gets that far
	r := chi.NewRouter()
	h.Mount(r)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/settings", ""},
		{http.MethodPatch, "/api/settings", `{"search.fts_weight": 2}`},
		{http.MethodGet, "/api/settings/stt-backends", ""},
		{http.MethodPost, "/api/settings/stt-test", `{"backend":"openai"}`},
	} {
		for _, p := range []*principal.Principal{nil, {UserID: "viewer"}, {UserID: "all", AccessAllLibraries: true}} {
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			if p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), p))
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %+v: %d, want 403", c.method, c.path, p, rec.Code)
			}
		}
	}
}
