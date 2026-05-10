package grpcsrv

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slots"
)

func setupServer(t *testing.T) (*Server, *probe.FakeBackend, *probe.Row) {
	t.Helper()
	fb := probe.NewFakeBackend()
	row := &probe.Row{
		VideoID: uuid.New(), LibraryID: uuid.New(), ContentHash: "h1",
		Path: "/v/x.mp4", Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
		Height: 1080, BitrateKbps: 6000, Probed: true,
	}
	fb.Set(row)
	pc := probe.NewCache(fb, 16)
	store := session.NewMemoryStore(time.Second)
	alloc := slots.NewAllocator(slots.AllocatorConfig{MaxTranscode: 2, QueueDepth: 2})
	srv := New(pc, store, alloc, capability.NewRegistry())
	return srv, fb, row
}

func TestOpenSession_Direct(t *testing.T) {
	srv, _, row := setupServer(t)
	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "ios-native",
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if resp.Mode != "direct" {
		t.Fatalf("mode=%s want direct", resp.Mode)
	}
	if resp.ManifestPath != "/stream/direct/"+row.VideoID.String() {
		t.Fatalf("manifest=%s", resp.ManifestPath)
	}
	if resp.ExpiresAt.Before(time.Now()) {
		t.Fatal("expires_at in past")
	}
}

func TestOpenSession_Transcode(t *testing.T) {
	srv, _, row := setupServer(t)
	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "generic", // forces transcode at 1080p
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if resp.Mode != "transcode" {
		t.Fatalf("mode=%s want transcode (1080p > generic 720p)", resp.Mode)
	}
	if resp.ManifestPath == "" || resp.ManifestPath[:8] != "/stream/" {
		t.Fatalf("manifest path=%s", resp.ManifestPath)
	}
}

func TestOpenSession_DASHFormat(t *testing.T) {
	srv, _, row := setupServer(t)
	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "browser-chrome", Format: "dash", ForceTranscode: true,
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if resp.ManifestPath[len(resp.ManifestPath)-3:] != "mpd" {
		t.Fatalf("manifest=%s want .mpd", resp.ManifestPath)
	}
}

func TestOpenSession_NotProbed(t *testing.T) {
	srv, fb, _ := setupServer(t)
	row := &probe.Row{VideoID: uuid.New(), LibraryID: uuid.New(), Probed: false}
	fb.Set(row)
	_, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
	})
	if !errors.Is(err, ErrFailedPrecondition) {
		t.Fatalf("err=%v want ErrFailedPrecondition", err)
	}
}

func TestOpenSession_DedupesByUserVideo(t *testing.T) {
	srv, _, row := setupServer(t)
	user := uuid.New().String()
	r1, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: user, ClientProfile: "ios-native",
	})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: user, ClientProfile: "ios-native",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r1.SessionID != r2.SessionID {
		t.Fatalf("expected dedupe; got %s vs %s", r1.SessionID, r2.SessionID)
	}
}

func TestOpenSession_SlotExhausted(t *testing.T) {
	srv, _, _ := setupServer(t)
	// Force the allocator to its cap.
	for i := 0; i < 2; i++ {
		_, _ = srv.Allocator.Decide(slots.Request{})
	}
	// Make a new row that requires transcode.
	fb := probe.NewFakeBackend()
	row := &probe.Row{
		VideoID: uuid.New(), LibraryID: uuid.New(),
		Path: "/v/y.mp4", Container: "mp4", VideoCodec: "av1", AudioCodec: "opus",
		Height: 480, BitrateKbps: 3000, Probed: true,
	}
	fb.Set(row)
	srv.Probe = probe.NewCache(fb, 16)

	_, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "generic",
	})
	if !errors.Is(err, ErrResourceExhausted) {
		t.Fatalf("err=%v want ErrResourceExhausted", err)
	}
}

func TestCloseSession(t *testing.T) {
	srv, _, row := setupServer(t)
	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(), ClientProfile: "ios-native",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.CloseSession(context.Background(), resp.SessionID); err != nil {
		t.Fatalf("close: %v", err)
	}
	// idempotent
	if err := srv.CloseSession(context.Background(), resp.SessionID); err != nil {
		t.Fatalf("re-close: %v", err)
	}
}

func TestCloseSession_NotFound(t *testing.T) {
	srv, _, _ := setupServer(t)
	err := srv.CloseSession(context.Background(), uuid.New().String())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestEvictHashCache(t *testing.T) {
	srv, fb, row := setupServer(t)
	row2 := &probe.Row{VideoID: uuid.New(), LibraryID: row.LibraryID, ContentHash: "h1", Probed: true, Path: "/v/y.mp4"}
	fb.Set(row2)
	_, _ = srv.Probe.Lookup(context.Background(), row.VideoID)
	_, _ = srv.Probe.Lookup(context.Background(), row2.VideoID)
	n, err := srv.EvictHashCache(context.Background(), "h1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("evicted=%d want 2", n)
	}
}

func TestGetCapabilities(t *testing.T) {
	srv, _, _ := setupServer(t)
	srv.HWAccel = "videotoolbox"
	caps, err := srv.GetCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps.HWAccel != "videotoolbox" {
		t.Fatalf("hwaccel=%s", caps.HWAccel)
	}
	if caps.TranscodeCapacity < 1 {
		t.Fatalf("capacity=%d", caps.TranscodeCapacity)
	}
}

func TestHealthCheck(t *testing.T) {
	srv, _, _ := setupServer(t)
	st, err := srv.HealthCheck(context.Background())
	if err != nil || !st.Healthy {
		t.Fatalf("status=%+v err=%v", st, err)
	}
}
