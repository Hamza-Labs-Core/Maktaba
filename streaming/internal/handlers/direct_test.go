package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
)

// memoryFile is a FileReader backed by a byte slice.
type memoryFile struct {
	data []byte
	pos  int64
	mt   time.Time
}

func (m *memoryFile) Read(p []byte) (int, error) {
	if m.pos >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.pos:])
	m.pos += int64(n)
	return n, nil
}
func (m *memoryFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case 0:
		m.pos = offset
	case 1:
		m.pos += offset
	case 2:
		m.pos = int64(len(m.data)) + offset
	}
	return m.pos, nil
}
func (m *memoryFile) Close() error { return nil }
func (m *memoryFile) Stat() (os.FileInfo, error) {
	return &memoryStat{name: "x.bin", size: int64(len(m.data)), mt: m.mt}, nil
}

type memoryStat struct {
	name string
	size int64
	mt   time.Time
}

func (s *memoryStat) Name() string       { return s.name }
func (s *memoryStat) Size() int64        { return s.size }
func (s *memoryStat) Mode() os.FileMode  { return 0644 }
func (s *memoryStat) ModTime() time.Time { return s.mt }
func (s *memoryStat) IsDir() bool        { return false }
func (s *memoryStat) Sys() any           { return nil }

type memoryOpener struct {
	files map[string]*memoryFile
}

func (m *memoryOpener) Open(path string) (FileReader, error) {
	f, ok := m.files[path]
	if !ok {
		return nil, errors.New("not found")
	}
	// Return a fresh handle so tests don't share position state.
	return &memoryFile{data: f.data, mt: f.mt}, nil
}

func setupHandler(t *testing.T) (*DirectHandler, *probe.Row, *memoryOpener, http.Handler) {
	t.Helper()
	row := &probe.Row{
		VideoID:     uuid.New(),
		LibraryID:   uuid.New(),
		ContentHash: "abc",
		Path:        "/v/x.mp4",
		Container:   "mp4",
		VideoCodec:  "h264",
		AudioCodec:  "aac",
		Height:      1080,
		BitrateKbps: 4000,
		Probed:      true,
		ModTime:     time.Now().UTC().Truncate(time.Second),
	}
	fb := probe.NewFakeBackend()
	fb.Set(row)
	cache := probe.NewCache(fb, 16)
	body := bytes.Repeat([]byte("ABCDEFGHIJ"), 100) // 1000 bytes
	mo := &memoryOpener{files: map[string]*memoryFile{"/v/x.mp4": {data: body, mt: row.ModTime}}}
	h := &DirectHandler{Probe: cache, Profiles: capability.NewRegistry(), Files: mo, AllowAllProfiles: true}

	r := chi.NewRouter()
	// Inject claims into context the way SignedURL would.
	r.With(injectSubject(row.VideoID.String())).Get("/stream/direct/{video_id}", h.ServeHTTP)
	r.With(injectSubject(row.VideoID.String())).Head("/stream/direct/{video_id}", h.ServeHTTP)
	return h, row, mo, r
}

// injectSubject stuffs a subject into the request context the way
// auth.SignedURL would after verification, so we can test the handler
// without minting a real JWT every time.
func injectSubject(sub string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cl := &auth.Claims{Sub: sub}
			ctx := auth.ContextWithClaims(r.Context(), cl)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func TestDirect_FullGetReturns200(t *testing.T) {
	_, row, _, h := setupHandler(t)
	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatal("missing Accept-Ranges")
	}
	if rec.Header().Get("Content-Length") != "1000" {
		t.Fatalf("content-length=%s", rec.Header().Get("Content-Length"))
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "video/") {
		t.Fatalf("content-type=%s", rec.Header().Get("Content-Type"))
	}
}

func TestDirect_HeadReturnsHeadersNoBody(t *testing.T) {
	_, row, _, h := setupHandler(t)
	req := httptest.NewRequest("HEAD", "/stream/direct/"+row.VideoID.String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("Content-Length") != "1000" {
		t.Fatalf("content-length=%s", rec.Header().Get("Content-Length"))
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD body len=%d", rec.Body.Len())
	}
}

func TestDirect_RangeReturns206(t *testing.T) {
	_, row, _, h := setupHandler(t)
	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil)
	req.Header.Set("Range", "bytes=10-19")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 206 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Range") != "bytes 10-19/1000" {
		t.Fatalf("content-range=%s", rec.Header().Get("Content-Range"))
	}
	if rec.Header().Get("Content-Length") != "10" {
		t.Fatalf("content-length=%s", rec.Header().Get("Content-Length"))
	}
	if rec.Body.Len() != 10 {
		t.Fatalf("body len=%d", rec.Body.Len())
	}
}

func TestDirect_RangeOpenEnded(t *testing.T) {
	_, row, _, h := setupHandler(t)
	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil)
	req.Header.Set("Range", "bytes=990-")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 206 {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("Content-Range") != "bytes 990-999/1000" {
		t.Fatalf("content-range=%s", rec.Header().Get("Content-Range"))
	}
}

func TestDirect_RangeSuffix(t *testing.T) {
	_, row, _, h := setupHandler(t)
	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil)
	req.Header.Set("Range", "bytes=-100")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 206 {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("Content-Range") != "bytes 900-999/1000" {
		t.Fatalf("content-range=%s", rec.Header().Get("Content-Range"))
	}
}

func TestDirect_MultipartRangeReturns416(t *testing.T) {
	_, row, _, h := setupHandler(t)
	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil)
	req.Header.Set("Range", "bytes=0-100,200-300")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 416 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "multi-range-unsupported") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestDirect_BadRangeReturns416(t *testing.T) {
	_, row, _, h := setupHandler(t)
	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil)
	req.Header.Set("Range", "bytes=2000-3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 416 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDirect_FileMissingReturns404(t *testing.T) {
	row := &probe.Row{
		VideoID:    uuid.New(),
		LibraryID:  uuid.New(),
		Path:       "/v/missing.mp4",
		Container:  "mp4",
		VideoCodec: "h264",
		AudioCodec: "aac",
		Probed:     true,
	}
	fb := probe.NewFakeBackend()
	fb.Set(row)
	cache := probe.NewCache(fb, 16)
	mo := &memoryOpener{files: map[string]*memoryFile{}}
	h := &DirectHandler{Probe: cache, Profiles: capability.NewRegistry(), Files: mo, AllowAllProfiles: true}
	r := chi.NewRouter()
	r.With(injectSubject(row.VideoID.String())).Get("/stream/direct/{video_id}", h.ServeHTTP)

	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDirect_NotProbedReturns424(t *testing.T) {
	row := &probe.Row{VideoID: uuid.New(), LibraryID: uuid.New(), Path: "/v/x.mp4", Probed: false}
	fb := probe.NewFakeBackend()
	fb.Set(row)
	h := &DirectHandler{Probe: probe.NewCache(fb, 16), Profiles: capability.NewRegistry(), Files: &memoryOpener{}, AllowAllProfiles: true}
	r := chi.NewRouter()
	r.With(injectSubject(row.VideoID.String())).Get("/stream/direct/{video_id}", h.ServeHTTP)
	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 424 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		hdr     string
		size    int64
		ws, we  int64
		wMulti  bool
		wantErr bool
	}{
		{"bytes=0-99", 1000, 0, 99, false, false},
		{"bytes=900-", 1000, 900, 999, false, false},
		{"bytes=-100", 1000, 900, 999, false, false},
		{"bytes=0-100,200-300", 1000, 0, 0, true, false},
		{"bytes=2000-3000", 1000, 0, 0, false, true},
		{"junk", 1000, 0, 0, false, true},
	}
	for _, c := range cases {
		s, e, multi, err := parseRange(c.hdr, c.size)
		if multi != c.wMulti {
			t.Fatalf("%q multi=%v want %v", c.hdr, multi, c.wMulti)
		}
		if c.wMulti {
			continue
		}
		if c.wantErr {
			if err == nil {
				t.Fatalf("%q expected error", c.hdr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q err=%v", c.hdr, err)
		}
		if s != c.ws || e != c.we {
			t.Fatalf("%q got %d-%d want %d-%d", c.hdr, s, e, c.ws, c.we)
		}
	}
}

var _ = context.Background
