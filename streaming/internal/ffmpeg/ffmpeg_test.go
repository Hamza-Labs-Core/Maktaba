package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProcessStop_SIGTERMGracefulThenKill covers HLB-328 I2. Stop must
// first SIGTERM (so ffmpeg can finalize the current segment/playlist),
// wait a bounded grace window, then escalate to SIGKILL if the child is
// still alive. We prove both arms with real OS processes.
func TestProcessStop_SIGTERMGracefulThenKill(t *testing.T) {
	// Shrink the grace window so the SIGKILL-escalation arm stays fast.
	old := stopGrace
	stopGrace = 300 * time.Millisecond
	t.Cleanup(func() { stopGrace = old })

	t.Run("graceful exit on SIGTERM (no SIGKILL needed)", func(t *testing.T) {
		dir := t.TempDir()
		// Default SIGTERM disposition terminates `sleep`, so this child
		// exits promptly on the TERM — well inside the grace window.
		script := filepath.Join(dir, "term-graceful")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		p, err := Spawn(context.Background(), Binary{FFmpeg: script}, Args{"x"})
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if err := p.Stop(context.Background()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		// Must have exited via the grace window, not after it (i.e. no
		// SIGKILL escalation was required).
		if elapsed := time.Since(start); elapsed >= stopGrace {
			t.Fatalf("Stop took %v (>= grace %v) — child did not exit on "+
				"SIGTERM as expected", elapsed, stopGrace)
		}
		if p.Active() {
			t.Fatal("process still active after graceful Stop")
		}
		// Idempotent.
		if err := p.Stop(context.Background()); err != nil {
			t.Fatalf("second Stop not idempotent: %v", err)
		}
	})

	t.Run("escalates to SIGKILL when SIGTERM ignored", func(t *testing.T) {
		dir := t.TempDir()
		// Trap SIGTERM and keep running — only SIGKILL can stop this.
		// The child touches `ready` *after* the trap is installed so the
		// test can wait for that before sending the signal — otherwise a
		// TERM delivered before `sh` parses the trap hits the default
		// disposition and the child dies early (flaky). Real ffmpeg
		// likewise has its signal handler up by the time it produces a
		// segment, so gating on readiness models production faithfully.
		script := filepath.Join(dir, "term-ignorer")
		ready := filepath.Join(dir, "ready")
		body := "#!/bin/sh\ntrap '' TERM\ntouch '" + ready + "'\nwhile true; do sleep 1; done\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		p, err := Spawn(context.Background(), Binary{FFmpeg: script}, Args{"x"})
		if err != nil {
			t.Fatal(err)
		}
		if !waitUntilFile(2*time.Second, ready) {
			t.Fatal("child never signalled readiness (trap not installed)")
		}
		start := time.Now()
		if err := p.Stop(context.Background()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		// Should only have exited after the grace window elapsed and the
		// SIGKILL landed — proving the TERM-then-grace-then-KILL order.
		if elapsed := time.Since(start); elapsed < stopGrace {
			t.Fatalf("Stop returned in %v (< grace %v) — SIGTERM-ignoring "+
				"child should have required the full grace window before "+
				"SIGKILL", elapsed, stopGrace)
		}
		if p.Active() {
			t.Fatal("SIGTERM-ignoring process still active after Stop — " +
				"SIGKILL escalation did not happen")
		}
	})
}

// waitUntilFile polls for the existence of path until it appears or the
// deadline elapses. Small bounded sleeps keep the stop tests fast and
// non-flaky.
func waitUntilFile(d time.Duration, path string) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err := os.Stat(path)
	return err == nil
}

func TestRemuxArgs_CopyOnly(t *testing.T) {
	args := RemuxArgs("/in.mkv", "/out.mp4")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c copy") {
		t.Fatalf("missing copy: %s", joined)
	}
	if !strings.Contains(joined, "-movflags +faststart+frag_keyframe+empty_moov") {
		t.Fatalf("missing movflags: %s", joined)
	}
	if !strings.Contains(joined, "/in.mkv") || !strings.Contains(joined, "/out.mp4") {
		t.Fatalf("missing paths: %s", joined)
	}
}

func TestHLSArgs_HasLadderRungs(t *testing.T) {
	ladder := DefaultLadder(0)
	args := HLSArgs("/in.mp4", "/out", "master.m3u8", ladder, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f hls") {
		t.Fatalf("missing -f hls")
	}
	for i := range ladder {
		if !strings.Contains(joined, "-c:v:"+itoa(i)) {
			t.Fatalf("missing video output for rung %d", i)
		}
	}
}

func TestHLSArgs_HWAccelEncoder(t *testing.T) {
	args := HLSArgs("/in.mp4", "/out", "master.m3u8", DefaultLadder(0), "videotoolbox")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "h264_videotoolbox") {
		t.Fatalf("expected h264_videotoolbox encoder: %s", joined)
	}
}

func TestDASHArgs_OutputsMPD(t *testing.T) {
	args := DASHArgs("/in.mp4", "/out", DefaultLadder(0), "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f dash") {
		t.Fatalf("missing -f dash")
	}
	if !strings.Contains(joined, "manifest.mpd") {
		t.Fatalf("missing mpd output: %s", joined)
	}
}

func TestDefaultLadder_MaxBitrateTrim(t *testing.T) {
	l := DefaultLadder(2000)
	if len(l) != 1 {
		t.Fatalf("got %d rungs", len(l))
	}
	if l[0].BitrateKbps != 1200 {
		t.Fatalf("kept %dkbps, expected 1200", l[0].BitrateKbps)
	}
}

func TestDefaultLadder_TooLowKeepsSmallest(t *testing.T) {
	l := DefaultLadder(200)
	if len(l) != 1 {
		t.Fatalf("got %d rungs, want 1 (forced lowest)", len(l))
	}
	if l[0].Name != "v2" {
		t.Fatalf("rung=%s", l[0].Name)
	}
}

func TestDetector_DarwinPrefersVideoToolbox(t *testing.T) {
	d := &Detector{
		Bin:  DefaultBinary(),
		GOOS: "darwin",
		Encoders: func(_ context.Context, _ string) ([]string, error) {
			return []string{"libx264", "h264_videotoolbox"}, nil
		},
		NVIDIASmiOK: func(_ context.Context) bool { return false },
		QuickSyncOK: func(_ context.Context) bool { return false },
	}
	got, err := d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != HWAccelVideoToolbox {
		t.Fatalf("got %s want videotoolbox", got)
	}
}

func TestDetector_LinuxNVIDIAPrefersNVENC(t *testing.T) {
	d := &Detector{
		Bin:  DefaultBinary(),
		GOOS: "linux",
		Encoders: func(_ context.Context, _ string) ([]string, error) {
			return []string{"libx264", "h264_nvenc", "h264_qsv"}, nil
		},
		NVIDIASmiOK: func(_ context.Context) bool { return true },
		QuickSyncOK: func(_ context.Context) bool { return true },
	}
	got, _ := d.Detect(context.Background())
	if got != HWAccelNVENC {
		t.Fatalf("got %s want nvenc", got)
	}
}

func TestDetector_LinuxNoGPUFallsBackToSoftware(t *testing.T) {
	d := &Detector{
		Bin:  DefaultBinary(),
		GOOS: "linux",
		Encoders: func(_ context.Context, _ string) ([]string, error) {
			return []string{"libx264"}, nil
		},
		NVIDIASmiOK: func(_ context.Context) bool { return false },
		QuickSyncOK: func(_ context.Context) bool { return false },
	}
	got, _ := d.Detect(context.Background())
	if got != HWAccelSoftware {
		t.Fatalf("got %s want software", got)
	}
}

func TestDetector_NoFFmpegFallsBackToSoftware(t *testing.T) {
	d := &Detector{
		Bin:  DefaultBinary(),
		GOOS: "darwin",
		Encoders: func(_ context.Context, _ string) ([]string, error) {
			return nil, errReadable
		},
	}
	got, _ := d.Detect(context.Background())
	if got != HWAccelSoftware {
		t.Fatalf("got %s want software fallback", got)
	}
}

var errReadable = readableError("ffmpeg not on PATH")

type readableError string

func (e readableError) Error() string { return string(e) }
