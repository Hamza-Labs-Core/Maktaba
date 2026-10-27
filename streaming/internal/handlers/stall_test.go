package handlers

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stallingFile reads failAt bytes, then blocks (a NAS that stopped answering) until
// release; it records its Close.
type stallingFile struct {
	memoryFile
	failAt  int64
	release chan struct{}
	closed  atomic.Bool
}

func (s *stallingFile) Read(p []byte) (int, error) {
	if s.pos >= s.failAt {
		<-s.release
		return 0, nil
	}
	if rem := s.failAt - s.pos; int64(len(p)) > rem {
		p = p[:rem]
	}
	return s.memoryFile.Read(p)
}

func (s *stallingFile) Close() error { s.closed.Store(true); return nil }

type stallingOpener struct{ f *stallingFile }

func (o stallingOpener) Open(string) (FileReader, error) { return o.f, nil }

// A read of the source that never returns ends the response after the stall limit,
// logged with its video and offset; the file is closed once that read returns.
func TestDirect_StalledReadEndsTheResponse(t *testing.T) {
	h, row, _, r := setupHandler(t)
	var buf bytes.Buffer
	h.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	h.ReadStall = 50 * time.Millisecond
	f := &stallingFile{memoryFile: memoryFile{data: bytes.Repeat([]byte("x"), 1000)}, failAt: 100,
		release: make(chan struct{})}
	h.Files = stallingOpener{f}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil))
		done <- rec
	}()
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the response waited on a stalled read")
	}
	if rec.Body.Len() != 100 {
		t.Errorf("wrote %d bytes, want the 100 read before the stall", rec.Body.Len())
	}
	out := buf.String()
	for _, want := range []string{"event=vod_direct_read_stalled", "level=ERROR", "video_id=" + row.VideoID.String(),
		"written=100", "stall_ms=50"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if f.closed.Load() {
		t.Fatal("the file was closed under its stalled read")
	}
	close(f.release)
	deadline := time.Now().Add(5 * time.Second)
	for !f.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the file was never closed after its stalled read returned")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Reads that return in time pass through, and Close closes at once.
func TestStallFile_PassesReadsThrough(t *testing.T) {
	m := &memoryFile{data: []byte("hello")}
	s := watchStalls(m, time.Second)
	p := make([]byte, 10)
	n, err := s.Read(p)
	if err != nil || string(p[:n]) != "hello" {
		t.Fatalf("Read = %q, %v", p[:n], err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if watchStalls(m, 0).limit != defaultReadStall {
		t.Error("no default stall limit")
	}
}
