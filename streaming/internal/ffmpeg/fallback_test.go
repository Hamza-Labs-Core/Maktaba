package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeNVENCFFmpeg is an ffmpeg whose NVENC the driver refuses: a run with
// h264_nvenc fails at once, as a full card's does; any other runs on. Each
// run's arguments go into calls, a line each.
func fakeNVENCFFmpeg(t *testing.T) (bin Binary, calls func() []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for ffmpeg")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "ffmpeg")
	body := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" +
		"case \"$*\" in *h264_nvenc*) echo 'OpenEncodeSessionEx failed: out of memory (10): (no details)' >&2; exit 1;; esac\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return Binary{FFmpeg: script}, func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

// A hardware run that fails at once (the driver refused the session) is run
// again in software, in the same session; its gate sessions come back, marked
// refused (the gate pauses NVENC for the playout too).
func TestExecRunner_HardwareFailureRunsAgainInSoftware(t *testing.T) {
	bin, calls := fakeNVENCFFmpeg(t)
	gate := &countGate{max: 6}
	r := &execRunner{Bin: bin, Gates: map[HWAccel]SessionGate{HWAccelNVENC: gate}}
	h, err := r.Start(context.Background(), Job{SessionID: uuid.NewString(), InputPath: "/in.mp4", OutputDir: t.TempDir(),
		Ladder: DefaultLadder(0), HWAccel: "nvenc", Format: "hls", SourceCodec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	if !waitUntil(20*time.Second, func() bool { return len(calls()) == 2 && h.PID() > 0 }) {
		t.Fatalf("calls %q, pid %d, stderr %q", calls(), h.PID(), h.Stderr())
	}
	c := calls()
	if !strings.Contains(c[0], "-c:v:0 h264_nvenc") || !strings.Contains(c[0], "-hwaccel cuda") {
		t.Errorf("first run %q", c[0])
	}
	if strings.Contains(c[1], "nvenc") || strings.Contains(c[1], "-hwaccel") || strings.Count(c[1], "libx264") != 3 {
		t.Errorf("second run %q", c[1])
	}
	if !h.Active() {
		t.Error("the session's software run is not running")
	}
	if held, refused := gate.counts(); held != 0 || refused != 3 {
		t.Errorf("gate holds %d, refused %d; want 0, 3", held, refused)
	}
	if err := h.Stop(context.Background()); err != nil || h.Active() {
		t.Fatalf("stop: %v, active %v", err, h.Active())
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(calls()); n != 2 {
		t.Errorf("%d runs after Stop", n)
	}
}

// A gate with no session left: the ladder is libx264's from the start, nothing
// fails, nothing is refused.
func TestExecRunner_FullGateEncodesInSoftware(t *testing.T) {
	bin, calls := fakeNVENCFFmpeg(t)
	gate := &countGate{max: 0}
	r := &execRunner{Bin: bin, Gates: map[HWAccel]SessionGate{HWAccelNVENC: gate}}
	h, err := r.Start(context.Background(), Job{SessionID: uuid.NewString(), InputPath: "/in.mp4", OutputDir: t.TempDir(),
		Ladder: DefaultLadder(0), HWAccel: "nvenc", Format: "dash", SourceCodec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	if !waitUntil(20*time.Second, func() bool { return len(calls()) == 1 && calls()[0] != "" }) {
		t.Fatalf("calls %q", calls())
	}
	if c := calls()[0]; strings.Contains(c, "nvenc") || strings.Contains(c, "-hwaccel") {
		t.Errorf("run %q", c)
	}
	if _, refused := gate.counts(); refused != 0 || !h.Active() {
		t.Errorf("refused %d, active %v", refused, h.Active())
	}
}
