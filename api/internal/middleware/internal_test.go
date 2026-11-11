package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setLocalToken(t *testing.T, tok string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "local.token")
	if err := os.WriteFile(f, []byte(tok), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", f)
}

// The internal budget holds on its own: the home's own callers are bounded, by a bucket of
// theirs, and a refusal says which limit it was.
func TestInternalCallersOwnBudget(t *testing.T) {
	setLocalToken(t, "tok-0123456789abcdef0123456789abcdef")
	var marked bool
	h := InternalCallers(3)(PerIP(1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marked = InternalCaller(r)
		w.WriteHeader(http.StatusNoContent)
	})))
	codes := []int{}
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set(LocalTokenHeader, "tok-0123456789abcdef0123456789abcdef")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
		if rec.Code == http.StatusTooManyRequests && rec.Header().Get("Retry-After") == "" {
			t.Error("429 without Retry-After")
		}
	}
	// PerIP(1) would have refused the second; the internal budget of 3 refuses the fourth
	want := []int{204, 204, 204, 429, 429}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes %v, want %v", codes, want)
		}
	}
	if !marked {
		t.Fatal("the home's own call was not marked")
	}
}

// Unlimited (0 or less) still marks; a request without the right token is never marked.
func TestInternalCallersMarkOnlyWithToken(t *testing.T) {
	setLocalToken(t, "tok-0123456789abcdef0123456789abcdef")
	var marked bool
	h := InternalCallers(-1)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { marked = InternalCaller(r) }))
	for _, c := range []struct {
		tok  string
		want bool
	}{
		{"tok-0123456789abcdef0123456789abcdef", true},
		{"tok-0123456789abcdef0123456789abcdeX", false},
		{"tok-0123456789abcdef", false},
		{"", false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "127.0.0.1:1"
		if c.tok != "" {
			req.Header.Set(LocalTokenHeader, c.tok)
		}
		marked = false
		h.ServeHTTP(httptest.NewRecorder(), req)
		if marked != c.want {
			t.Errorf("token %q: marked %v, want %v", c.tok, marked, c.want)
		}
	}
	// the mark is the middleware's alone: nothing else sets it
	if InternalCaller(httptest.NewRequest(http.MethodGet, "/x", nil)) {
		t.Fatal("a bare request reads as internal")
	}
}

// The sign-in routes' table ignores the mark: the home's own callers get no extra logins.
func TestAuthRouteLimitIgnoresInternalMark(t *testing.T) {
	setLocalToken(t, "tok-0123456789abcdef0123456789abcdef")
	h := InternalCallers(-1)(AuthRouteRateLimit([]AuthRouteLimit{{Path: "/api/auth/login", PerMin: 2}})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	refused := 0
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set(LocalTokenHeader, "tok-0123456789abcdef0123456789abcdef")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused != 3 {
		t.Fatalf("%d of 5 logins refused, want 3", refused)
	}
}
