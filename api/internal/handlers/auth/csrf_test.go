package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// okHandler is the protected handler the CSRF guard wraps. It records
// whether the request reached it.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

// E10-CSRF-1: a safe method (GET/HEAD/OPTIONS) is exempt from the CSRF
// double-submit check even with no token at all.
func TestCSRF_SafeMethodsExempt(t *testing.T) {
	h := &Handler{}
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		reached := false
		req := httptest.NewRequest(m, "/api/libraries", nil)
		rec := httptest.NewRecorder()
		h.CSRF(okHandler(&reached)).ServeHTTP(rec, req)
		if !reached || rec.Code != http.StatusOK {
			t.Fatalf("%s: safe method must pass; code=%d reached=%v", m, rec.Code, reached)
		}
	}
}

// E10-CSRF-2: a state-changing request authenticated by a cookie
// principal with NO X-Maktaba-CSRF header is rejected 403 csrf-mismatch.
func TestCSRF_CookiePrincipalMissingHeaderRejected(t *testing.T) {
	h := &Handler{}
	reached := false
	req := httptest.NewRequest(http.MethodPost, "/api/libraries", nil)
	req.AddCookie(&http.Cookie{Name: CookieCSRF, Value: "tok-abc"})
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
		UserID: "u1", Source: principal.SourceCookie,
	}))
	rec := httptest.NewRecorder()
	h.CSRF(okHandler(&reached)).ServeHTTP(rec, req)
	if reached {
		t.Fatal("handler must not be reached on CSRF failure")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "csrf-mismatch") {
		t.Fatalf("body = %s, want csrf-mismatch type", rec.Body.String())
	}
}

// E10-CSRF-3: matching double-submit token (cookie == header) passes.
func TestCSRF_CookiePrincipalMatchingTokenPasses(t *testing.T) {
	h := &Handler{}
	reached := false
	req := httptest.NewRequest(http.MethodPost, "/api/libraries", nil)
	req.AddCookie(&http.Cookie{Name: CookieCSRF, Value: "tok-abc"})
	req.Header.Set("X-Maktaba-CSRF", "tok-abc")
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
		UserID: "u1", Source: principal.SourceCookie,
	}))
	rec := httptest.NewRecorder()
	h.CSRF(okHandler(&reached)).ServeHTTP(rec, req)
	if !reached || rec.Code != http.StatusOK {
		t.Fatalf("matching token must pass; code=%d reached=%v", rec.Code, reached)
	}
}

// E10-CSRF-4: mismatched token (cookie != header) is rejected 403.
func TestCSRF_CookiePrincipalMismatchedTokenRejected(t *testing.T) {
	h := &Handler{}
	reached := false
	req := httptest.NewRequest(http.MethodPost, "/api/libraries", nil)
	req.AddCookie(&http.Cookie{Name: CookieCSRF, Value: "tok-abc"})
	req.Header.Set("X-Maktaba-CSRF", "tok-WRONG")
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
		UserID: "u1", Source: principal.SourceCookie,
	}))
	rec := httptest.NewRecorder()
	h.CSRF(okHandler(&reached)).ServeHTTP(rec, req)
	if reached || rec.Code != http.StatusForbidden {
		t.Fatalf("mismatched token must 403; code=%d reached=%v", rec.Code, reached)
	}
}

// E10-CSRF-5: a bearer/JWT principal (not cookie-sourced) skips the
// CSRF check entirely (Story 10.10 AC-3) — API clients carry no cookie.
func TestCSRF_BearerPrincipalSkipsCheck(t *testing.T) {
	h := &Handler{}
	for _, src := range []principal.Source{principal.SourceJWT, principal.SourceAdminToken} {
		reached := false
		req := httptest.NewRequest(http.MethodPost, "/api/libraries", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
			UserID: "u1", Source: src,
		}))
		rec := httptest.NewRecorder()
		h.CSRF(okHandler(&reached)).ServeHTTP(rec, req)
		if !reached || rec.Code != http.StatusOK {
			t.Fatalf("source %s must skip CSRF; code=%d reached=%v", src, rec.Code, reached)
		}
	}
}

// E10-CSRF-6: an anonymous state-changing request (no principal) is
// NOT blocked by the CSRF guard — the downstream RequireAuth gate owns
// the 401. The CSRF guard only defends authenticated cookie sessions.
func TestCSRF_AnonymousPassesThroughToAuthGate(t *testing.T) {
	h := &Handler{}
	reached := false
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	rec := httptest.NewRecorder()
	h.CSRF(okHandler(&reached)).ServeHTTP(rec, req)
	if !reached {
		t.Fatalf("anonymous request must pass through CSRF guard to the auth gate; code=%d", rec.Code)
	}
}

// E10-CSRF-7: a cookie principal whose request also carries a bearer
// Authorization header is treated as an API client and skips CSRF —
// double-submit only protects pure-cookie browser requests.
func TestCSRF_CookiePrincipalWithBearerHeaderSkips(t *testing.T) {
	h := &Handler{}
	reached := false
	req := httptest.NewRequest(http.MethodPost, "/api/libraries", nil)
	req.Header.Set("Authorization", "Bearer something")
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
		UserID: "u1", Source: principal.SourceCookie,
	}))
	rec := httptest.NewRecorder()
	h.CSRF(okHandler(&reached)).ServeHTTP(rec, req)
	if !reached || rec.Code != http.StatusOK {
		t.Fatalf("bearer-bearing request must skip CSRF; code=%d reached=%v", rec.Code, reached)
	}
}
