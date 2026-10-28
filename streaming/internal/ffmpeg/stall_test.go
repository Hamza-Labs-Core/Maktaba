package ffmpeg

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// scriptFFmpeg is a shell script standing in for ffmpeg.
func scriptFFmpeg(t *testing.T, body string) Binary {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for ffmpeg")
	}
	script := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return Binary{FFmpeg: script}
}

// An encoder that writes nothing (its input hangs on a NAS read) is stopped once
// its output has not changed for the stall limit, and logged: its playlist then
// answers 410 and the player reopens, instead of waiting for the idle reap.
func TestExecRunner_StalledEncoderIsStopped(t *testing.T) {
	logs := &syncBuf{}
	r := &execRunner{Bin: scriptFFmpeg(t, "exec sleep 30\n"), Logger: slog.New(slog.NewTextHandler(logs, nil)),
		poll: 20 * time.Millisecond, stall: 400 * time.Millisecond}
	h, err := r.Start(context.Background(), Job{SessionID: uuid.NewString(), InputPath: "/in.mp4",
		OutputDir: t.TempDir(), Ladder: DefaultLadder(0), Format: "hls", SourceCodec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	// Active goes false as the stop begins, Ended once the process is reaped: wait for
	// the end itself (a loaded runner saw the moment between them, 2026-10-28)
	if !waitUntil(10*time.Second, func() bool { return !h.Active() && h.Ended() }) {
		t.Fatal("a stalled encoder ran on")
	}
	out := logs.String()
	for _, want := range []string{"event=vod_transcode_stalled", "level=ERROR", "idle_ms="} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if !h.Ended() || h.Finished() {
		t.Error("the transcode is not over: its playlist would not answer 410")
	}
}

// An encoder that keeps writing is left alone, however long it runs. It watches
// for several stall limits (1.5 s), past the unit tier's cap: it runs in the
// integration tier (`go test` without -short).
func TestExecRunner_WritingEncoderRunsOn(t *testing.T) {
	if testing.Short() {
		t.Skip("watches an encoder for 1.5 s; runs without -short")
	}
	dir := t.TempDir()
	logs := &syncBuf{}
	body := "while true; do date > '" + filepath.Join(dir, "progress") + "'; sleep 0.05; done\n"
	r := &execRunner{Bin: scriptFFmpeg(t, body), Logger: slog.New(slog.NewTextHandler(logs, nil)),
		poll: 20 * time.Millisecond, stall: 400 * time.Millisecond}
	h, err := r.Start(context.Background(), Job{SessionID: uuid.NewString(), InputPath: "/in.mp4",
		OutputDir: dir, Ladder: DefaultLadder(0), Format: "hls", SourceCodec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(1500 * time.Millisecond)
	if !h.Active() || strings.Contains(logs.String(), "vod_transcode_stalled") {
		t.Fatalf("a writing encoder was stopped: %s", logs.String())
	}
}
