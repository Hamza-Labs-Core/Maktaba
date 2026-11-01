package handlers

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/mediatest"
)

const tsMap = "X-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000"

// WithTimestampMap puts the map right after the WEBVTT line, keeping the
// file's line endings (LF, CRLF or CR) and the rest of its header; a map the
// file has is rebased to the session's MPEGTS, its LOCAL kept; a file that is
// no WebVTT is left alone.
func TestWithTimestampMap(t *testing.T) {
	for in, want := range map[string]string{
		"WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\nHi\n": "WEBVTT\n" + tsMap + "\n\n1\n00:00:01.000 --> 00:00:02.000\nHi\n",
		"WEBVTT - film\nKind: captions\n\n1\n":             "WEBVTT - film\n" + tsMap + "\nKind: captions\n\n1\n",
		"WEBVTT\r\n\r\n1\r\n":                              "WEBVTT\r\n" + tsMap + "\r\n\r\n1\r\n",
		// CR-only line endings: the map in the header, not after the cues
		"WEBVTT\rKind: captions\r\r1\r00:00:01.000 --> 00:00:02.000\rHi\r": "WEBVTT\r" + tsMap + "\rKind: captions\r\r1\r00:00:01.000 --> 00:00:02.000\rHi\r",
		"\uFEFFWEBVTT\n\n": "WEBVTT\n" + tsMap + "\n\n",
		"WEBVTT":           "WEBVTT\n" + tsMap + "\n",
		// a map from another packaging: its MPEGTS rebased to the session's
		"WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n\n1\n": "WEBVTT\n" + tsMap + "\n\n1\n",
		"WEBVTT\rX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\r\r1\r": "WEBVTT\r" + tsMap + "\r\r1\r",
		// a nonzero LOCAL is kept (cue time LOCAL is the video's start), in
		// either order, written hh:mm:ss.ttt; the other header lines stay
		"WEBVTT\r\nKind: captions\r\nX-TIMESTAMP-MAP=LOCAL:01:00:00.000,MPEGTS:900000\r\n\r\n": "WEBVTT\r\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:01:00:00.000\r\nKind: captions\r\n\r\n",
		"WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:0,LOCAL:00:10.500\n\n":                                 "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:10.500\n\n",
		// a LOCAL that is no WebVTT timestamp counts as 0
		"WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:soon\n\n": "WEBVTT\n" + tsMap + "\n\n",
		"1\n00:00:01,000 --> 00:00:02,000\nnot vtt\n":          "1\n00:00:01,000 --> 00:00:02,000\nnot vtt\n",
		// a cue that says X-TIMESTAMP-MAP is no header
		"WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\nX-TIMESTAMP-MAP\n": "WEBVTT\n" + tsMap + "\n\n1\n00:00:01.000 --> 00:00:02.000\nX-TIMESTAMP-MAP\n",
	} {
		if got := string(WithTimestampMap([]byte(in))); got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
	}
}

// The sidecar route serves the file as it is (the web's <track>), and with the
// map when a rendition's playlist asks for it (?hls=1).
func TestSubtitleHandler_SidecarAsHLSSegment(t *testing.T) {
	h := &SubtitleHandler{SidecarReader: func(_ context.Context, lang string) ([]byte, error) {
		return []byte("WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\n" + lang + "\n"), nil
	}}
	r := chi.NewRouter()
	r.Get("/subs/sidecar/{lang}.vtt", h.ServeSidecar)
	for path, want := range map[string]string{
		"/subs/sidecar/en.vtt":             "WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\nen\n",
		"/subs/sidecar/en.vtt?sig=x":       "WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\nen\n",
		"/subs/sidecar/en.vtt?hls=1&sig=x": "WEBVTT\n" + tsMap + "\n\n1\n00:00:01.000 --> 00:00:02.000\nen\n",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || rec.Body.String() != want || rec.Header().Get("Content-Type") != "text/vtt; charset=utf-8" {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}

	// a sidecar with its own map, CR line endings: as it is without ?hls=1,
	// rebased with it
	own := "WEBVTT\rX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\r\r1\r00:00:01.000 --> 00:00:02.000\rHi\r"
	h.SidecarReader = func(context.Context, string) ([]byte, error) { return []byte(own), nil }
	for path, want := range map[string]string{
		"/subs/sidecar/en.vtt?sig=x":       own,
		"/subs/sidecar/en.vtt?hls=1&sig=x": "WEBVTT\r" + tsMap + "\r\r1\r00:00:01.000 --> 00:00:02.000\rHi\r",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
}

// The map's timestamp is where a session's transcode puts the video's time 0:
// the real ffmpeg, run with the session's HLS arguments from the start and
// from a position (-output_ts_offset), starts its first segment's timestamps at
// hlsTimestampBase + start×90000 (to within a frame or two of encoder delay).
func TestHLSTimestampBase_RealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	mediatest.NeedFFmpeg(t, "ffmpeg", "ffprobe")
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=25", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "8", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", src).CombinedOutput(); err != nil {
		t.Fatalf("source: %v %s", err, out)
	}
	ladder := ffmpeg.DefaultLadder(0)[2:] // one small rung
	for _, start := range []int{0, 3} {
		out := filepath.Join(dir, "s"+strconv.Itoa(start))
		_ = os.MkdirAll(filepath.Join(out, "v0"), 0o755)
		args := ffmpeg.HLSArgs(src, out, "master.m3u8", ladder, ffmpeg.EncodePlan{Audio: true, Start: start})
		if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("start %d: %v %s", start, err, b)
		}
		b, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "packet=pts", "-read_intervals", "%+#4",
			"-of", "csv=p=0", filepath.Join(out, "v0", "seg-0.ts")).Output()
		if err != nil {
			t.Fatalf("ffprobe: %v", err)
		}
		first := int64(-1)
		for _, f := range strings.Fields(string(b)) {
			pts, err := strconv.ParseInt(strings.Trim(f, ","), 10, 64)
			if err == nil && (first < 0 || pts < first) {
				first = pts
			}
		}
		want := int64(hlsTimestampBase + start*90000)
		if first < want-9000 || first > want+9000 { // 0.1 s
			t.Errorf("start %d: first timestamp %d, want %d (±0.1 s)", start, first, want)
		}
	}
}
