package updates

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestExtendStatusPassthroughIsNotHTML: a response ExtendStatus cannot decorate is
// relayed as text, never as a page a browser would run (CodeQL go/reflected-xss,
// alert 37).
func TestExtendStatusPassthroughIsNotHTML(t *testing.T) {
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<script>alert(" + r.URL.Query().Get("x") + ")</script>"))
	})
	rec := httptest.NewRecorder()
	(&Handler{}).ExtendStatus(base).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system/updates?x=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("Content-Type = %q, nosniff = %q", ct, rec.Header().Get("X-Content-Type-Options"))
	}
}
