package system

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStatusHandlerPassthroughIsNotHTML: a response StatusHandler cannot decorate (an
// error, or a body that is not JSON) is relayed as text, never as a page a browser
// would run (CodeQL go/reflected-xss, alert 150).
func TestStatusHandlerPassthroughIsNotHTML(t *testing.T) {
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("<script>alert(" + r.URL.Query().Get("x") + ")</script>"))
	})
	rec := httptest.NewRecorder()
	(&SelfUpdater{}).StatusHandler(base).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system/updates?x=1", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("Content-Type = %q, nosniff = %q", ct, rec.Header().Get("X-Content-Type-Options"))
	}
}
