package ffmpeg

import (
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

// An encoder that keeps writing is left alone, however long it runs, and stopped
// once it stops writing. The test steps the watch's clock and ticks itself and
// writes the output at that clock's times: an encoder's own writes race the
// scheduler (a shell loop starved past the stall limit on a loaded runner was
// stopped, rightly, 2026-11-03), and this asks only what the watch decides.
func TestExecRunner_WritingEncoderRunsOn(t *testing.T) {
	dir := t.TempDir()
	logs := &syncBuf{}
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0) // whole seconds: any filesystem keeps them
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) time.Time { mu.Lock(); defer mu.Unlock(); now = now.Add(d); return now }
	ticks := make(chan time.Time)
	t.Cleanup(func() { close(ticks) }) // runs after the Stop below: the watch returns
	// tick hands the watch one tick; false once it has stopped looking (a stall)
	tick := func() bool {
		select {
		case ticks <- clock():
			return true
		case <-time.After(10 * time.Second):
			return false
		}
	}
	const stall = 4 * time.Second
	r := &execRunner{Bin: scriptFFmpeg(t, "exec sleep 30\n"), Logger: slog.New(slog.NewTextHandler(logs, nil)),
		stall: stall, now: clock,
		ticks: func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }}
	h, err := r.Start(context.Background(), Job{SessionID: uuid.NewString(), InputPath: "/in.mp4",
		OutputDir: dir, Ladder: DefaultLadder(0), Format: "hls", SourceCodec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })

	// a write lands with its time already set (made aside, renamed in): the watch
	// never sees the real clock's mtime between the write and the Chtimes
	progress, aside := filepath.Join(dir, "progress"), filepath.Join(t.TempDir(), "progress")
	// ten stall limits of an encoder writing every second
	for i := 0; i < 10*int(stall/time.Second); i++ {
		at := advance(time.Second)
		if err := os.WriteFile(aside, []byte(at.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(aside, at, at); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(aside, progress); err != nil {
			t.Fatal(err)
		}
		if !tick() {
			t.Fatalf("the watch ended after %d s of writes: %s", i+1, logs.String())
		}
	}
	// the watch takes a tick only once it is done with the last (a stall returns)
	if !tick() || !h.Active() || strings.Contains(logs.String(), "vod_transcode_stalled") {
		t.Fatalf("a writing encoder was stopped: %s", logs.String())
	}

	// it stops writing: the stall limit after its last write, it is stopped
	advance(stall - time.Second)
	if !tick() || !h.Active() {
		t.Fatalf("stopped a second inside the stall limit: %s", logs.String())
	}
	advance(time.Second)
	if !tick() {
		t.Fatal("the watch ended before the stall limit")
	}
	if !waitUntil(10*time.Second, func() bool { return !h.Active() && h.Ended() }) {
		t.Fatalf("an encoder that stopped writing ran on: %s", logs.String())
	}
	if out := logs.String(); !strings.Contains(out, "event=vod_transcode_stalled") || !strings.Contains(out, "idle_ms=4000") {
		t.Errorf("the stop is not logged as a stall of 4 s: %s", out)
	}
}
