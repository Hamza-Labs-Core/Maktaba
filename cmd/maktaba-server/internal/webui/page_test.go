package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The server's own pages follow the system theme and default to dark (never white).
func TestErrorPageIsThemedDarkByDefault(t *testing.T) {
	r := httptest.NewRequest("GET", "/library", nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	w := httptest.NewRecorder()
	Error(w, r, http.StatusBadGateway, "This machine cannot reach its home", "<x>")
	body := w.Body.String()
	if w.Code != 502 || !strings.Contains(body, "color-scheme") || !strings.Contains(body, "--bg:#111317") ||
		!strings.Contains(body, "prefers-color-scheme: light") || strings.Contains(body, "<x>") {
		t.Fatalf("page: %d %s", w.Code, body)
	}
	r = httptest.NewRequest("GET", "/api/x", nil)
	w = httptest.NewRecorder()
	Error(w, r, http.StatusBadGateway, "t", "d")
	if strings.Contains(w.Body.String(), "<html") {
		t.Fatal("a non-browser request gets plain text")
	}
}
