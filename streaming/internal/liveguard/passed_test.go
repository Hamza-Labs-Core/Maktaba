package liveguard

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// What the cut writer passes on goes out with nosniff, under the handler's own
// type, and never as a page; once cut it writes nothing.
func TestCutWriterPassesBodiesSafely(t *testing.T) {
	for in, want := range map[string]string{
		"video/mp2t":               "video/mp2t",
		"text/html; charset=utf-8": "application/octet-stream",
		"":                         "application/octet-stream",
	} {
		rec := httptest.NewRecorder()
		cw := &cutWriter{ResponseWriter: rec}
		if in != "" {
			cw.Header().Set("Content-Type", in)
		}
		if _, err := cw.Write([]byte("<script>")); err != nil {
			t.Fatal(err)
		}
		if h := rec.Header(); h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Type") != want {
			t.Errorf("a write of %q: headers %v", in, h)
		}
		rec = httptest.NewRecorder()
		cw = &cutWriter{ResponseWriter: rec}
		if in != "" {
			cw.Header().Set("Content-Type", in)
		}
		cw.WriteHeader(http.StatusOK)
		if h := rec.Result().Header; h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Type") != want {
			t.Errorf("a status with %q: headers %v", in, h)
		}
	}
	rec := httptest.NewRecorder()
	cw := &cutWriter{ResponseWriter: rec}
	cw.cut.Store(true)
	if _, err := cw.Write([]byte("x")); !errors.Is(err, errCut) || rec.Body.Len() != 0 {
		t.Fatalf("a cut stream wrote: %v %q", err, rec.Body.String())
	}
}

func playlistHandler(ctype string, status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

// A signed playlist carries the signature only when it is a token's characters; text
// that is none is written nowhere, and the playlist goes out unsigned. Every answer
// has nosniff, and none is a page.
func TestServeSignedPlaylistWritesOnlyAToken(t *testing.T) {
	const playlist = "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\nseg1.ts\n"
	serve := func(h http.Handler, sig string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		serveSignedPlaylist(rec, httptest.NewRequest(http.MethodGet, "/stream/channel/x/index.m3u8", nil), h, sig)
		return rec
	}
	rec := serve(playlistHandler("application/vnd.apple.mpegurl", http.StatusOK, playlist), "aGVhZA.Ym9keQ.c2ln-_")
	if got := rec.Body.String(); got != "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4?sig=aGVhZA.Ym9keQ.c2ln-_\"\nseg1.ts?sig=aGVhZA.Ym9keQ.c2ln-_\n" {
		t.Errorf("a signed playlist = %q", got)
	}
	if h := rec.Header(); h.Get("Content-Type") != "application/vnd.apple.mpegurl" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("a signed playlist's headers = %v", h)
	}
	for _, sig := range []string{`<script>alert(1)</script>`, `a"b`, "a b", "a\nb", "a.b&c=d", "", ".", "a..b"} {
		rec := serve(playlistHandler("application/vnd.apple.mpegurl", http.StatusOK, playlist), sig)
		if rec.Body.String() != playlist {
			t.Errorf("sig %q was written: %q", sig, rec.Body.String())
		}
	}
	// a page from the handler (it sends none) could still not be opened as one
	rec = serve(playlistHandler("text/html", http.StatusNotFound, "<html>"), "aGVhZA.Ym9keQ.c2ln")
	if h := rec.Header(); rec.Code != http.StatusNotFound || h.Get("Content-Type") != "application/octet-stream" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("a page = %d %v", rec.Code, h)
	}
}

// A playlist too large to hold is passed on unsigned, whole, with nosniff.
func TestBufferWriterPassesALargeBodySafely(t *testing.T) {
	rec := httptest.NewRecorder()
	big := bytes.Repeat([]byte("a"), playlistMax)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("head\n"))
		_, _ = w.Write(big)
		_, _ = w.Write([]byte("\ntail"))
	})
	serveSignedPlaylist(rec, httptest.NewRequest(http.MethodGet, "/x.m3u8", nil), h, "aGVhZA.Ym9keQ.c2ln")
	got := rec.Body.String()
	if len(got) != len(big)+10 || !strings.HasPrefix(got, "head\n") || !strings.HasSuffix(got, "\ntail") || strings.Contains(got, "sig=") {
		t.Fatalf("a large body: %d bytes", len(got))
	}
	if hd := rec.Header(); hd.Get("Content-Type") != "application/octet-stream" || hd.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("a large body's headers = %v", hd)
	}
}
