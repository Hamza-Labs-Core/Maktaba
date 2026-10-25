package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The VOD fallback goes over the machine's cluster link while it is up (story 35.16
// AC14), to the home's streaming port while it is not.
func TestVODFallbackOverTheLink(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("t0k"), 0o600)
	up := true
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			w.Header().Set("X-Maktaba-Link", "down")
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Range", r.Header.Get("Range"))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "link "+r.URL.Query().Get("purpose")+" "+r.URL.Query().Get("path"))
	}))
	defer api.Close()
	direct := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "direct "+r.URL.Path) })
	h := vodOverLink(api.URL, tok, direct)
	get := func() (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/stream/s1/video.mp4?sig=x", nil)
		req.Header.Set("Range", "bytes=0-9")
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, b := get(); code != http.StatusPartialContent || b != "link vod /stream/s1/video.mp4?sig=x" {
		t.Fatalf("over the link: %d %q", code, b)
	}
	up = false
	if _, b := get(); b != "direct /stream/s1/video.mp4" {
		t.Fatalf("link down: %q", b)
	}
}
