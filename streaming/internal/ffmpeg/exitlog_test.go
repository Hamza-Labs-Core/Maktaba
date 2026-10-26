package ffmpeg

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A software encoder that fails is logged with its exit status and the last
// of its stderr; one that was stopped is logged as stopped.
func TestExecRunner_LogsHowTheEncoderEnded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for ffmpeg")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "ffmpeg")
	body := "#!/bin/sh\n" +
		"case \"$*\" in *bad.mp4*) echo 'Invalid data found when processing input' >&2; exit 183;; esac\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf syncBuf
	r := &execRunner{Bin: Binary{FFmpeg: script}, Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	job := func(in string) Job {
		return Job{SessionID: uuid.NewString(), InputPath: in, OutputDir: t.TempDir(),
			Ladder: DefaultLadder(0), Format: "hls", SourceCodec: "h264"}
	}

	bad := job("/nas/bad.mp4")
	h, err := r.Start(context.Background(), bad)
	if err != nil {
		t.Fatal(err)
	}
	if !waitUntil(10*time.Second, h.Ended) {
		t.Fatal("the failing encoder did not end")
	}
	if !waitUntil(5*time.Second, func() bool { return strings.Contains(buf.String(), "outcome=failed") }) {
		t.Fatalf("no failure line: %s", buf.String())
	}
	out := buf.String()
	for _, want := range []string{"event=vod_transcode_exited", "level=WARN", "session_id=" + bad.SessionID,
		"input=/nas/bad.mp4", "exit_code=183", "Invalid data found when processing input"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}

	good := job("/nas/ok.mp4")
	h2, err := r.Start(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	if !waitUntil(10*time.Second, func() bool { return h2.PID() > 0 }) {
		t.Fatal("encoder did not start")
	}
	_ = h2.Stop(context.Background())
	if !waitUntil(5*time.Second, func() bool {
		return strings.Contains(buf.String(), "session_id="+good.SessionID) &&
			strings.Contains(buf.String(), "outcome=stopped")
	}) {
		t.Fatalf("no stopped line: %s", buf.String())
	}
}

func TestTailLines(t *testing.T) {
	if got := tailLines("a\n\nb\nc\n", 2); got != "b\nc" {
		t.Fatalf("tailLines = %q", got)
	}
}
