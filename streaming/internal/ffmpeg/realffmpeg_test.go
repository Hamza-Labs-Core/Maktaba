package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/mediatest"
)

// countGate is a SessionGate of max sessions that records what it gave.
type countGate struct {
	mu            sync.Mutex
	max, held     int
	most, refused int
}

func (g *countGate) Acquire(context.Context, uuid.UUID, time.Duration) (func(bool), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held >= g.max {
		return nil, false
	}
	g.held++
	if g.held > g.most {
		g.most = g.held
	}
	var once sync.Once
	return func(refused bool) {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.held--
			if refused {
				g.refused++
			}
		})
	}, true
}

func (g *countGate) heldNow() int {
	held, _ := g.counts()
	return held
}

func (g *countGate) counts() (held, refused int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.held, g.refused
}

// The transcodes against the real ffmpeg (the media e2e tier). Each GPU
// verdict's -hwaccel is one this ffmpeg has and its preset one its encoder
// takes, whether or not this machine has the GPU. Then every source this
// ffmpeg can make (8-bit H.264 with audio, 10-bit HEVC, 10-bit H.264, AV1, one
// without audio) goes through the orchestrator in software and with the
// detected encoder (NVENC on a PC with an NVIDIA GPU), its gate holding two
// sessions: every rung's variant playlist and segments come out (v0, v1, v2:
// the master playlist's), and the gate gets its sessions back. A forced NVENC
// run on a machine without one fails and is run again in software.
func TestTranscodeArgs_RealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	mediatest.NeedFFmpeg(t, "ffmpeg", "ffprobe")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-hwaccels").CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg -hwaccels: %v %s", err, out)
	}
	hwaccels := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) == 1 {
			hwaccels[f[0]] = true
		}
	}
	encs, err := listEncoders(ctx, "ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, e := range encs {
		listed[e] = true
	}
	for _, hw := range []HWAccel{HWAccelNVENC, HWAccelQSV, HWAccelVideoToolbox} {
		enc := encoderForHWAccel(string(hw))
		if !listed[enc] {
			t.Logf("%s: this ffmpeg has no %s", hw, enc)
			continue
		}
		if dec := decodeHWAccel(string(hw)); dec != "" && !hwaccels[dec] {
			t.Errorf("%s: -hwaccel %s, which this ffmpeg does not have (it has %v)", hw, dec, hwaccels)
		}
		if p := presetForEncoder(enc); p != "" {
			help, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-h", "encoder="+enc).CombinedOutput()
			if err != nil {
				t.Fatalf("ffmpeg -h encoder=%s: %v", enc, err)
			}
			// the preset is one of -preset's named values
			if !regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(p) + `\s`).Match(help) {
				t.Errorf("%s: preset %q is not one %s takes:\n%s", hw, p, enc, help)
			}
		}
	}

	// the sources: name, codec (the probe's), audio, and how ffmpeg makes it
	srcDir := t.TempDir()
	type source struct {
		name, codec string
		audio       bool
		path        string
	}
	var sources []source
	hdr10 := []string{"-c:v", "libx265", "-pix_fmt", "yuv420p10le",
		"-x265-params", "log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc"}
	for _, s := range []struct {
		name, codec string
		audio       bool
		args        []string
	}{
		// 13 s: its HLS segments show the rungs' key frames aligned on 6 s
		{"h264", "h264", true, []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-t", "13"}},
		{"hevc-10bit", "hevc", true, []string{"-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-x265-params", "log-level=error"}},
		{"h264-10bit", "h264", true, []string{"-c:v", "libx264", "-pix_fmt", "yuv420p10le"}},
		{"av1", "av1", true, []string{"-c:v", "libaom-av1", "-cpu-used", "8", "-row-mt", "1", "-pix_fmt", "yuv420p"}},
		{"silent", "h264", false, []string{"-c:v", "libx264", "-pix_fmt", "yuv420p"}},
		// HDR10: PQ in BT.2020, tone-mapped to SDR when this ffmpeg can; in
		// MPEG-TS too (ffprobe prints its stream values twice), and tagged PQ
		// only (zscale found no conversion without the matrix and primaries)
		{"hdr10", "hevc", true, hdr10},
		{"hdr10.ts", "hevc", true, hdr10},
		{"hdr10-pq-only", "hevc", true, []string{"-c:v", "libx265", "-pix_fmt", "yuv420p10le",
			"-x265-params", "log-level=error:transfer=smpte2084", "-color_trc", "smpte2084"}},
	} {
		p := filepath.Join(srcDir, s.name)
		if filepath.Ext(s.name) == "" {
			p += ".mkv"
		}
		args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=1280x720:r=25:d=13"}
		if s.audio {
			args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:duration=13", "-c:a", "aac", "-shortest")
		}
		if s.name != "h264" {
			args = append(args, "-t", "3")
		}
		args = append(append(args, s.args...), p)
		if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
			t.Logf("source %s: this ffmpeg cannot make it (%v: %s)", s.name, err, lastLines(string(out)))
			continue
		}
		sources = append(sources, source{s.name, s.codec, s.audio, p})
	}
	if len(sources) == 0 || sources[0].name != "h264" {
		t.Fatal("not even the 8-bit H.264 source could be made")
	}
	// the HDR sources are found HDR (by their first frame), and tone-mapped when
	// this ffmpeg has the filters
	have, err := listFilters(ctx, "ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range sources {
		isHDR := strings.HasPrefix(src.name, "hdr10")
		if c := sourceColour(ctx, "ffprobe", src.path); c.HDR() != isHDR {
			t.Errorf("%s: colour %+v", src.name, c)
		}
		if isHDR {
			got := (&execRunner{}).toneMapFor(ctx, Job{InputPath: src.path, Ladder: DefaultLadder(0)})
			if want := have["zscale"] && have["tonemap"]; (got != "") != want {
				t.Errorf("%s: tone-mapping %q, this ffmpeg has zscale %v, tonemap %v", src.name, got, have["zscale"], have["tonemap"])
			} else if !want {
				t.Logf("%s: this ffmpeg has no zscale/tonemap: cut to 8 bits untoned", src.name)
			} else if out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-i", src.path,
				"-vf", got, "-f", "null", "-").CombinedOutput(); err != nil {
				// the chain itself (the transcode would fall back to an untoned run)
				t.Errorf("%s: the tone-mapping fails: %v %s", src.name, err, lastLines(string(out)))
			}
		}
	}

	picked, err := Default(DefaultBinary()).Detect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("the detector picks %q here", picked)
	verdicts := []HWAccel{HWAccelSoftware}
	if picked != HWAccelSoftware {
		verdicts = append(verdicts, picked)
	}
	// NVENC forced where it does not work: the hardware run fails at once and
	// is run again in software
	if picked != HWAccelNVENC && listed["h264_nvenc"] {
		verdicts = append(verdicts, HWAccelNVENC)
	}
	for _, hw := range verdicts {
		for _, src := range sources {
			for _, format := range []string{"hls", "dash"} {
				gate := &countGate{max: 2}
				o := DefaultOrchestrator(DefaultBinary(), map[HWAccel]SessionGate{hw: gate})
				dir := t.TempDir()
				job := Job{SessionID: uuid.NewString(), InputPath: src.path, OutputDir: dir, Ladder: DefaultLadder(0),
					HWAccel: string(hw), Format: format, SourceCodec: src.codec, NoAudio: !src.audio}
				h, err := o.Start(ctx, job)
				if err != nil {
					t.Fatalf("%q %s %s: start: %v", hw, src.name, format, err)
				}
				deadline := time.Now().Add(2 * time.Minute)
				for h.Active() && time.Now().Before(deadline) {
					time.Sleep(100 * time.Millisecond)
				}
				if h.Active() {
					_ = h.Stop(ctx)
					t.Fatalf("%q %s %s: still running after 2 min", hw, src.name, format)
				}
				var want []string
				if format == "hls" {
					for _, v := range []string{"v0", "v1", "v2"} {
						want = append(want, filepath.Join(v, "index.m3u8"), filepath.Join(v, "seg-0.ts"))
						if src.name != "h264" {
							continue
						}
						// every rung cut on the same 6 s boundaries (its key frames)
						if pl, err := os.ReadFile(filepath.Join(dir, v, "index.m3u8")); err == nil {
							durs := regexp.MustCompile(`#EXTINF:([0-9.]+)`).FindAllStringSubmatch(string(pl), -1)
							for i, d := range durs {
								if i < len(durs)-1 && d[1] != "6.000000" {
									t.Errorf("%q h264 hls %s: segment %d is %s s, want 6", hw, v, i, d[1])
								}
							}
						}
					}
				} else {
					want = []string{"manifest.mpd"}
					// the ladder is one AdaptationSet, the audio another
					if mpd, err := os.ReadFile(filepath.Join(dir, "manifest.mpd")); err == nil {
						sets := 1
						if src.audio {
							sets = 2
						}
						if n := strings.Count(string(mpd), "<AdaptationSet"); n != sets {
							t.Errorf("%q %s dash: %d AdaptationSets, want %d", hw, src.name, n, sets)
						}
					}
				}
				for _, f := range want {
					if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
						t.Errorf("%q %s %s: no %s (ffmpeg: %s)", hw, src.name, format, f, lastLines(h.Stderr()))
					}
				}
				if gate.heldNow() != 0 {
					t.Errorf("%q %s %s: the gate still holds %d sessions", hw, src.name, format, gate.heldNow())
				}
				if hw != HWAccelSoftware && hw == picked && gate.most != 2 {
					t.Errorf("%q %s %s: the gate gave %d sessions, want 2 (its max; the third rung is libx264's)",
						hw, src.name, format, gate.most)
				}
			}
		}
	}
}

func lastLines(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if len(ls) > 4 {
		ls = ls[len(ls)-4:]
	}
	return strings.Join(ls, " | ")
}
