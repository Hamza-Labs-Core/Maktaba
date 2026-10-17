package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The SSE channels under /ws/* type-assert http.Flusher; the wrappers
// that sit on every request must keep it, or every stream answers 500
// ("streaming unsupported").
func TestWrappersKeepFlusher(t *testing.T) {
	rec := httptest.NewRecorder()
	var gw http.ResponseWriter = &guardedWriter{ResponseWriter: rec, ctx: slog.Default()}
	var sw http.ResponseWriter = &statusCapture{ResponseWriter: gw}
	f, ok := sw.(http.Flusher)
	if !ok {
		t.Fatal("statusCapture is not an http.Flusher")
	}
	f.Flush()
	if !rec.Flushed || rec.Code != http.StatusOK {
		t.Fatalf("flush did not reach the recorder: flushed=%v code=%d", rec.Flushed, rec.Code)
	}
	if _, ok := gw.(http.Flusher); !ok {
		t.Fatal("guardedWriter is not an http.Flusher")
	}
}
