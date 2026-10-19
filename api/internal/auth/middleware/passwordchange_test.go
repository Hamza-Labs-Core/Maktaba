package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func TestRequireAuthExcept_MustChangePassword(t *testing.T) {
	gate := RequireAuthExcept(DefaultPublicAllowlist())
	h := gate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	cases := []struct {
		method, path string
		must         bool
		want         int
	}{
		{http.MethodGet, "/api/videos", true, http.StatusForbidden},
		{http.MethodPost, "/api/stream/sessions", true, http.StatusForbidden},
		{http.MethodPatch, "/api/me", true, http.StatusForbidden},
		{http.MethodGet, "/api/me/change-password", true, http.StatusForbidden},
		{http.MethodPost, "/api/me/change-password", true, http.StatusOK},
		{http.MethodGet, "/api/me", true, http.StatusOK},
		{http.MethodPost, "/api/auth/logout", true, http.StatusOK},
		{http.MethodGet, "/api/system/version", true, http.StatusOK},
		{http.MethodGet, "/", true, http.StatusOK},
		{http.MethodGet, "/api/videos", false, http.StatusOK},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		p := &principal.Principal{UserID: "u1", Source: principal.SourceCookie, MustChangePassword: c.must}
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s must=%v: status %d, want %d", c.method, c.path, c.must, rec.Code, c.want)
		}
		if rec.Code == http.StatusForbidden {
			var body map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["type"] != TypePasswordChangeRequired {
				t.Errorf("%s %s: problem type %v", c.method, c.path, body["type"])
			}
		}
	}
}
