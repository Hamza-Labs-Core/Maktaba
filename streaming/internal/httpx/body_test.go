package httpx

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// cutWriter takes the first `left` bytes and then refuses every write, as a
// guard that stops a stream (liveguard, timelimit) or a client that left does.
type cutWriter struct {
	http.ResponseWriter
	left int
}

func (c *cutWriter) Write(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errors.New("cut")
	}
	if len(p) > c.left {
		p = p[:c.left]
	}
	n, err := c.ResponseWriter.Write(p)
	c.left -= n
	if err == nil && c.left <= 0 {
		err = errors.New("cut")
	}
	return n, err
}

func get(t *testing.T, h http.HandlerFunc) (*http.Response, []byte, error) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, b, err
}

const segment = 1 << 20

// A body sent whole goes out with its length, never chunked.
func TestWriteBodySendsTheLength(t *testing.T) {
	body := bytes.Repeat([]byte{0x47}, segment)
	resp, got, err := get(t, func(w http.ResponseWriter, _ *http.Request) { WriteBody(w, http.StatusOK, body) })
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("read %d bytes, %v", len(got), err)
	}
	if resp.ContentLength != segment || len(resp.TransferEncoding) != 0 {
		t.Fatalf("Content-Length %d, Transfer-Encoding %v: want %d and none", resp.ContentLength, resp.TransferEncoding, segment)
	}
}

// The case this guards against: a segment cut part way (its writer refuses the rest)
// is a short body with a length, an error for the client, never a whole one.
func TestATruncatedSegmentIsNotDeliveredWhole(t *testing.T) {
	body := bytes.Repeat([]byte{0x47}, segment)
	_, got, err := get(t, func(w http.ResponseWriter, _ *http.Request) {
		WriteBody(&cutWriter{ResponseWriter: w, left: segment / 8}, http.StatusOK, body)
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("a cut segment read as %d bytes with %v: want io.ErrUnexpectedEOF", len(got), err)
	}
	if len(got) >= segment {
		t.Fatalf("read %d bytes of a cut segment", len(got))
	}

	// what it was before: written with no length, the same cut went out chunked and
	// ended with the last chunk, a whole-looking body of an eighth of the segment
	_, got, err = get(t, func(w http.ResponseWriter, _ *http.Request) {
		cw := &cutWriter{ResponseWriter: w, left: segment / 8}
		w.WriteHeader(http.StatusOK)
		_, _ = cw.Write(body)
	})
	if err != nil || len(got) != segment/8 {
		t.Fatalf("without a length: %d bytes, %v (the chunked body this replaces read as whole)", len(got), err)
	}
}

// A chunked copy that fails part way is aborted: the client gets no last chunk.
func TestAbortIfCutLeavesTheLastChunkOut(t *testing.T) {
	_, got, err := get(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte{1}, 64<<10))
		w.(http.Flusher).Flush()
		AbortIfCut(errors.New("the source went away"))
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("an aborted copy read as %d bytes with %v: want io.ErrUnexpectedEOF", len(got), err)
	}
	AbortIfCut(nil) // a copy that ended well returns
}
