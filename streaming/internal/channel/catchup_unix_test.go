//go:build unix

package channel

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// -re on ffmpeg 7.1+ reads an input that fell behind at only 1.05x until it has
// caught up: on 2026-10-28 (TV change #53) the staging slot's 28 VideoToolbox
// stations took 7-15 s to open their encoders and stayed that far behind the wall
// clock (channel.position, channel.wallclock), which they would have kept for minutes.
// With -readrate_catchup a block whose ffmpeg stalled is back on the wall clock
// within seconds of the stall.
func TestBlockArgs_LaggingInputCatchesUp(t *testing.T) {
	needFFmpeg(t)
	if !ReadrateCatchupFor("ffmpeg") {
		t.Log("this ffmpeg has no -readrate_catchup (before 7.1): -re catches up on its own")
	}
	p := OutputProfile{Width: 320, Height: 180, FPS: 30, VideoKbps: 300, Encoder: EncoderSoftware,
		ReadrateCatchup: ReadrateCatchupFor("ffmpeg")}
	args := BlockArgs(p, BlockInput{Kind: "slate", DurationSec: 20})
	if p.ReadrateCatchup && !strings.Contains(joined(args), "-re -readrate_catchup "+readrateCatchup+" -f lavfi") {
		t.Fatalf("the input has no catch-up rate: %s", joined(args))
	}
	dir := t.TempDir()
	prog := filepath.Join(dir, "progress")
	args[len(args)-1] = filepath.Join(dir, "out.ts")
	cmd := exec.Command("ffmpeg", append([]string{"-y", "-progress", prog}, args...)...) //nolint:gosec // test
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(1500 * time.Millisecond)
	// the stall: an encoder opening, a share not answering
	if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	if err := cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(6 * time.Second)
	behind := time.Since(start).Seconds() - outTime(t, prog)
	t.Logf("%.2f s behind the wall clock 6 s after a 5 s stall", behind)
	// ffmpeg's own start-up counts too (well under a second here); 1.05x would
	// still be ~4.7 s behind
	if behind > 1.5 {
		t.Errorf("the output stayed %.2f s behind the wall clock after the stall", behind)
	}
}

// outTime is ffmpeg's last reported output position (s).
func outTime(t *testing.T, path string) float64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	last := 0.0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "out_time_us="); ok {
			if us, err := strconv.ParseFloat(v, 64); err == nil {
				last = us / 1e6
			}
		}
	}
	return last
}
