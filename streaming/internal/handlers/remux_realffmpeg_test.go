package handlers

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/mediatest"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// run runs a tool to its end, failing the test (with its output) when it fails.
func run(t *testing.T, ctx context.Context, name string, args ...string) []byte {
	t.Helper()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// remuxSession runs a remux job through the real orchestrator into a session's
// folder of layout, to its end.
func remuxSession(t *testing.T, ctx context.Context, layout *cache.Layout, input string, plan ffmpeg.RemuxPlan) (uuid.UUID, string) {
	t.Helper()
	id := uuid.New()
	dir := layout.HLSDir(id.String())
	o := ffmpeg.DefaultOrchestrator(ffmpeg.DefaultBinary(), nil, nil)
	h, err := o.Start(ctx, ffmpeg.Job{SessionID: id.String(), InputPath: input, OutputDir: dir, Format: "hls", Remux: &plan})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for !h.Ended() && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !h.Finished() {
		t.Fatalf("the remux did not finish: %s", h.Stderr())
	}
	return id, dir
}

// hevcHDR10Clip is a 5 s HEVC Main 10 HDR10 clip with AC3 audio, in MKV (no
// B-frames: a raw stream with Dolby Vision units injected must mux with
// generated timestamps on ffmpeg 4.4 to 8).
func hevcHDR10Clip(t *testing.T, ctx context.Context, dir string) string {
	t.Helper()
	out := filepath.Join(dir, "hdr10.mkv")
	run(t, ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "5", "-c:v", "libx265", "-pix_fmt", "yuv420p10le",
		"-x265-params", "log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:keyint=48",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc",
		"-c:a", "ac3", out)
	return out
}

// T04: a 5 s HEVC Main 10 clip remuxed into HLS fMP4 (Cast's) plays: ffprobe
// reads it as HEVC, each playlist names its init section (EXT-X-MAP) and .m4s
// segments, and the session's master still lists its subtitle rendition,
// whose WebVTT is mapped on fMP4's timestamps (0, not TS's 1.4 s: measured
// here, hlsFMP4TimestampBase).
func TestRemuxFMP4_RealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	mediatest.NeedFFmpeg(t, "ffmpeg", "ffprobe")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	src := hevcHDR10Clip(t, ctx, t.TempDir())
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	id, dir := remuxSession(t, ctx, layout, src, ffmpeg.RemuxPlan{
		Segments: ffmpeg.SegmentsFMP4, VideoCodec: "hevc", Audio: true, SourceAudio: "ac3",
		Width: 640, Height: 360, BitrateKbps: 2000, TenBit: true, VideoRange: "PQ",
	})

	index, err := os.ReadFile(filepath.Join(dir, "v0", "index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(index, []byte("#EXT-X-MAP:URI=")) || !bytes.Contains(index, []byte(".m4s")) || bytes.Contains(index, []byte(".ts")) {
		t.Fatalf("variant playlist:\n%s", index)
	}
	probe := string(run(t, ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_name,codec_tag_string,start_time,color_transfer",
		"-of", "default=noprint_wrappers=1", filepath.Join(dir, "master.m3u8")))
	if !strings.Contains(probe, "codec_name=hevc") || !strings.Contains(probe, "codec_name=ac3") {
		t.Fatalf("ffprobe of the remux:\n%s", probe)
	}
	// the first video timestamp: fMP4's base is 0 (only the encoder's frame delay
	// after it), where TS's is 1.4 s
	for _, l := range strings.Split(probe, "\n") {
		if v, ok := strings.CutPrefix(l, "start_time="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0.7 {
				t.Fatalf("an fMP4 stream starts at %v s: the subtitle map's base (%d) is wrong", f, hlsFMP4TimestampBase)
			}
		}
	}

	// the session's master, served with a sidecar subtitle
	store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
		id: {ID: id, Format: session.FormatHLS, Mode: session.ModeRemux, Segments: ffmpeg.SegmentsFMP4},
	}}
	h := &ManifestHandler{Sessions: store, Layout: layout,
		Subtitles: listerOf(SubtitleSet{DurationSec: 5, Langs: []string{"ar"}}, nil)}
	subs := &SubtitleHandler{SidecarReader: func(context.Context, string) ([]byte, error) {
		return []byte("WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\nمرحبا\n"), nil
	}}
	r := chi.NewRouter()
	r.Route("/stream/{session_id}", func(sub chi.Router) {
		sub.Use(injectSubject(id.String()))
		sub.Get("/manifest.m3u8", h.ServeMaster)
		sub.Get("/subs-{lang}/index.m3u8", h.ServeSubtitlePlaylist)
		sub.Get("/subs/sidecar/{lang}.vtt", subs.ServeSidecar)
	})
	get := func(path string) string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+id.String()+path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	master := get("/manifest.m3u8?sig=tok")
	for _, want := range []string{`#EXT-X-MEDIA:TYPE=SUBTITLES`, `URI="subs-ar/index.m3u8?sig=tok"`, `SUBTITLES="`,
		`CODECS="hvc1.2.4.L153.B0,ac-3"`, "VIDEO-RANGE=PQ", "v0/index.m3u8?sig=tok"} {
		if !strings.Contains(master, want) {
			t.Errorf("master lacks %q:\n%s", want, master)
		}
	}
	playlist := get("/subs-ar/index.m3u8")
	if !strings.Contains(playlist, "../subs/sidecar/ar.vtt?hls=fmp4") {
		t.Fatalf("subtitle playlist:\n%s", playlist)
	}
	if vtt := get("/subs/sidecar/ar.vtt?hls=fmp4"); !strings.Contains(vtt, "X-TIMESTAMP-MAP=MPEGTS:0,LOCAL:00:00:00.000") {
		t.Fatalf("fMP4 rendition's map:\n%s", vtt)
	}
}

// nalTypes counts an Annex B HEVC stream's NAL units by type.
func nalTypes(data []byte) map[int]int {
	n := map[int]int{}
	for i := 0; i+3 < len(data); i++ {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			n[int(data[i+3]>>1)&0x3f]++
			i += 2
		}
	}
	return n
}

// withDolbyVisionUnits puts a Dolby Vision RPU (NAL type 62) and an
// enhancement-layer unit (63) after each picture of an Annex B HEVC stream:
// the units a profile 7 or 8 stream carries beside its base layer.
func withDolbyVisionUnits(data []byte) []byte {
	var starts []int
	for i := 0; i+3 < len(data); i++ {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			starts = append(starts, i)
			i += 2
		}
	}
	out := make([]byte, 0, len(data)+len(starts)*24)
	for k, s := range starts {
		end := len(data)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		out = append(out, data[s:end]...)
		if t := int(data[s+3]>>1) & 0x3f; t < 32 { // a picture's slice
			out = append(out, 0, 0, 1, 62<<1, 1, 0x19, 0x08, 0x09, 0x10, 0x20, 0x30, 0x80)
			out = append(out, 0, 0, 1, 63<<1, 1, 0x44, 0x55, 0x66, 0x80)
		}
	}
	return out
}

// T05: a Dolby Vision profile 8.1 stream (an HDR10 base layer with RPU and
// enhancement units) remuxed for a Samsung TV, which has no Dolby Vision: the
// output is the HDR10 base layer alone, no RPU or enhancement unit left, its
// picture copied (PQ, BT.2020).
func TestDVBaseLayerRemux_RealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	mediatest.NeedFFmpeg(t, "ffmpeg", "ffprobe")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	work := t.TempDir()
	base := filepath.Join(work, "base.hevc")
	run(t, ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-t", "3",
		"-c:v", "libx265", "-pix_fmt", "yuv420p10le",
		"-x265-params", "log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:keyint=24:bframes=0",
		"-f", "hevc", base)
	raw, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	dvRaw := filepath.Join(work, "dv.hevc")
	if err := os.WriteFile(dvRaw, withDolbyVisionUnits(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(work, "dv81.mkv")
	run(t, ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-fflags", "+genpts", "-framerate", "24",
		"-f", "hevc", "-i", dvRaw, "-c", "copy", src)
	annexB := func(in, out string) map[int]int {
		run(t, ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", in, "-map", "0:v:0", "-c", "copy",
			"-bsf:v", "hevc_mp4toannexb", "-f", "hevc", out)
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return nalTypes(b)
	}
	if in := annexB(src, filepath.Join(work, "in.hevc")); in[62] == 0 || in[63] == 0 {
		t.Fatalf("the sample carries no Dolby Vision units: %v", in)
	}

	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	_, dir := remuxSession(t, ctx, layout, src, ffmpeg.RemuxPlan{
		Segments: ffmpeg.SegmentsFMP4, VideoCodec: "hevc", DropDV: true, TenBit: true, VideoRange: "PQ",
	})
	out := annexB(filepath.Join(dir, "master.m3u8"), filepath.Join(work, "out.hevc"))
	if out[62] != 0 || out[63] != 0 {
		t.Fatalf("Dolby Vision units left after the remux: %v", out)
	}
	if out[19]+out[20]+out[1] == 0 { // IDR or trailing pictures: the base layer is there
		t.Fatalf("no picture in the remux: %v", out)
	}
	probe := string(run(t, ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,pix_fmt,color_transfer,color_primaries", "-of", "default=noprint_wrappers=1",
		filepath.Join(dir, "master.m3u8")))
	for _, want := range []string{"codec_name=hevc", "pix_fmt=yuv420p10le", "color_transfer=smpte2084", "color_primaries=bt2020"} {
		if !strings.Contains(probe, want) {
			t.Errorf("remux is not the HDR10 base layer (%s):\n%s", want, probe)
		}
	}
}

// Roku: the audio as EAC3 5.1 with a stereo AAC track beside it, each its own
// rendition playlist (v1/, v2/), as the master's audio group names them.
func TestRemuxStereoBeside_RealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	mediatest.NeedFFmpeg(t, "ffmpeg", "ffprobe")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	src := hevcHDR10Clip(t, ctx, t.TempDir())
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	_, dir := remuxSession(t, ctx, layout, src, ffmpeg.RemuxPlan{
		Segments: ffmpeg.SegmentsFMP4, VideoCodec: "hevc", Audio: true, AudioCodec: "eac3", SourceAudio: "ac3",
		StereoAAC: true, TenBit: true,
	})
	master, _ := os.ReadFile(filepath.Join(dir, "master.m3u8"))
	for _, want := range []string{`URI="v1/index.m3u8"`, `URI="v2/index.m3u8"`, `AUDIO="aud"`} {
		if !bytes.Contains(master, []byte(want)) {
			t.Fatalf("master lacks %q:\n%s", want, master)
		}
	}
	for v, codec := range map[string]string{"v0": "hevc", "v1": "eac3", "v2": "aac"} {
		probe := string(run(t, ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_name,channels",
			"-of", "default=noprint_wrappers=1", filepath.Join(dir, v, "index.m3u8")))
		if !strings.Contains(probe, "codec_name="+codec) {
			t.Errorf("%s is not %s:\n%s", v, codec, probe)
		}
		if codec == "aac" && !strings.Contains(probe, "channels=2") {
			t.Errorf("the stereo track is not stereo:\n%s", probe)
		}
	}
}

// A transcode for a device that plays fMP4 alone (a Cast receiver listing only
// fmp4): every rung's playlist names an init section that is there, beside its
// .m4s segments.
func TestTranscodeFMP4_RealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	mediatest.NeedFFmpeg(t, "ffmpeg", "ffprobe")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	src := hevcHDR10Clip(t, ctx, t.TempDir())
	dir := t.TempDir()
	ladder := ffmpeg.FitLadder(ffmpeg.DefaultLadder(0)[1:], 640, 360) // two rungs
	o := ffmpeg.DefaultOrchestrator(ffmpeg.DefaultBinary(), nil, nil)
	h, err := o.Start(ctx, ffmpeg.Job{SessionID: uuid.NewString(), InputPath: src, OutputDir: dir, Format: "hls",
		Ladder: ladder, SourceCodec: "hevc", Segments: ffmpeg.SegmentsFMP4})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for !h.Ended() && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !h.Finished() {
		t.Fatalf("the transcode did not finish: %s", h.Stderr())
	}
	for i := range ladder {
		v := filepath.Join(dir, "v"+strconv.Itoa(i))
		index, err := os.ReadFile(filepath.Join(v, "index.m3u8"))
		if err != nil {
			t.Fatal(err)
		}
		m := hlsURIAttr.FindSubmatch(index)
		if m == nil || !bytes.Contains(index, []byte(".m4s")) {
			t.Fatalf("v%d playlist:\n%s", i, index)
		}
		if _, err := os.Stat(filepath.Join(v, string(m[1]))); err != nil {
			t.Fatalf("v%d's init section %s is not beside its playlist: %v", i, m[1], err)
		}
	}
	probe := string(run(t, ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_name", "-of", "csv=p=0",
		filepath.Join(dir, "v0", "index.m3u8")))
	if !strings.Contains(probe, "h264") || !strings.Contains(probe, "aac") {
		t.Fatalf("ffprobe:\n%s", probe)
	}
}
