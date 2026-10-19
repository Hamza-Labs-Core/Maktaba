package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertThemed(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{`content="dark light"`, "color-scheme:dark light", "@media (prefers-color-scheme: light)", "--mk-bg:#09090b"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Error("page has a script")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "style-src 'sha256-") {
		t.Error("csp does not pin the stylesheet")
	}
}

func TestRelayErrorPagesFollowTheSystemTheme(t *testing.T) {
	d := &Deps{PublicHost: "relay.maktaba.app"}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "elsewhere.example"
	req.Header.Set("Sec-Fetch-Dest", "document")
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code %d", rec.Code)
	}
	assertThemed(t, rec)

	// the home server is offline: a themed page that retries
	rec = httptest.NewRecorder()
	writeErr(rec, req, http.StatusBadGateway, "server_offline", "")
	assertThemed(t, rec)
	if !strings.Contains(rec.Body.String(), `http-equiv="refresh"`) || !strings.Contains(rec.Body.String(), "unreachable") {
		t.Error("offline page should say unreachable and retry")
	}

	// an app fetch keeps the JSON body
	api := httptest.NewRequest("GET", "/api/x", nil)
	api.Header.Set("Sec-Fetch-Dest", "empty")
	rec = httptest.NewRecorder()
	writeErr(rec, api, http.StatusBadGateway, "server_offline", "")
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("fetch content type %q", rec.Header().Get("Content-Type"))
	}
}
