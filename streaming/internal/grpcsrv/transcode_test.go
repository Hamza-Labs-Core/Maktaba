package grpcsrv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slots"
)

// fakeOrchestrator records the job and returns a controllable handle so
// the OpenSession→FFmpeg-spawn wiring is exercised without exec'ing
// ffmpeg (HLB-328 DI seam — real-by-default in main.go, fake here).
type fakeOrchestrator struct {
	calls   int
	lastJob ffmpeg.Job
	failErr error
	stopped *bool
}

func (f *fakeOrchestrator) Start(_ context.Context, job ffmpeg.Job) (*ffmpeg.Handle, error) {
	f.calls++
	f.lastJob = job
	if f.failErr != nil {
		return nil, f.failErr
	}
	st := false
	f.stopped = &st
	return ffmpeg.NewHandleForTest(
		func(context.Context) error { st = true; return nil },
		func() int { return 7777 },
		func() bool { return !st },
	), nil
}

func transcodeSetup(t *testing.T) (*Server, *probe.Row, string) {
	t.Helper()
	fb := probe.NewFakeBackend()
	row := &probe.Row{
		VideoID: uuid.New(), LibraryID: uuid.New(), ContentHash: "h1",
		Path: "/media/movie.mkv", Container: "mkv", VideoCodec: "av1", AudioCodec: "opus",
		Height: 1080, BitrateKbps: 6000, Probed: true,
	}
	fb.Set(row)
	pc := probe.NewCache(fb, 16)
	store := session.NewMemoryStore(time.Second)
	alloc := slots.NewAllocator(slots.AllocatorConfig{MaxTranscode: 2, QueueDepth: 2})
	srv := New(pc, store, alloc, capability.NewRegistry())
	cacheRoot := t.TempDir()
	srv.ResolveDir = func(id string) string { return filepath.Join(cacheRoot, "hls", id) }
	return srv, row, cacheRoot
}

func TestOpenSession_SpawnsFFmpegForTranscode(t *testing.T) {
	srv, row, _ := transcodeSetup(t)
	orch := &fakeOrchestrator{}
	srv.Transcode = orch

	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "generic", // av1/opus 1080p → transcode
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if resp.Mode != "transcode" {
		t.Fatalf("mode=%s want transcode", resp.Mode)
	}
	if orch.calls != 1 {
		t.Fatalf("orchestrator called %d times, want 1 (ffmpeg never spawned — HLB-328 regressed)", orch.calls)
	}
	if orch.lastJob.InputPath != "/media/movie.mkv" {
		t.Fatalf("job input=%q want the probe row Path", orch.lastJob.InputPath)
	}
	if orch.lastJob.Format != "hls" {
		t.Fatalf("job format=%q want hls", orch.lastJob.Format)
	}
	if orch.lastJob.OutputDir == "" || orch.lastJob.SessionID != resp.SessionID {
		t.Fatalf("job dir/session mismatch: %+v vs sess=%s", orch.lastJob, resp.SessionID)
	}

	// The handle must be pinned to the stored row so the reaper /
	// CloseSession can kill the child.
	sid := uuid.MustParse(resp.SessionID)
	stored, ok, _ := srv.Sessions.Get(context.Background(), sid)
	if !ok || stored.Transcoder == nil {
		t.Fatal("session row has no Transcoder handle — reaper can't kill ffmpeg")
	}
	if stored.PID != 7777 {
		t.Fatalf("row.PID=%d want 7777", stored.PID)
	}

	// CloseSession must terminate the child.
	if err := srv.CloseSession(context.Background(), resp.SessionID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if orch.stopped == nil || !*orch.stopped {
		t.Fatal("CloseSession did not Stop the ffmpeg handle")
	}
}

func TestOpenSession_DASHJobFormat(t *testing.T) {
	srv, row, _ := transcodeSetup(t)
	orch := &fakeOrchestrator{}
	srv.Transcode = orch
	if _, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "browser-chrome", Format: "dash", ForceTranscode: true,
	}); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if orch.lastJob.Format != "dash" {
		t.Fatalf("job format=%q want dash", orch.lastJob.Format)
	}
}

func TestOpenSession_SpawnFailureAborts(t *testing.T) {
	srv, row, _ := transcodeSetup(t)
	srv.Transcode = &fakeOrchestrator{failErr: errors.New("ffmpeg: no such encoder")}

	_, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "generic",
	})
	if !errors.Is(err, ErrFailedPrecondition) {
		t.Fatalf("err=%v want ErrFailedPrecondition", err)
	}
	// No half-open session should have been persisted.
	all, _ := srv.Sessions.List(context.Background())
	if len(all) != 0 {
		t.Fatalf("spawn failure leaked %d session rows", len(all))
	}
}

func TestOpenSession_DirectModeSkipsSpawn(t *testing.T) {
	srv, _, _ := transcodeSetup(t)
	orch := &fakeOrchestrator{}
	srv.Transcode = orch

	// A directly-playable source (h264/aac mp4) → mode=direct, no ffmpeg.
	fb := probe.NewFakeBackend()
	dr := &probe.Row{
		VideoID: uuid.New(), LibraryID: uuid.New(), Path: "/m/d.mp4",
		Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
		Height: 720, BitrateKbps: 3000, Probed: true,
	}
	fb.Set(dr)
	srv.Probe = probe.NewCache(fb, 16)

	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: dr.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "ios-native",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if resp.Mode != "direct" {
		t.Fatalf("mode=%s want direct", resp.Mode)
	}
	if orch.calls != 0 {
		t.Fatalf("ffmpeg spawned %d times for direct play, want 0", orch.calls)
	}
}

func TestOpenSession_NoOrchestratorIsLegacyNoop(t *testing.T) {
	// When Transcode is nil (e.g. a deployment that hasn't enabled it),
	// OpenSession must still succeed — it just doesn't spawn ffmpeg.
	srv, row, _ := transcodeSetup(t)
	srv.Transcode = nil
	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "generic",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if resp.Mode != "transcode" {
		t.Fatalf("mode=%s want transcode", resp.Mode)
	}
}

func TestOpenSession_ReaperKillsTranscoder(t *testing.T) {
	srv, row, _ := transcodeSetup(t)
	orch := &fakeOrchestrator{}
	srv.Transcode = orch
	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(),
		ClientProfile: "generic",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	// Drive the reaper past the idle horizon — it must Stop the handle.
	reaper := session.NewReaper(srv.Sessions, session.ReaperConfig{
		IdleAfter: time.Millisecond, Interval: time.Hour,
	})
	reaper.SetClock(func() time.Time { return time.Now().Add(time.Hour) })
	if err := reaper.Sweep(context.Background()); err != nil {
		t.Fatalf("reaper sweep: %v", err)
	}
	if orch.stopped == nil || !*orch.stopped {
		t.Fatal("reaper did not kill the ffmpeg handle on idle")
	}
	_ = resp
}

// sanity: the on-disk master playlist the orchestrator writes is
// readable by the manifest layer (single source of truth check).
func TestOrchestratorMasterMatchesHandlerBuilder(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "sess")
	o := &ffmpeg.Orchestrator{Runner: stubRunner{}}
	if _, err := o.Start(context.Background(), ffmpeg.Job{
		SessionID: "sess", InputPath: "/m/x.mkv", OutputDir: out,
		Ladder: ffmpeg.DefaultLadder(0), Format: "hls",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "master.m3u8"))
	if err != nil {
		t.Fatalf("read master: %v", err)
	}
	want := ffmpeg.BuildMasterPlaylistFor(ffmpeg.DefaultLadder(0))
	if string(got) != string(want) {
		t.Fatalf("on-disk master diverges from builder:\n got=%q\nwant=%q", got, want)
	}
}

type stubRunner struct{}

func (stubRunner) Start(context.Context, ffmpeg.Job) (*ffmpeg.Handle, error) {
	return ffmpeg.NewHandleForTest(nil, nil, nil), nil
}
