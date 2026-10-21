package channel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func joined(a []string) string { return strings.Join(a, " ") }

func argAfter(a []string, flag string) string {
	for i := 0; i+1 < len(a); i++ {
		if a[i] == flag {
			return a[i+1]
		}
	}
	return ""
}

func TestBlockArgs_EncoderSelection(t *testing.T) {
	in := BlockInput{Kind: "file", Source: "/m/a.mp4", DurationSec: 10, HasVideo: true, HasAudio: true, HWDecode: true}
	nv := BlockArgs(OutputProfile{Encoder: EncoderNVENC}, in)
	if argAfter(nv, "-c:v") != "h264_nvenc" {
		t.Errorf("nvenc profile should use h264_nvenc: %s", joined(nv))
	}
	if argAfter(nv, "-hwaccel") != "cuda" {
		t.Errorf("NVENC host should decode with -hwaccel cuda (never 'nvenc'): %s", joined(nv))
	}
	sw := BlockArgs(OutputProfile{Encoder: EncoderSoftware}, BlockInput{Kind: "file", Source: "/m/a.mp4",
		DurationSec: 10, HasVideo: true, HasAudio: true})
	if argAfter(sw, "-c:v") != "libx264" || strings.Contains(joined(sw), "-hwaccel") {
		t.Errorf("software profile wrong: %s", joined(sw))
	}
	// Fixed output: default 1080p30, 48 kHz stereo AAC, MPEG-TS on stdout.
	j := joined(sw)
	for _, want := range []string{"scale=1920:1080", "fps=30", "-ar 48000", "-ac 2", "-c:a aac", "-f mpegts pipe:1", "-g 60"} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in %s", want, j)
		}
	}
	ac3 := BlockArgs(OutputProfile{AudioCodec: "ac3"}, in)
	if argAfter(ac3, "-c:a") != "ac3" {
		t.Errorf("ac3 profile: %s", joined(ac3))
	}
}

func TestBlockArgs_RealTimeSeekAndTimeline(t *testing.T) {
	a := BlockArgs(DefaultProfile(), BlockInput{Kind: "file", Source: "/m/a.mp4", SeekSec: 754.25,
		DurationSec: 600, HasVideo: true, HasAudio: true, TSOffsetSec: 3600.5})
	j := joined(a)
	if !strings.Contains(j, "-ss 754.25 -t 600 -re -i /m/a.mp4") {
		t.Errorf("want input seek + bound + -re before -i: %s", j)
	}
	if argAfter(a, "-output_ts_offset") != "3600.5" {
		t.Errorf("timeline offset missing: %s", j)
	}
	if !strings.Contains(j, "+initial_discontinuity") {
		t.Error("blocks must flag their first packets as a discontinuity")
	}
}

func TestBlockArgs_SilentInputGetsAnullsrc(t *testing.T) {
	a := BlockArgs(DefaultProfile(), BlockInput{Kind: "file", Source: "/m/silent.mp4", DurationSec: 5, HasVideo: true})
	j := joined(a)
	if strings.Contains(j, "0:a:0") {
		t.Errorf("silent file must not map 0:a:0: %s", j)
	}
	if !strings.Contains(j, "anullsrc=channel_layout=stereo:sample_rate=48000") || argAfter(a, "-map") == "" {
		t.Errorf("silent file needs generated silence: %s", j)
	}
	if !strings.Contains(j, "-map 1:a") {
		t.Errorf("silence input should be mapped: %s", j)
	}
}

func TestBlockArgs_AudioOnlyGetsBlackVideo(t *testing.T) {
	j := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "file", Source: "/m/a.mp3", DurationSec: 5, HasAudio: true}))
	if !strings.Contains(j, "color=c=black") || strings.Contains(j, "0:v:0") {
		t.Errorf("audio-only source needs a black video source: %s", j)
	}
}

func TestBlockArgs_WatermarkAndLoudnorm(t *testing.T) {
	ln := Loudnorm{Enabled: true, TargetLUFS: -18, Filter: "loudnorm"}
	wm := &Watermark{Path: "/look/bug.gif", Position: "bottom-left", WidthPct: 12, MarginPct: 3}
	a := BlockArgs(DefaultProfile(), BlockInput{Kind: "file", Source: "/m/a.mp4", DurationSec: 5,
		HasVideo: true, HasAudio: true, Watermark: wm, Loudnorm: &ln})
	j := joined(a)
	if !strings.Contains(j, "-ignore_loop 0 -i /look/bug.gif") {
		t.Errorf("animated GIF watermark must loop: %s", j)
	}
	if !strings.Contains(j, "scale=230:-2") { // 12% of 1920 = 230.4 → even 230
		t.Errorf("watermark width wrong: %s", j)
	}
	if !strings.Contains(j, "overlay=x=32:y=H-h-32") { // 3% of 1080
		t.Errorf("bottom-left placement wrong: %s", j)
	}
	if !strings.Contains(j, "loudnorm=I=-18:TP=-1.5:LRA=11") {
		t.Errorf("loudnorm missing: %s", j)
	}
	png := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "file", Source: "/m/a.mp4", DurationSec: 5,
		HasVideo: true, HasAudio: true, Watermark: &Watermark{Path: "/look/bug.png", Position: "top-right", WidthPct: 10}}))
	if !strings.Contains(png, "-loop 1 -i /look/bug.png") || !strings.Contains(png, "overlay=x=W-w-0:y=0") {
		t.Errorf("png watermark: %s", png)
	}
	off := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "file", Source: "/m/a.mp4", DurationSec: 5,
		HasVideo: true, HasAudio: true, Loudnorm: &Loudnorm{Enabled: false}}))
	if strings.Contains(off, "loudnorm") {
		t.Errorf("disabled loudnorm applied: %s", off)
	}
	dyn := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "file", Source: "/m/a.mp4", DurationSec: 5,
		HasVideo: true, HasAudio: true, Loudnorm: &Loudnorm{Enabled: true, Filter: "dynaudnorm"}}))
	if !strings.Contains(dyn, "dynaudnorm") {
		t.Errorf("dynaudnorm: %s", dyn)
	}
}

// speechnorm (dizqueTV's default) levels programme audio only: generated
// silence (a silent source, a slate) is never filtered.
func TestBlockArgs_Speechnorm(t *testing.T) {
	ln := DefaultLook().Loudnorm
	j := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "file", Source: "/m/a.mp4", DurationSec: 5,
		HasVideo: true, HasAudio: true, Loudnorm: &ln}))
	if !strings.Contains(j, "aresample=async=1000,speechnorm=e=6.25:r=0.00001:l=1,") {
		t.Errorf("speechnorm missing: %s", j)
	}
	ln.Expansion = 3
	if j := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "live", Source: "/m/a.mp4", DurationSec: 5,
		HasVideo: true, HasAudio: true, Loudnorm: &ln})); !strings.Contains(j, "speechnorm=e=3:") {
		t.Errorf("expansion: %s", j)
	}
	for name, in := range map[string]BlockInput{
		"silent file": {Kind: "file", Source: "/m/a.mp4", DurationSec: 5, HasVideo: true, Loudnorm: &ln},
		"slate":       {Kind: "slate", DurationSec: 5, Loudnorm: &ln},
	} {
		if j := joined(BlockArgs(DefaultProfile(), in)); strings.Contains(j, "speechnorm") || !strings.Contains(j, "anullsrc") {
			t.Errorf("%s: silence must not be levelled: %s", name, j)
		}
	}
}

func TestLookaheadAllowance_OnlyLookaheadFilters(t *testing.T) {
	base := earlyEnd.Seconds()
	for f, extra := range map[string]bool{"speechnorm": false, "loudnorm": true, "dynaudnorm": true} {
		got := lookaheadAllowance(BlockInput{HasAudio: true, Loudnorm: &Loudnorm{Enabled: true, Filter: f}})
		if (got > base) != extra {
			t.Errorf("%s: allowance %v (base %v)", f, got, base)
		}
	}
}

func TestBlockArgs_Slates(t *testing.T) {
	black := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "slate", DurationSec: 7}))
	if !strings.Contains(black, "-re -f lavfi -i color=c=black:s=1920x1080:r=30") || !strings.Contains(black, "anullsrc") {
		t.Errorf("black slate: %s", black)
	}
	if !strings.Contains(black, "-t 7") {
		t.Errorf("slate needs its duration: %s", black)
	}
	logo := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "slate", DurationSec: 7, SlateLogo: "/l/logo.png",
		SlateText: "Live stream unavailable", FontFile: "C:/Windows/Fonts/arial.ttf"}))
	if !strings.Contains(logo, "overlay=x=(W-w)/2:y=(H-h)/2") || !strings.Contains(logo, "-loop 1 -i /l/logo.png") {
		t.Errorf("logo slate should centre the logo: %s", logo)
	}
	if !strings.Contains(logo, `drawtext=fontfile='C\:/Windows/Fonts/arial.ttf':text='Live stream unavailable'`) {
		t.Errorf("caption: %s", logo)
	}
	load := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "slate", DurationSec: 7, SlateImage: "/l/loading.jpg", SlateLogo: "/l/logo.png"}))
	if !strings.Contains(load, "-re -loop 1 -i /l/loading.jpg") || strings.Contains(load, "logo.png") {
		t.Errorf("loading picture should win over logo: %s", load)
	}
}

func TestBlockArgs_LiveNoSeek(t *testing.T) {
	a := BlockArgs(DefaultProfile(), BlockInput{Kind: "live", Source: "https://x/live.m3u8", SeekSec: 99,
		DurationSec: 3600, HasVideo: true, HasAudio: true})
	j := joined(a)
	if strings.Contains(j, "-ss") {
		t.Errorf("live must not seek: %s", j)
	}
	if !strings.Contains(j, "-reconnect 1") || !strings.Contains(j, "-re -i https://x/live.m3u8") {
		t.Errorf("live input: %s", j)
	}
}

func TestMuxerArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "channel-x")
	slash := filepath.ToSlash(dir)
	a := MuxerArgs(DefaultProfile(), dir, false)
	j := joined(a)
	for _, want := range []string{"-f mpegts -i pipe:0", "-c copy", "-copyts", "-f hls", slash + "/index.m3u8",
		slash + "/seg-%d.ts", "omit_endlist", "pipe:1"} {
		if !strings.Contains(j, want) {
			t.Errorf("muxer missing %q: %s", want, j)
		}
	}
	if strings.Contains(j, "discont_start") {
		t.Error("first muxer must not mark a discontinuity")
	}
	if !strings.Contains(joined(MuxerArgs(DefaultProfile(), dir, true)), "discont_start") {
		t.Error("restarted muxer should mark a discontinuity")
	}
	if !strings.Contains(MasterPlaylist(DefaultProfile()), "RESOLUTION=1920x1080") {
		t.Error("master playlist resolution")
	}
}

// TestBlockArgs_RealFFmpegVariants runs each arg shape through real
// ffmpeg for a second to prove the filtergraphs are valid.
func TestBlockArgs_RealFFmpegVariants(t *testing.T) {
	needFFmpeg(t)
	tmp := t.TempDir()
	withAudio := filepath.Join(tmp, "a.mp4")
	silent := filepath.Join(tmp, "s.mp4")
	makeClip(t, withAudio, true, 3)
	makeClip(t, silent, false, 3)
	png := filepath.Join(tmp, "logo.png")
	gif := filepath.Join(tmp, "bug.gif")
	for _, c := range [][]string{
		{"-f", "lavfi", "-i", "testsrc2=s=200x100:d=1", "-frames:v", "1", png},
		{"-f", "lavfi", "-i", "testsrc2=s=100x100:r=5:d=1", gif},
	} {
		if out, err := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, c...)...).CombinedOutput(); err != nil {
			t.Fatalf("asset: %v %s", err, out)
		}
	}
	prof := OutputProfile{Width: 320, Height: 180, FPS: 30, VideoKbps: 500, Encoder: EncoderSoftware}
	ln := &Loudnorm{Enabled: true, TargetLUFS: -16, Filter: "loudnorm"}
	sn := &Loudnorm{Enabled: true, Filter: "speechnorm", Expansion: DefaultExpansion}
	cases := map[string]BlockInput{
		"audio+speechnorm": {Kind: "file", Source: withAudio, DurationSec: 1, HasVideo: true, HasAudio: true, Loudnorm: sn},
		"audio+gif+loudnorm": {Kind: "file", Source: withAudio, SeekSec: 1, DurationSec: 1, HasVideo: true, HasAudio: true,
			Loudnorm: ln, Watermark: &Watermark{Path: gif, Position: "top-right", WidthPct: 10, MarginPct: 3}},
		"silent+png": {Kind: "file", Source: silent, DurationSec: 1, HasVideo: true, Loudnorm: ln,
			Watermark: &Watermark{Path: png, Position: "bottom-right", WidthPct: 20, MarginPct: 2}},
		"live-as-file":    {Kind: "live", Source: withAudio, DurationSec: 1, HasVideo: true, HasAudio: true, Loudnorm: &Loudnorm{Enabled: true, Filter: "dynaudnorm"}},
		"slate-black":     {Kind: "slate", DurationSec: 1},
		"slate-logo":      {Kind: "slate", DurationSec: 1, SlateLogo: png, SlateText: "Live stream unavailable", FontFile: FindFont()},
		"slate-loading":   {Kind: "slate", DurationSec: 1, SlateImage: png},
		"slate-text-only": {Kind: "slate", DurationSec: 1, SlateText: "It's 10:30 — 100%", FontFile: FindFont()},
	}
	nvenc := DetectEncoder(context.Background(), "ffmpeg", "auto") == EncoderNVENC
	if nvenc {
		cases["nvenc+cuda-decode"] = BlockInput{Kind: "file", Source: withAudio, SeekSec: 1, DurationSec: 1,
			HasVideo: true, HasAudio: true, HWDecode: true, Loudnorm: ln}
	} else {
		t.Log("NVENC not available here; skipping the h264_nvenc variant")
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(tmp, strings.ReplaceAll(name, "+", "_")+".ts")
			p := prof
			if strings.HasPrefix(name, "nvenc") {
				p.Encoder = EncoderNVENC
			}
			args := BlockArgs(p, in)
			args[len(args)-1] = out
			if b, err := exec.Command("ffmpeg", append([]string{"-y"}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("ffmpeg failed: %v\n%s\nargs: %s", err, b, joined(args))
			}
			if _, n := probeStartTime(t, out); n != 2 {
				t.Errorf("%s: %d streams, want video+audio", name, n)
			}
			if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
				t.Errorf("no output")
			}
		})
	}
}
