package httperror

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A browser navigation gets a themed page (system theme, dark fallback),
// never raw JSON on a white tab; a fetch keeps problem+json.
func TestWriteNavigationGetsThemedPage(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/reports/nope.html", nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	r.Header.Set("Sec-Fetch-Dest", "document")
	w := httptest.NewRecorder()
	Write(w, r, &Error{Type: "about:blank", Title: "Unauthorized", Status: http.StatusUnauthorized, Detail: "sign in first"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{`content="dark light"`, "color-scheme:dark light", "prefers-color-scheme: light", `href="/login"`, "sign in first"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Error("page has a script")
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "style-src 'sha256-") {
		t.Errorf("csp %q", csp)
	}

	f := httptest.NewRequest(http.MethodGet, "/api/videos", nil)
	f.Header.Set("Accept", "application/json")
	f.Header.Set("Sec-Fetch-Dest", "empty")
	w = httptest.NewRecorder()
	Write(w, f, NotFound("x"))
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("fetch content type %q", ct)
	}
}
