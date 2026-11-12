package timelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
)

// What the time-limited writer passes on goes out with nosniff, under the handler's
// own type, and never as a page; an ended stream writes nothing.
func TestWriterPassesBodiesSafely(t *testing.T) {
	now := time.Now()
	g := New()
	g.Now = func() time.Time { return now }
	mk := func(rec *httptest.ResponseRecorder) *writer {
		return &writer{ResponseWriter: rec, g: g, claims: &auth.Claims{}, deadline: now.Add(time.Hour), asked: now}
	}
	for in, want := range map[string]string{
		"video/mp4":                "video/mp4",
		"text/html; charset=utf-8": "application/octet-stream",
		"image/svg+xml":            "application/octet-stream",
		"":                         "application/octet-stream",
	} {
		// the handler writes with no status of its own
		rec := httptest.NewRecorder()
		w := mk(rec)
		if in != "" {
			w.Header().Set("Content-Type", in)
		}
		if _, err := w.Write([]byte("<script>alert(1)</script>")); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("more")); err != nil {
			t.Fatal(err)
		}
		h := rec.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Type") != want {
			t.Errorf("a write of %q: headers %v, want the type %q and nosniff", in, h, want)
		}
		if rec.Body.String() != "<script>alert(1)</script>more" {
			t.Errorf("a write of %q: body %q", in, rec.Body.String())
		}
		// the handler sends its status first
		rec = httptest.NewRecorder()
		w = mk(rec)
		if in != "" {
			w.Header().Set("Content-Type", in)
		}
		w.WriteHeader(http.StatusPartialContent)
		res := rec.Result()
		if rec.Code != http.StatusPartialContent || res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Content-Type") != want {
			t.Errorf("a status with %q: %d %v", in, rec.Code, res.Header)
		}
	}
	rec := httptest.NewRecorder()
	w := mk(rec)
	w.ended = true
	if _, err := w.Write([]byte("x")); err != ErrEnded || rec.Body.Len() != 0 {
		t.Fatalf("an ended stream wrote: %v %q", err, rec.Body.String())
	}
}
