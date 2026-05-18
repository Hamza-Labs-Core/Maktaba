package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pidAlive reports whether an OS process with pid is still running.
// signal 0 performs error checking without delivering a signal: ESRCH
// means the process is gone, nil/EPERM means it is still alive.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// waitUntil polls cond every 10ms until it holds or the deadline
// elapses; returns the final cond value. Bounded + small sleeps keep
// the lifetime tests fast and non-flaky.
func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestExecRunner_ChildOutlivesRequestCtx is the structural regression
// for HLB-328 C1: the ffmpeg child MUST NOT be parented to the inbound
// OpenSession request ctx. grpc-go cancels that ctx the instant the RPC
// returns; pre-fix exec.CommandContext(ctx,...) SIGKILLed ffmpeg within
// ms, so no variant playlist/segments ever materialised and the client
// polled forever.
//
// We spawn a REAL long-lived child (a sleep script) through the real
// execRunner/Spawn path with a CANCELLABLE PARENT ctx (the stand-in for
// the request ctx). After Start returns we cancel that parent ctx and
// assert the child is STILL ALIVE — it does not die with the request.
// Then Handle.Stop() must terminate it. Pre-fix this FAILS (child dies
// on parent-ctx cancel); post-fix it PASSES.
func TestExecRunner_ChildOutlivesRequestCtx(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fakeffmpeg")
	// Long-lived: sleep so the child stays up well past the test window
	// unless something signals it.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	er := &execRunner{Bin: Binary{FFmpeg: script}}

	parentCtx, cancelParent := context.WithCancel(context.Background())
	h, err := er.Start(parentCtx, Job{
		SessionID: "s", InputPath: "/m/x.mkv",
		OutputDir: filepath.Join(dir, "out"), Ladder: DefaultLadder(0), Format: "hls",
	})
	if err != nil {
		t.Fatalf("execRunner.Start: %v", err)
	}
	pid := h.PID()
	if pid <= 0 || !pidAlive(pid) {
		t.Fatalf("child not running after Start (pid=%d)", pid)
	}

	// Simulate OpenSession returning: grpc-go cancels the request ctx.
	cancelParent()

	// The child MUST survive request-ctx cancellation. Give it a bounded
	// window and assert it stays alive the whole time (pre-fix it dies
	// almost immediately via exec.CommandContext).
	if waitUntil(750*time.Millisecond, func() bool { return !pidAlive(pid) }) {
		t.Fatalf("child pid=%d died on parent-ctx cancel — encoder is "+
			"parented to the request ctx (HLB-328 C1 regression)", pid)
	}

	// Now the session owner stops it: Handle.Stop must terminate it.
	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Handle.Stop: %v", err)
	}
	if !waitUntil(10*time.Second, func() bool { return !pidAlive(pid) }) {
		t.Fatalf("child pid=%d still alive after Handle.Stop", pid)
	}
}

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
