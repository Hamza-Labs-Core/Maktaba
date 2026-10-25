package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What another machine's stream says it is keeps its type only when it is a medium or data
// the link carries; a page (HTML, SVG, XML) or a type it does not say is served as bytes.
func TestProxiedTypeNeverServesAPage(t *testing.T) {
	for ct, want := range map[string]string{
		"video/MP2T":                    "video/MP2T",
		"application/vnd.apple.mpegurl": "application/vnd.apple.mpegurl",
		"audio/aac":                     "audio/aac",
		"image/jpeg":                    "image/jpeg",
		"application/json":              "application/json",
		"application/problem+json":      "application/problem+json",
		"text/vtt; charset=utf-8":       "text/vtt; charset=utf-8",
		"application/dash+xml":          "application/dash+xml",
		"text/plain; charset=utf-8":     "text/plain; charset=utf-8",
		"text/html; charset=utf-8":      "application/octet-stream",
		"application/xhtml+xml":         "application/octet-stream",
		"image/svg+xml":                 "application/octet-stream",
		"text/xml":                      "application/octet-stream",
		"":                              "application/octet-stream",
		"<script>alert(1)</script>":     "application/octet-stream",
	} {
		if got := proxiedType(ct); got != want {
			t.Errorf("%q: %q, want %q", ct, got, want)
		}
	}
}

// A request's words never come back in the answer of the node's local fetch: a path with a
// script in it is refused in the route's own words, as plain text that is not sniffed.
func TestLocalFetchNeverEchoesTheRequest(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("t0k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", tok)
	h := serveLocalFetch(nil)
	for _, path := range []string{"<script>alert(1)</script>", "/../<script>alert(1)</script>", "/<script>alert(1)</script>"} {
		req := httptest.NewRequest(http.MethodGet, "/api/cluster/link/local/fetch?path="+url.QueryEscape(path), nil)
		req.Header.Set("X-Maktaba-Local-Token", "t0k")
		rec := httptest.NewRecorder()
		h(rec, req)
		if strings.Contains(rec.Body.String(), "<script>") {
			t.Errorf("%q came back: %d %q", path, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%q: Content-Type %q", path, ct)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%q: sniffable", path)
		}
	}
}
