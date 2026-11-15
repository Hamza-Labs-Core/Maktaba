package frontdoor

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A node or slot that ends its chunked answer with an orderly close before the last
// chunk (its process exited mid-segment) must not reach the player as a whole
// segment: the front door aborts its own answer, so the player gets no last chunk.
func TestCopyResponseCutUpstreamIsNotWhole(t *testing.T) {
	part := bytes.Repeat([]byte{0x47}, 64<<10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(part)
		w.(http.Flusher).Flush()
		// a FIN with the body unfinished: no last chunk
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = buf.Flush()
		_ = conn.Close()
	}))
	defer upstream.Close()

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp, err := http.Get(upstream.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		copyResponse(w, resp, nil)
	}))
	defer front.Close()

	resp, err := http.Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("a cut segment came through as %d bytes with %v: want io.ErrUnexpectedEOF", len(got), err)
	}
}

// An upstream answer that ends well comes through whole, with its length.
func TestCopyResponseWholeKeepsTheLength(t *testing.T) {
	seg := bytes.Repeat([]byte{0x47}, 1<<20)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1048576")
		_, _ = w.Write(seg)
	}))
	defer upstream.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp, err := http.Get(upstream.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		copyResponse(w, resp, nil)
	}))
	defer front.Close()
	resp, err := http.Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || !bytes.Equal(got, seg) || resp.ContentLength != int64(len(seg)) {
		t.Fatalf("%d bytes, %v, Content-Length %d", len(got), err, resp.ContentLength)
	}
}
