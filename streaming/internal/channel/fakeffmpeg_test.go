package channel

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The test binary doubles as a fake ffmpeg/ffprobe so the station's
// process plumbing (muxer supervision, block forwarding, pacing, the TS
// fan-out, Stop) runs in the -short unit tier without a real ffmpeg.
// Mode comes from MAKTABA_FAKE_FFMPEG:
//
//	1      normal: probe prints streams, blocks emit TS, muxer copies + writes HLS
//	wedge  the muxer never reads stdin (a hung ffmpeg)
//	nvencfail  as 1, but a block encoding with h264_nvenc fails at once
const fakeFFmpegEnv = "MAKTABA_FAKE_FFMPEG"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeFFmpegEnv); mode != "" {
		os.Exit(fakeFFmpeg(mode, os.Args[1:]))
	}
	// never this machine's real node config (a joined machine's holds a
	// database credential and makes DetectRole/LocalIdentity say "node"):
	// a path that never exists; tests that need one set their own
	dir, err := os.MkdirTemp("", "maktaba-nodecfg-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(dir, "node.json"))
	_ = os.Unsetenv("MAKTABA_TV_HOST_ROLE")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func hasArg(args []string, a string) bool {
	for _, x := range args {
		if x == a {
			return true
		}
	}
	return false
}

func lastArgAfter(args []string, flag string) string {
	v := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			v = args[i+1]
		}
	}
	return v
}

func fakeFFmpeg(mode string, args []string) int {
	switch {
	case hasArg(args, "-show_entries"): // ffprobe
		_, _ = fmt.Fprintln(os.Stdout, "video")
		_, _ = fmt.Fprintln(os.Stdout, "audio")
		return 0
	case hasArg(args, "pipe:0"): // the channel muxer
		if mode == "wedge" {
			time.Sleep(time.Hour)
			return 0
		}
		dir := ""
		for _, a := range args {
			if strings.HasSuffix(a, "/index.m3u8") {
				dir = strings.TrimSuffix(a, "/index.m3u8")
			}
		}
		return fakeMuxer(dir)
	case hasArg(args, "pipe:1") && mode == "nvencfail" && hasArg(args, "h264_nvenc"):
		// the driver refuses the session before any output
		_, _ = fmt.Fprintln(os.Stderr, "[h264_nvenc] OpenEncodeSessionEx failed: incompatible client key (21)")
		return 1
	case hasArg(args, "pipe:1"): // a block
		dur, _ := strconv.ParseFloat(lastArgAfter(args, "-t"), 64)
		off, _ := strconv.ParseFloat(lastArgAfter(args, "-output_ts_offset"), 64)
		w := bufio.NewWriter(os.Stdout)
		frames := int(dur * 30)
		filler := make([]byte, 30*tsPacket) // non-PES packets (~"video payload")
		for i := 0; i < frames; i++ {
			pts := int64((off+1.4)*90000) + int64(i)*3000
			_, _ = w.Write(pesPacket(pts))
			_, _ = w.Write(filler)
			if i%3 == 2 { // emit in real time, like ffmpeg -re
				_ = w.Flush()
				time.Sleep(100 * time.Millisecond)
			}
		}
		_ = w.Flush()
		return 0
	}
	return 2
}

// fakeMuxer copies stdin to stdout and cuts a "segment" per 64 KiB.
func fakeMuxer(dir string) int {
	buf := make([]byte, 64*1024)
	seg := 0
	var names []string
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			_, _ = os.Stdout.Write(buf[:n])
			seg++
			name := "seg-" + strconv.Itoa(seg) + ".ts"
			_ = os.WriteFile(filepath.Join(dir, name), buf[:n], 0o644)
			names = append(names, name)
			pl := "#EXTM3U\n#EXT-X-TARGETDURATION:2\n"
			for _, nm := range names {
				pl += "#EXTINF:1.0,\n" + nm + "\n"
			}
			_ = os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte(pl), 0o644)
		}
		if err != nil {
			return 0
		}
	}
}

func fakeStation(t *testing.T, mode string, blocks []ProgramBlock) *Station {
	t.Helper()
	t.Setenv(fakeFFmpegEnv, mode)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "hls")
	st, err := StartStation(StationConfig{
		FFmpeg: bin, Profile: OutputProfile{Width: 320, Height: 180, FPS: 30, Encoder: EncoderSoftware,
			SegmentSeconds: 1, HLSListSize: 1},
		Repo: &syncRepo{blocks: blocks}, Prober: stubProber{}, Resolver: NewYtDlp(""),
		Logger: testLogger(t),
	}, uuidFor(t), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.Stop()
		select {
		case <-st.Done():
		case <-time.After(5 * time.Second):
			t.Error("station did not stop")
		}
	})
	return st
}

func TestStation_FakeFFmpeg_AirsBlocksIntoHLSAndTS(t *testing.T) {
	now := time.Now()
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	_ = os.WriteFile(clip, []byte("x"), 0o644)
	st := fakeStation(t, "1", []ProgramBlock{
		// joined ~9 s into a 14 s program (ends after the test, so nothing is queued ahead)
		{Seq: 1, Kind: KindProgram, Path: clip, StartAt: now.Add(-9 * time.Second), EndAt: now.Add(5 * time.Second),
			SourceDurationMS: 14_000},
		{Seq: 2, Kind: KindSlate, StartAt: now.Add(5 * time.Second), EndAt: now.Add(time.Minute)},
	})
	sub, unsub := st.Subscribe()
	defer unsub()
	ctx, cancel := contextWithTimeout(3 * time.Second)
	defer cancel()
	if err := st.WaitReady(ctx); err != nil {
		t.Fatalf("ready: %v", err)
	}
	select {
	case b := <-sub:
		if len(b) == 0 || len(b)%tsPacket != 0 || b[0] != 0x47 {
			t.Errorf("TS fan-out chunk not packet aligned (%d bytes)", len(b))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no TS data")
	}
	if st.PID() == 0 || !st.Alive() {
		t.Error("station not running")
	}
	if !waitForSoft(2*time.Second, func() bool {
		b, _ := os.ReadFile(filepath.Join(st.Dir(), "blocks.log"))
		return strings.Contains(string(b), " file src=")
	}) {
		t.Error("the program block did not air")
	}
	if t.Failed() {
		b, _ := os.ReadFile(filepath.Join(st.Dir(), "blocks.log"))
		t.Logf("blocks.log:\n%s", b)
	}
	if r := st.reached(); r <= 0 {
		t.Errorf("reached timeline not tracked: %v", r)
	}
}

// A swap's generation N+1 ends the programme on the air at boundary_max_wait
// (tvrelease generation.py shortens its end_at and leaves its source_duration,
// the whole programme's). The station must stop that programme at the block's
// end, not play the source on, and start the next block of the new schedule
// exactly there: the front door switches the channel at that instant.
func TestStation_FakeFFmpeg_CappedProgrammeEndsAtItsBlockEnd(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	long, next := filepath.Join(dir, "long.mp4"), filepath.Join(dir, "next.mp4")
	_ = os.WriteFile(long, []byte("x"), 0o644)
	_ = os.WriteFile(next, []byte("x"), 0o644)
	capAt := now.Add(1500 * time.Millisecond)
	st := fakeStation(t, "1", []ProgramBlock{
		// a two-hour programme 20 min in, capped 1.5 s from now (the next block is
		// queued ahead at once: the test does not wait for the boundary)
		{Seq: 1, Kind: KindProgram, Path: long, StartAt: now.Add(-20 * time.Minute), EndAt: capAt,
			SourceDurationMS: int((2 * time.Hour) / time.Millisecond)},
		{Seq: 2, Kind: KindProgram, Path: next, StartAt: capAt, EndAt: capAt.Add(time.Minute),
			SourceDurationMS: 60_000},
	})
	type launch struct {
		src      string
		dur, off float64
	}
	read := func() []launch {
		b, _ := os.ReadFile(filepath.Join(st.Dir(), "blocks.log"))
		var out []launch
		for _, l := range strings.Split(string(b), "\n") {
			i := strings.Index(l, " file src=")
			if i < 0 {
				continue
			}
			var x launch
			f := strings.Fields(l[i+len(" file "):])
			for _, kv := range f {
				k, v, _ := strings.Cut(kv, "=")
				switch k {
				case "src":
					x.src, _ = strconv.Unquote(v)
				case "dur":
					x.dur, _ = strconv.ParseFloat(v, 64)
				case "off":
					x.off, _ = strconv.ParseFloat(v, 64)
				}
			}
			out = append(out, x)
		}
		return out
	}
	var capped, after *launch
	waitForSoft(3*time.Second, func() bool {
		capped, after = nil, nil
		for _, l := range read() {
			l := l
			switch {
			case strings.HasSuffix(l.src, "long.mp4"):
				capped = &l
			case strings.HasSuffix(l.src, "next.mp4"):
				after = &l
			}
		}
		return capped != nil && after != nil
	})
	if capped == nil || after == nil {
		t.Fatalf("blocks launched: capped %+v, next %+v", capped, after)
	}
	// the capped programme runs only to its block's end (it joined just now)
	if capped.dur <= 0 || capped.dur > capAt.Sub(now).Seconds()+0.05 {
		t.Errorf("the capped programme runs %.3f s, want at most %.3f s (its block's end, not its source)",
			capped.dur, capAt.Sub(now).Seconds())
	}
	// the new schedule's first block starts at the cap, on the wall-clock grid
	want := float64(TSOffsetTicks(AlignStart(capAt, 30))) / 90000
	if d := after.off - want; d < -0.001 || d > 0.001 {
		t.Errorf("the next block starts at offset %.3f, want %.3f (the capped end)", after.off, want)
	}
}

func TestStation_FakeFFmpeg_MuxerRestartsOnItsOwn(t *testing.T) {
	now := time.Now()
	st := fakeStation(t, "1", []ProgramBlock{{Seq: 1, Kind: KindSlate, StartAt: now, EndAt: now.Add(time.Minute)}})
	waitForT(t, 2*time.Second, func() bool { return st.PID() != 0 })
	old := st.PID()
	st.muxMu.Lock()
	_ = st.mux.cmd.Process.Kill()
	st.muxMu.Unlock()
	waitForT(t, 3*time.Second, func() bool { return st.MuxerRestarts() == 1 && st.PID() != 0 })
	if st.PID() == old || !st.Alive() {
		t.Fatal("muxer was not replaced")
	}
}

func TestStation_FakeFFmpeg_StopNeverWaitsOnAWedgedMuxer(t *testing.T) {
	now := time.Now()
	st := fakeStation(t, "wedge", []ProgramBlock{{Seq: 1, Kind: KindSlate, StartAt: now, EndAt: now.Add(time.Minute)}})
	// Let a block fill the pipe the wedged muxer never drains.
	time.Sleep(150 * time.Millisecond)
	stopped := make(chan struct{})
	go func() { st.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked on a wedged muxer")
	}
	select {
	case <-st.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("station never finished after Stop")
	}
}

func uuidFor(*testing.T) uuid.UUID { return uuid.New() }

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func waitForSoft(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// testLogger routes station logs to the test log (warnings and up).
func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(b), "\n"))
	return len(b), nil
}

// stubProber reports video+audio without spawning ffprobe.
type stubProber struct{}

func (stubProber) Probe(context.Context, string) (MediaInfo, error) {
	return MediaInfo{HasVideo: true, HasAudio: true}, nil
}

func TestFFprobe_FakeBinary(t *testing.T) {
	t.Setenv(fakeFFmpegEnv, "1")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := NewFFprobe(bin).Probe(context.Background(), "file:/x.mp4")
	if err != nil || !info.HasVideo || !info.HasAudio {
		t.Fatalf("probe = %+v %v", info, err)
	}
}
