package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	args := HLSArgs("/in.mp4", "/out", "master.m3u8", ladder, SoftwarePlan(len(ladder), true))
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f hls") {
		t.Fatalf("missing -f hls")
	}
	for i := range ladder {
		if !strings.Contains(joined, "-c:v:"+itoa(i)) {
			t.Fatalf("missing video output for rung %d", i)
		}
	}
	// one variant per rung (v0, v1, v2 as the master playlist lists them), each with
	// the audio: without the map every rung went into v0/ and v1/, v2/ 404'd
	if got := argAfter(args, "-var_stream_map"); got != "v:0,a:0 v:1,a:1 v:2,a:2" {
		t.Errorf("-var_stream_map %q", got)
	}
	if n := strings.Count(joined, "-map 0:a:0"); n != len(ladder) {
		t.Errorf("audio mapped %d times, want once per rung", n)
	}
	// a source without audio maps none
	silent := HLSArgs("/in.mp4", "/out", "master.m3u8", ladder, SoftwarePlan(len(ladder), false))
	if got := argAfter(silent, "-var_stream_map"); got != "v:0 v:1 v:2" || strings.Contains(strings.Join(silent, " "), "0:a:0") {
		t.Errorf("silent source: %v", silent)
	}
}

func TestHLSArgs_HWAccelEncoder(t *testing.T) {
	args := HLSArgs("/in.mp4", "/out", "master.m3u8", DefaultLadder(0), PlanFor("videotoolbox", 3, 3, "h264", true))
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "h264_videotoolbox") {
		t.Fatalf("expected h264_videotoolbox encoder: %s", joined)
	}
}

func TestDASHArgs_OutputsMPD(t *testing.T) {
	args := DASHArgs("/in.mp4", "/out", DefaultLadder(0), SoftwarePlan(3, true))
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

// detector answers -encoders with encs and a test encode with works.
func detector(goos string, encs []string, works map[string]bool) *Detector {
	return &Detector{
		Bin:      DefaultBinary(),
		GOOS:     goos,
		Encoders: func(context.Context, string) ([]string, error) { return encs, nil },
		Works:    func(_ context.Context, _, enc string) (bool, bool) { return works[enc], false },
	}
}

// After a failed detection: a busy device (its sessions all taken) is tried
// again until it works, the waits doubling; one that failed without a sign of
// its device a few times more, then never (Windows' builds list h264_nvenc on
// any PC); one that works, or none listed (no test encode at all), not again.
func TestWatchChoiceRetries(t *testing.T) {
	for _, c := range []struct {
		name       string
		encs       []string
		busy       bool
		worksAfter int // the try that works (0: never)
		want       int // tries
		hw         HWAccel
	}{
		{"busy, then free", []string{"h264_nvenc"}, true, 6, 6, HWAccelNVENC},
		{"no device", []string{"h264_nvenc"}, false, 0, 1 + maybeTries, HWAccelSoftware},
		{"none listed", []string{"libx264"}, false, 0, 0, HWAccelSoftware},
		{"works at once", []string{"h264_nvenc"}, false, 1, 1, HWAccelNVENC},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			tries := 0
			d := &Detector{Bin: DefaultBinary(), GOOS: "linux",
				Encoders: func(context.Context, string) ([]string, error) { return c.encs, nil },
				Works: func(context.Context, string, string) (bool, bool) {
					mu.Lock()
					defer mu.Unlock()
					tries++
					return c.worksAfter > 0 && tries >= c.worksAfter, c.busy
				}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			choice := WatchChoice(ctx, d, time.Second, time.Millisecond, func(HWAccel, error) {})
			count := func() int {
				mu.Lock()
				defer mu.Unlock()
				return tries
			}
			if !waitUntil(5*time.Second, func() bool { return count() >= c.want && choice.Get() == c.hw }) {
				t.Fatalf("tries %d, verdict %q; want %d, %q", count(), choice.Get(), c.want, c.hw)
			}
			time.Sleep(100 * time.Millisecond) // waits double from 1 ms: well past the next try
			if n := count(); n != c.want {
				t.Errorf("tries %d, want %d", n, c.want)
			}
		})
	}
}

func TestDetector(t *testing.T) {
	all := []string{"libx264", "h264_nvenc", "h264_qsv", "h264_videotoolbox"}
	for _, c := range []struct {
		name, goos, prefer string
		encs               []string
		works              map[string]bool
		want               HWAccel
	}{
		{"a Mac", "darwin", "", all, map[string]bool{"h264_videotoolbox": true}, HWAccelVideoToolbox},
		{"Linux with an NVIDIA GPU", "linux", "", all, map[string]bool{"h264_nvenc": true, "h264_qsv": true}, HWAccelNVENC},
		// Windows' builds list h264_nvenc on any PC: the test encode decides
		{"Windows with an NVIDIA GPU", "windows", "", all, map[string]bool{"h264_nvenc": true}, HWAccelNVENC},
		{"Windows without one", "windows", "", all, map[string]bool{}, HWAccelSoftware},
		{"Linux with an Intel GPU", "linux", "", all, map[string]bool{"h264_qsv": true}, HWAccelQSV},
		{"listed but not built in", "linux", "", []string{"libx264"}, map[string]bool{"h264_nvenc": true}, HWAccelSoftware},
		{"no GPU", "linux", "", []string{"libx264"}, nil, HWAccelSoftware},
		{"prefer software", "linux", "software", all, map[string]bool{"h264_nvenc": true}, HWAccelSoftware},
		{"prefer qsv", "linux", "qsv", all, map[string]bool{"h264_nvenc": true, "h264_qsv": true}, HWAccelQSV},
		{"prefer one that does not work", "linux", "qsv", all, map[string]bool{"h264_nvenc": true}, HWAccelSoftware},
		{"prefer something unknown", "linux", "cuda", all, map[string]bool{"h264_nvenc": true}, HWAccelSoftware},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := detector(c.goos, c.encs, c.works)
			d.Prefer = c.prefer
			got, err := d.Detect(context.Background())
			if got != c.want || (err != nil) != (c.prefer == "cuda") {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
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

// Each verdict's arguments: FFmpeg has no "nvenc" hwaccel (NVENC decodes with
// cuda), NVENC refuses libx264's "veryfast", VideoToolbox has no -preset; every
// rung is 8-bit 4:2:0 (NVENC's H.264 encodes no 10-bit source otherwise).
func TestTranscodeArgs_PerHWAccel(t *testing.T) {
	for _, c := range []struct {
		hw, decode, encoder, preset string
	}{
		{"", "", "libx264", "veryfast"},
		{"nvenc", "cuda", "h264_nvenc", "p4"},
		{"qsv", "", "h264_qsv", "veryfast"},
		{"videotoolbox", "videotoolbox", "h264_videotoolbox", ""},
	} {
		plan := PlanFor(c.hw, 3, 3, "hevc", true)
		for name, args := range map[string]Args{
			"hls":  HLSArgs("/in.mp4", "/out", "master.m3u8", DefaultLadder(0), plan),
			"dash": DASHArgs("/in.mp4", "/out", DefaultLadder(0), plan),
		} {
			joined := " " + strings.Join(args, " ") + " "
			if got := argAfter(args, "-hwaccel"); got != c.decode {
				t.Errorf("%s %q: -hwaccel %q, want %q", name, c.hw, got, c.decode)
			}
			if !strings.Contains(joined, " -c:v:0 "+c.encoder+" ") {
				t.Errorf("%s %q: no %s: %s", name, c.hw, c.encoder, joined)
			}
			if got := argAfter(args, "-preset:v:0"); got != c.preset {
				t.Errorf("%s %q: preset %q, want %q", name, c.hw, got, c.preset)
			}
			for i := 0; i < 3; i++ {
				if got := argAfter(args, "-pix_fmt:v:"+itoa(i)); got != "yuv420p" {
					t.Errorf("%s %q: rung %d pix_fmt %q", name, c.hw, i, got)
				}
			}
		}
	}
}

// The DASH ladder is one AdaptationSet (its rungs a player's Representations),
// the audio another; without audio only the video's.
func TestDASHArgs_OneAdaptationSet(t *testing.T) {
	if got := argAfter(DASHArgs("/in.mp4", "/out", DefaultLadder(0), SoftwarePlan(3, true)), "-adaptation_sets"); got != "id=0,streams=v id=1,streams=a" {
		t.Errorf("with audio: %q", got)
	}
	if got := argAfter(DASHArgs("/in.mp4", "/out", DefaultLadder(0), SoftwarePlan(3, false)), "-adaptation_sets"); got != "id=0,streams=v" {
		t.Errorf("without audio: %q", got)
	}
}

// An HDR source is tone-mapped once, its result split to the rungs: each maps
// its own [vN] instead of the source's video.
func TestTranscodeArgs_ToneMap(t *testing.T) {
	p := PlanFor("nvenc", 3, 3, "hevc", true)
	p.ToneMap = toneMapFilters
	for name, args := range map[string]Args{
		"hls":  HLSArgs("/in.mkv", "/out", "master.m3u8", DefaultLadder(0), p),
		"dash": DASHArgs("/in.mkv", "/out", DefaultLadder(0), p),
	} {
		graph := argAfter(args, "-filter_complex")
		if graph != "[0:v:0]"+toneMapFilters+",split=3[v0][v1][v2]" {
			t.Errorf("%s: graph %q", name, graph)
		}
		joined := " " + strings.Join(args, " ") + " "
		for _, v := range []string{"[v0]", "[v1]", "[v2]"} {
			if !strings.Contains(joined, " -map "+v+" ") {
				t.Errorf("%s: no -map %s", name, v)
			}
		}
		if strings.Contains(joined, " -map 0:v:0 ") {
			t.Errorf("%s: a rung maps the source's video past the tone-mapping", name)
		}
	}
	// SDR: no graph
	if g := argAfter(HLSArgs("/in.mkv", "/out", "master.m3u8", DefaultLadder(0), SoftwarePlan(3, true)), "-filter_complex"); g != "" {
		t.Errorf("SDR graph %q", g)
	}
}

// toneMapFor: an HDR source, when this ffmpeg has zscale and tonemap (asked
// once); SDR, or an ffmpeg without them, none.
func TestToneMapFor(t *testing.T) {
	var lists int
	for _, c := range []struct {
		hdr     bool
		filters map[string]bool
		want    string
	}{
		{true, map[string]bool{"zscale": true, "tonemap": true}, toneMapFilters},
		{false, map[string]bool{"zscale": true, "tonemap": true}, ""},
		{true, map[string]bool{"tonemap": true}, ""},
	} {
		e := &execRunner{
			hdr:     func(context.Context, string, string) bool { return c.hdr },
			filters: func(context.Context, string) map[string]bool { lists++; return c.filters },
		}
		for i := 0; i < 2; i++ {
			if got := e.toneMapFor(context.Background(), Job{InputPath: "/in.mkv"}); got != c.want {
				t.Errorf("hdr %v filters %v: %q", c.hdr, c.filters, got)
			}
		}
	}
	if lists != 2 {
		t.Errorf("the filters were listed %d times, want once a runner that met an HDR source", lists)
	}
}

// The GPU decodes only what every NVDEC and VideoToolbox decodes (H.264, HEVC):
// AV1 on an NVIDIA GPU without AV1 decode failed outright (FFmpeg 7's native AV1
// decoder has no software fallback). The rungs the gate gave no session for are
// libx264's.
func TestPlanFor(t *testing.T) {
	for _, c := range []struct {
		hw     string
		hwRung int
		codec  string
		decode string
		encs   string
	}{
		{"nvenc", 3, "h264", "cuda", "h264_nvenc h264_nvenc h264_nvenc"},
		{"nvenc", 3, "hevc", "cuda", "h264_nvenc h264_nvenc h264_nvenc"},
		{"nvenc", 3, "av1", "", "h264_nvenc h264_nvenc h264_nvenc"},
		{"nvenc", 3, "vp9", "", "h264_nvenc h264_nvenc h264_nvenc"},
		{"nvenc", 3, "", "", "h264_nvenc h264_nvenc h264_nvenc"},
		{"nvenc", 1, "h264", "cuda", "h264_nvenc libx264 libx264"},
		{"nvenc", 0, "h264", "", "libx264 libx264 libx264"},
		{"videotoolbox", 3, "av1", "", "h264_videotoolbox h264_videotoolbox h264_videotoolbox"},
		{"qsv", 3, "h264", "", "h264_qsv h264_qsv h264_qsv"},
		{"", 3, "h264", "", "libx264 libx264 libx264"},
	} {
		p := PlanFor(c.hw, c.hwRung, 3, c.codec, true)
		if p.Decode != c.decode || strings.Join(p.Encoders, " ") != c.encs || !p.Audio {
			t.Errorf("PlanFor(%q, %d, %q) = %+v, want decode %q, %s", c.hw, c.hwRung, c.codec, p, c.decode, c.encs)
		}
	}
}

func argAfter(args Args, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

var errReadable = readableError("ffmpeg not on PATH")

type readableError string

func (e readableError) Error() string { return string(e) }
