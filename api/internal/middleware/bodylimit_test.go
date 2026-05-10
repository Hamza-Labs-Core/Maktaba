package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodyLimitContentLength(t *testing.T) {
	calls := 0
	h := BodyLimit(1 << 10)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		calls++
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte("x")))
	r.ContentLength = 1 << 20 // big lie

	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if calls != 0 {
		t.Fatalf("handler called %d times, want 0 (must reject before handler)", calls)
	}
}

func TestBodyLimitFakeContentLength(t *testing.T) {
	// Content-Length lies small but body has more bytes; MaxBytesReader
	// caps the actual read.
	body := strings.Repeat("a", 1<<14)
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	r.ContentLength = 100 // matches the cap, so no early reject

	read := 0
	h := BodyLimit(1 << 10)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		read = len(b)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if read > (1 << 10) {
		t.Fatalf("read %d bytes, want at most %d", read, 1<<10)
	}
}

func TestBodyLimitAllowsExactSize(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 1<<10)
	r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body))
	r.ContentLength = int64(1 << 10)

	called := false
	h := BodyLimit(1 << 10)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called {
		t.Fatalf("handler not called for exact-cap body (status=%d)", w.Code)
	}
}
