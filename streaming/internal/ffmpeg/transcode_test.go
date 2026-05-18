package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner stands in for the real ffmpeg-spawning Runner: it writes a
// canned variant index + segment so the full manifest→segment serving
// path can be exercised without exec'ing ffmpeg.
type fakeRunner struct {
	started bool
	job     Job
	failErr error
}

func (f *fakeRunner) Start(_ context.Context, job Job) (*Handle, error) {
	if f.failErr != nil {
		return nil, f.failErr
	}
	f.started = true
	f.job = job
	// Emit v0/index.m3u8 + one segment so a player would get bytes.
	if len(job.Ladder) > 0 {
		vdir := filepath.Join(job.OutputDir, job.Ladder[0].Name)
		_ = os.MkdirAll(vdir, 0o755)
		_ = os.WriteFile(filepath.Join(vdir, "index.m3u8"),
			[]byte("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.0,\nseg-0.ts\n"), 0o644)
		_ = os.WriteFile(filepath.Join(vdir, "seg-0.ts"), []byte("TSDATA"), 0o644)
	}
	stopped := false
	return NewHandleForTest(
		func(context.Context) error { stopped = true; return nil },
		func() int { return 4242 },
		func() bool { return !stopped },
	), nil
}

func TestOrchestrator_WritesMasterAndSpawns(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "hls", "sess-1")
	fr := &fakeRunner{}
	o := &Orchestrator{Runner: fr}

	h, err := o.Start(context.Background(), Job{
		SessionID: "sess-1",
		InputPath: "/media/x.mkv",
		OutputDir: out,
		Ladder:    DefaultLadder(0),
		Format:    "hls",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !fr.started {
		t.Fatal("runner.Start was never called — ffmpeg seam not invoked")
	}
	// Master playlist must exist on disk so the manifest GET resolves.
	master, err := os.ReadFile(filepath.Join(out, "master.m3u8"))
	if err != nil {
		t.Fatalf("master playlist not written: %v", err)
	}
	if !strings.Contains(string(master), "#EXT-X-STREAM-INF") {
		t.Fatalf("master playlist malformed:\n%s", master)
	}
	if h.PID() != 4242 {
		t.Fatalf("pid=%d want 4242 (handle not wired)", h.PID())
	}
	if !h.Active() {
		t.Fatal("handle should be active before Stop")
	}
	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if h.Active() {
		t.Fatal("handle should be inactive after Stop")
	}
}

func TestOrchestrator_DASHSkipsMasterPlaylist(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "hls", "sess-d")
	o := &Orchestrator{Runner: &fakeRunner{}}
	if _, err := o.Start(context.Background(), Job{
		SessionID: "sess-d", InputPath: "/m/x.mkv", OutputDir: out,
		Ladder: DefaultLadder(0), Format: "dash",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "master.m3u8")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("DASH must not write an HLS master playlist")
	}
}

func TestOrchestrator_RunnerFailurePropagates(t *testing.T) {
	o := &Orchestrator{Runner: &fakeRunner{failErr: errors.New("boom")}}
	_, err := o.Start(context.Background(), Job{
		SessionID: "s", InputPath: "/m/x.mkv", OutputDir: t.TempDir(),
		Ladder: DefaultLadder(0), Format: "hls",
	})
	if err == nil {
		t.Fatal("expected runner failure to propagate")
	}
}

func TestOrchestrator_RejectsEmptyInputs(t *testing.T) {
	o := &Orchestrator{Runner: &fakeRunner{}}
	if _, err := o.Start(context.Background(), Job{OutputDir: t.TempDir()}); err == nil {
		t.Fatal("expected error for empty input path")
	}
	if _, err := o.Start(context.Background(), Job{InputPath: "/x"}); err == nil {
		t.Fatal("expected error for empty output dir")
	}
}

func TestExecRunner_BuildsArgsAndExecsBinary(t *testing.T) {
	// Real Runner path: point FFmpeg at a tiny shell script that just
	// exits 0 — proves execRunner actually spawns the configured binary
	// (the production path) without depending on a real ffmpeg install.
	dir := t.TempDir()
	script := filepath.Join(dir, "fakeffmpeg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	er := &execRunner{Bin: Binary{FFmpeg: script}}
	h, err := er.Start(context.Background(), Job{
		SessionID: "s", InputPath: "/m/x.mkv",
		OutputDir: filepath.Join(dir, "out"), Ladder: DefaultLadder(0), Format: "hls",
	})
	if err != nil {
		t.Fatalf("execRunner.Start: %v", err)
	}
	if err := h.Wait(); err != nil {
		t.Fatalf("fake ffmpeg exited non-zero: %v", err)
	}
}

// Wait is a test convenience exposing the underlying process wait.
func (h *Handle) Wait() error {
	if h.proc != nil {
		return h.proc.Wait()
	}
	return nil
}
