package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Every page the web port renders itself follows the system theme with a
// dark fallback (shared/go/pagetheme), above all the ones a user sees when
// something is down: no bright white error tab.

var browserNav = map[string]string{
	"Accept":         "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	"Sec-Fetch-Dest": "document",
}

func assertThemedPage(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q, body %q", ct, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<meta name="color-scheme" content="dark light">`,
		"color-scheme:dark light",
		"@media (prefers-color-scheme: light)",
		"--mk-bg:#09090b",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Error("page carries a script")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "style-src 'sha256-") {
		t.Errorf("csp %q does not allow the inline stylesheet", csp)
	}
}

// deadWeb is the web port with neither the API nor streaming listening:
// the "server is restarting" case.
func deadWeb() http.Handler {
	var cfg config.Config
	cfg.Server.Listen = "127.0.0.1:1"
	cfg.Server.StreamAddr = "127.0.0.1:1"
	return webHandler(cfg)
}

func TestAPIDownPageIsThemedAndRetries(t *testing.T) {
	rec := get(deadWeb(), "/api/reports/channel-plan.html", browserNav)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code %d", rec.Code)
	}
	assertThemedPage(t, rec)
	if !strings.Contains(rec.Body.String(), `http-equiv="refresh"`) {
		t.Error("the restarting page should reload by itself")
	}
}

func TestAPIDownFetchGetsProblemJSON(t *testing.T) {
	rec := get(deadWeb(), "/api/auth/me", map[string]string{"Accept": "application/json", "Sec-Fetch-Dest": "empty"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type %q", ct)
	}
}

func TestLiveTVDownPageIsThemed(t *testing.T) {
	rec := get(deadWeb(), "/xmltv.xml", browserNav)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code %d", rec.Code)
	}
	assertThemedPage(t, rec)
}

func TestStreamGateErrorPagesAreThemed(t *testing.T) {
	mux, _, _, _ := webMux(t)
	for path, code := range map[string]int{
		"/stream/nope": http.StatusNotFound,
		"/stream/channel/" + chanID + "/index.m3u8": http.StatusUnauthorized,
	} {
		rec := get(mux, path, browserNav)
		if rec.Code != code {
			t.Fatalf("%s: code %d", path, rec.Code)
		}
		assertThemedPage(t, rec)
	}
	// a player's fetch keeps the plain-text answer
	rec := get(mux, "/stream/channel/"+chanID+"/index.m3u8", map[string]string{"Sec-Fetch-Dest": "empty"})
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("player fetch content type %q", ct)
	}
}

func TestWebUIPlaceholderIsThemed(t *testing.T) {
	rec := get(deadWeb(), "/", browserNav)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	assertThemedPage(t, rec)
}
