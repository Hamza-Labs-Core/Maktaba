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

// Too little free on the cache's disk: no transcode starts, no slot or
// session is held, and the API hears why (not "busy").
func TestOpenSession_NoRoomRefusesTranscode(t *testing.T) {
	srv, row, _ := transcodeSetup(t)
	orch := &fakeOrchestrator{}
	srv.Transcode = orch
	srv.TranscodeRoom = func() error { return errors.New("disk low") }
	for i := 0; i < 3; i++ {
		_, err := srv.OpenSession(context.Background(), OpenSessionRequest{
			VideoID: row.VideoID.String(), UserID: uuid.New().String(), ClientProfile: "generic",
		})
		if !errors.Is(err, ErrNoDiskSpace) {
			t.Fatalf("err=%v want ErrNoDiskSpace", err)
		}
	}
	if all, _ := srv.Sessions.List(context.Background()); len(all) != 0 || orch.lastJob.SessionID != "" {
		t.Fatalf("refused opens left %d sessions, job %+v", len(all), orch.lastJob)
	}
	// room again: the slots were not taken by the refusals
	srv.TranscodeRoom = func() error { return nil }
	if _, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(), ClientProfile: "generic",
	}); err != nil || orch.lastJob.SessionID == "" {
		t.Fatalf("with room: %v, job %+v", err, orch.lastJob)
	}
}

// A transcode slot comes back when its session closes (the API's close, the
// reaper's), and when the open fails after taking it: more sessions than
// slots, opened and closed one after another, all transcode.
func TestOpenSession_SlotsComeBack(t *testing.T) {
	ctx := context.Background()
	srv, row, _ := transcodeSetup(t)
	slotCount := srv.Allocator.MaxConcurrent()
	orch := &fakeOrchestrator{}
	srv.Transcode = orch
	open := func() OpenSessionResponse {
		t.Helper()
		resp, err := srv.OpenSession(ctx, OpenSessionRequest{VideoID: row.VideoID.String(), UserID: uuid.New().String(),
			ClientProfile: "generic"})
		if err != nil || resp.Mode != string(session.ModeTranscode) {
			t.Fatalf("open: %v, mode %q (slots used %d of %d)", err, resp.Mode, srv.Allocator.Used(), slotCount)
		}
		return resp
	}
	for i := 0; i < 3*slotCount; i++ {
		if err := srv.CloseSession(ctx, open().SessionID); err != nil {
			t.Fatal(err)
		}
	}
	if used := srv.Allocator.Used(); used != 0 {
		t.Fatalf("after %d closed sessions %d slots are held", 3*slotCount, used)
	}
	// the reaper: an idle session's close stops its transcode, which gives
	// the slot back
	reaper := session.NewReaper(srv.Sessions, session.ReaperConfig{IdleAfter: time.Minute})
	reaper.SetClock(func() time.Time { return time.Now().Add(time.Hour) })
	for i := 0; i < slotCount; i++ {
		open()
	}
	if err := reaper.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if used := srv.Allocator.Used(); used != 0 {
		t.Fatalf("after the reaper %d slots are held", used)
	}
	// a spawn that fails gives back the slot it took
	orch.failErr = errors.New("ffmpeg: no such encoder")
	for i := 0; i < 3*slotCount; i++ {
		if _, err := srv.OpenSession(ctx, OpenSessionRequest{VideoID: row.VideoID.String(), UserID: uuid.New().String(),
			ClientProfile: "generic"}); !errors.Is(err, ErrFailedPrecondition) {
			t.Fatalf("failed spawn: %v", err)
		}
	}
	if used := srv.Allocator.Used(); used != 0 {
		t.Fatalf("after failed spawns %d slots are held", used)
	}
}

// A player opening the video again because its transcode died (its playlist
// answered 410) gets a new session and transcode, not the dead one; while the
// transcode runs, the same session.
func TestOpenSession_ReopenAfterADeadTranscode(t *testing.T) {
	ctx := context.Background()
	srv, row, _ := transcodeSetup(t)
	orch := &fakeOrchestrator{}
	srv.Transcode = orch
	user := uuid.New().String()
	req := OpenSessionRequest{VideoID: row.VideoID.String(), UserID: user, ClientProfile: "generic", StartSec: 600}
	first, err := srv.OpenSession(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := srv.OpenSession(ctx, req); err != nil || again.SessionID != first.SessionID {
		t.Fatalf("while it runs: %v, %s, want %s", err, again.SessionID, first.SessionID)
	}
	id, _ := uuid.Parse(first.SessionID)
	old, _, _ := srv.Sessions.Get(ctx, id)
	_ = old.Transcoder.Stop(ctx) // dies: over short of the end, the session still open
	again, err := srv.OpenSession(ctx, req)
	if err != nil || again.SessionID == first.SessionID {
		t.Fatalf("after its transcode died: %v, %s", err, again.SessionID)
	}
	if old, _, _ = srv.Sessions.Get(ctx, id); old.ClosedAt == nil || old.ClosedReason != session.ReasonTranscodeFailed {
		t.Errorf("the dead session: closed %v, %q", old.ClosedAt, old.ClosedReason)
	}
	if orch.calls != 2 {
		t.Errorf("transcodes started %d, want 2", orch.calls)
	}
}

// A viewer at a position (a resume, a reopen): an HLS transcode starts there
// and the session says so; a DASH one starts at 0. An open session is handed
// back only when its stream reaches the position (from its start to a minute
// past what it has written); otherwise a new one starts there, beside it.
func TestOpenSession_StartsAtThePosition(t *testing.T) {
	ctx := context.Background()
	srv, row, root := transcodeSetup(t)
	orch := &fakeOrchestrator{}
	srv.Transcode = orch
	user := uuid.New().String()
	open := func(start int, format string) OpenSessionResponse {
		t.Helper()
		resp, err := srv.OpenSession(ctx, OpenSessionRequest{VideoID: row.VideoID.String(), UserID: user,
			ClientProfile: "generic", StartSec: start, Format: format})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	first := open(1213, "")
	if first.StartSec != 1213 || orch.lastJob.StartSec != 1213 {
		t.Fatalf("opened at 1213: session %d, job %d", first.StartSec, orch.lastJob.StartSec)
	}
	// 30 s written: a position up to a minute past them is this session's
	v0 := filepath.Join(root, "hls", first.SessionID, "v0")
	_ = os.MkdirAll(v0, 0o755)
	_ = os.WriteFile(filepath.Join(v0, "index.m3u8"),
		[]byte("#EXTM3U\n#EXTINF:6.000000,\ns0.ts\n#EXTINF:6.000000,\ns1.ts\n#EXTINF:18.000000,\ns2.ts\n"), 0o644)
	for _, at := range []int{1213, 1240, 1300} {
		if again := open(at, ""); again.SessionID != first.SessionID {
			t.Errorf("at %d: a new session, want the open one", at)
		}
	}
	calls := orch.calls
	moved := open(1400, "") // past what it reaches
	if moved.SessionID == first.SessionID || moved.StartSec != 1400 || orch.calls != calls+1 {
		t.Fatalf("at 1400: %+v", moved)
	}
	id, _ := uuid.Parse(first.SessionID)
	if old, _, _ := srv.Sessions.Get(ctx, id); old.ClosedAt != nil {
		t.Errorf("the session at 1213 closed (%q): another device may be playing it", old.ClosedReason)
	}
	back := open(0, "")
	if back.SessionID == moved.SessionID || back.SessionID == first.SessionID || back.StartSec != 0 { // before both starts
		t.Errorf("at 0: %+v", back)
	}
	// their slots back for the DASH one (the setup has two)
	for _, sid := range []string{first.SessionID, moved.SessionID, back.SessionID} {
		_ = srv.CloseSession(ctx, sid)
	}
	other := OpenSessionRequest{VideoID: row.VideoID.String(), UserID: uuid.New().String(), ClientProfile: "generic",
		StartSec: 1213, Format: "dash"}
	if dash, err := srv.OpenSession(ctx, other); err != nil || dash.StartSec != 0 || orch.lastJob.StartSec != 0 {
		t.Errorf("dash: %v, %+v, job %d", err, dash, orch.lastJob.StartSec)
	}
}

// Two devices of one user at different places in the video: each gets its
// own session and keeps it; reopening (a fresh signed URL, a 410) hands each
// back its own, never closing the other's.
func TestOpenSession_TwoDevicesAtTwoPlaces(t *testing.T) {
	ctx := context.Background()
	srv, row, _ := transcodeSetup(t)
	srv.Transcode = &fakeOrchestrator{}
	user := uuid.New().String()
	open := func(start int) string {
		t.Helper()
		resp, err := srv.OpenSession(ctx, OpenSessionRequest{VideoID: row.VideoID.String(), UserID: user,
			ClientProfile: "generic", StartSec: start})
		if err != nil {
			t.Fatal(err)
		}
		return resp.SessionID
	}
	a := open(600)
	b := open(3600)
	if b == a {
		t.Fatal("device B at 3600 got device A's stream from 600")
	}
	for i := 0; i < 3; i++ {
		if again := open(630); again != a {
			t.Fatalf("device A reopening at 630 got %s, want its own %s", again, a)
		}
		if again := open(3610); again != b {
			t.Fatalf("device B reopening at 3610 got %s, want its own %s", again, b)
		}
	}
	for _, sid := range []string{a, b} {
		id, _ := uuid.Parse(sid)
		if r, _, _ := srv.Sessions.Get(ctx, id); r.ClosedAt != nil {
			t.Errorf("%s closed (%q)", sid, r.ClosedReason)
		}
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
