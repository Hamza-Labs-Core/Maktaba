package handlers

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
)

type clipProbe struct{ row *probe.Row }

func (p clipProbe) Lookup(context.Context, uuid.UUID) (*probe.Row, error) {
	if p.row == nil {
		return nil, probe.ErrNotFound
	}
	return p.row, nil
}

func clipReq(vid uuid.UUID, q string, accept string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/stream/clip/"+vid.String()+"?"+q, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	return r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{Sub: vid.String()}))
}

// The clip is the word's track and window (capped at 12 s, clamped to the file), cut once:
// a second request is served from the cache without ffmpeg.
func TestClipCutsTheTrackAndWindowOnce(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	vid := uuid.New()
	var runs [][]string
	h := &ClipHandler{
		Probe:    clipProbe{&probe.Row{VideoID: vid, Path: media, DurationSec: 100}},
		CacheDir: filepath.Join(dir, "clips"),
		Run: func(_ context.Context, args []string, out string) error {
			runs = append(runs, args)
			return os.WriteFile(out, []byte("audio"), 0o644)
		},
	}
	for range 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, clipReq(vid, "track=1&start=10.5&end=40", ""))
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "audio/mp4" || rec.Body.String() != "audio" {
			t.Fatalf("%d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
	if len(runs) != 1 {
		t.Fatalf("ffmpeg ran %d times", len(runs))
	}
	a := strings.Join(runs[0], " ")
	if !strings.Contains(a, "-map 0:a:1") || !strings.Contains(a, "-ss 10.50") || !strings.Contains(a, "-t 12.00") {
		t.Fatalf("args %s", a)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, clipReq(vid, "start=95&end=110", "audio/webm"))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "audio/webm" ||
		!strings.Contains(strings.Join(runs[1], " "), "-t 5.00 ") {
		t.Fatalf("webm: %d %v", rec.Code, runs[1])
	}
}

func TestClipOfAMissingVideoIsMediaMissing(t *testing.T) {
	vid := uuid.New()
	for _, h := range []*ClipHandler{
		{Probe: clipProbe{nil}, CacheDir: t.TempDir()},
		{Probe: clipProbe{&probe.Row{Path: "/no/such/file.mp4"}}, CacheDir: t.TempDir()},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, clipReq(vid, "start=1&end=2", ""))
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "media_missing") {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	(&ClipHandler{Probe: clipProbe{nil}}).ServeHTTP(rec, clipReq(vid, "start=x", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d", rec.Code)
	}
}

func TestTheClipCacheKeepsItsSize(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+".m4a"), make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	(&ClipHandler{CacheBytes: 250}).evict(dir)
	left, _ := os.ReadDir(dir)
	if len(left) != 2 {
		t.Fatalf("%d clips left", len(left))
	}
}

// With a real ffmpeg: a two-track file (440 Hz, then 1000 Hz), the clip of track 1 is the
// 1000 Hz tone, within 50 ms of the asked length.
func TestClipWithFFmpegTakesTheTranscriptsTrack(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("no ffprobe")
	}
	dir := t.TempDir()
	media := filepath.Join(dir, "two.mkv")
	if b, err := exec.Command(ff, "-loglevel", "error", "-f", "lavfi", "-i", "sine=f=440:d=6",
		"-f", "lavfi", "-i", "sine=f=1000:d=6", "-map", "0", "-map", "1", "-c:a", "aac", media).CombinedOutput(); err != nil {
		t.Fatalf("make: %v %s", err, b)
	}
	vid := uuid.New()
	h := &ClipHandler{Probe: clipProbe{&probe.Row{Path: media, DurationSec: 6}}, CacheDir: filepath.Join(dir, "c"), FFmpeg: ff}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, clipReq(vid, "track=1&start=1&end=3", ""))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	clip := filepath.Join(dir, "clip.m4a")
	_ = os.WriteFile(clip, rec.Body.Bytes(), 0o644)
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", clip).Output()
	if err != nil {
		t.Fatal(err)
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if math.Abs(d-2) > 0.1 {
		t.Fatalf("length %.3f", d)
	}
	// the tone: count zero crossings of the decoded samples over the clip
	pcm, err := exec.Command(ff, "-loglevel", "error", "-i", clip, "-f", "s16le", "-ac", "1", "-ar", "16000", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	cross := 0
	for i := 2; i+1 < len(pcm); i += 2 {
		a, b := int16(uint16(pcm[i-2])|uint16(pcm[i-1])<<8), int16(uint16(pcm[i])|uint16(pcm[i+1])<<8)
		if (a < 0) != (b < 0) {
			cross++
		}
	}
	hz := float64(cross) / 2 / d
	if hz < 800 {
		t.Fatalf("%.0f Hz: not track 1's 1000 Hz", hz)
	}
}
