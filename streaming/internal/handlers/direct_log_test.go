package handlers

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
)

// failingOpener answers every Open with err.
type failingOpener struct{ err error }

func (f failingOpener) Open(string) (FileReader, error) { return nil, f.err }

// brokenFile reads n bytes, then fails as a NAS read does.
type brokenFile struct {
	memoryFile
	failAt int64
}

func (b *brokenFile) Read(p []byte) (int, error) {
	if b.pos >= b.failAt {
		return 0, syscall.EIO
	}
	if rem := b.failAt - b.pos; int64(len(p)) > rem {
		p = p[:rem]
	}
	return b.memoryFile.Read(p)
}

type brokenOpener struct{ data []byte }

func (o brokenOpener) Open(string) (FileReader, error) {
	return &brokenFile{memoryFile: memoryFile{data: o.data}, failAt: 100}, nil
}

func TestDirect_LogsAFileThatCannotBeOpened(t *testing.T) {
	for _, tc := range []struct {
		err   error
		level string
	}{
		{fs.ErrPermission, "level=ERROR"},
		{fs.ErrNotExist, "level=WARN"},
	} {
		h, row, _, r := setupHandler(t)
		var buf bytes.Buffer
		h.Logger = slog.New(slog.NewTextHandler(&buf, nil))
		h.Files = failingOpener{tc.err}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil))
		if rec.Code != 404 {
			t.Fatalf("status %d", rec.Code)
		}
		out := buf.String()
		for _, want := range []string{"event=vod_direct_open_failed", tc.level, "video_id=" + row.VideoID.String(), "path=/v/x.mp4"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: missing %q in %s", tc.err, want, out)
			}
		}
	}
}

func TestDirect_LogsAFailedReadNotAClientThatLeft(t *testing.T) {
	h, row, _, r := setupHandler(t)
	var buf bytes.Buffer
	h.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	h.Files = brokenOpener{data: bytes.Repeat([]byte("x"), 1000)}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil))
	out := buf.String()
	for _, want := range []string{"event=vod_direct_read_failed", "written=100", "input/output error"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}

	// the client going away is not logged
	buf.Reset()
	h.copyFailed(row.VideoID, "/v/x.mp4", 0, 10, errors.New("write: broken pipe"))
	if buf.Len() != 0 {
		t.Fatalf("a client write error was logged: %s", buf.String())
	}
}

func TestManifest_ReadFailedLogsAllButNotWrittenYet(t *testing.T) {
	var buf bytes.Buffer
	h := &ManifestHandler{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	r := httptest.NewRequest("GET", "/stream/s/v0/index.m3u8", nil)
	h.readFailed(r, "/cache/hls/s/v0/index.m3u8", fs.ErrNotExist)
	if buf.Len() != 0 {
		t.Fatalf("a segment not written yet was logged: %s", buf.String())
	}
	h.readFailed(r, "/cache/hls/s/v0/index.m3u8", fs.ErrPermission)
	if !strings.Contains(buf.String(), "event=vod_file_read_failed") || !strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("a permission denial was not logged: %s", buf.String())
	}
}
