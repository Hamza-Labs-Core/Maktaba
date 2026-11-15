package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// A session's segment and its playlists go out with their length, never chunked: a
// chunked body cut by an orderly close (the service exiting mid-answer) reads as whole
// to Apple's URL loading system, and a truncated segment would be decoded as whole.
func TestSegmentsAndPlaylistsCarryTheirLength(t *testing.T) {
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()
	sessID := uuid.New()
	rendDir := filepath.Join(layout.HLSDir(sessID.String()), "v0")
	if err := os.MkdirAll(rendDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seg := bytes.Repeat([]byte{0x47}, 1<<20) // larger than the server's 2 KB sniff buffer
	if err := os.WriteFile(filepath.Join(rendDir, "seg-0.ts"), seg, 0o644); err != nil {
		t.Fatal(err)
	}
	store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{sessID: {ID: sessID, Format: session.FormatHLS}}}
	h := &ManifestHandler{Sessions: store, Layout: layout}
	r := chi.NewRouter()
	r.With(injectSubject(sessID.String())).Get("/stream/{session_id}/{rendition}/{segment}", h.ServeSegment)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/stream/" + sessID.String() + "/v0/seg-0.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || !bytes.Equal(got, seg) {
		t.Fatalf("segment: %d bytes, %v", len(got), err)
	}
	if resp.ContentLength != int64(len(seg)) || len(resp.TransferEncoding) != 0 {
		t.Fatalf("segment: Content-Length %d, Transfer-Encoding %v; want %d and none", resp.ContentLength, resp.TransferEncoding, len(seg))
	}
}

// A sidecar subtitle file goes out with its length too.
func TestSidecarSubtitleCarriesItsLength(t *testing.T) {
	vtt := append([]byte("WEBVTT\n\n"), bytes.Repeat([]byte("00:00:01.000 --> 00:00:02.000\nhello\n\n"), 400)...)
	h := &SubtitleHandler{SidecarReader: func(context.Context, string) ([]byte, error) { return vtt, nil }}
	r := chi.NewRouter()
	r.Get("/subs/sidecar/{lang}.vtt", h.ServeSidecar)
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/subs/sidecar/en.vtt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.ContentLength != int64(len(vtt)) || len(resp.TransferEncoding) != 0 || !bytes.Equal(got, vtt) {
		t.Fatalf("Content-Length %d, Transfer-Encoding %v, %d bytes; want %d, none", resp.ContentLength, resp.TransferEncoding, len(got), len(vtt))
	}
}
