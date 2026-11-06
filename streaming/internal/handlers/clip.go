package handlers

// Epic 52 (story 52.8): the audio around a suspect word, for a home admin reviewing it.
//
//	GET /stream/clip/{video_id}?track=N&start=S&end=E&sig=...
//
// The API signs the URL for the video (aud streaming-direct, 5 minutes) after checking the
// caller is a home admin; this handler cuts start..end (at most MaxClipSec) of the audio
// stream N with ffmpeg: AAC in fragmented MP4, or Opus in WebM when the client sends
// Accept: audio/webm, mono, 64 kb/s. Clips are kept on disk by (video, track, window,
// format) and the oldest go past CacheBytes, so a word opened again costs no ffmpeg.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
)

// MaxClipSec is the longest clip a review asks for.
const MaxClipSec = 12.0

// DefaultClipCacheBytes is the clip cache's size (500 MB).
const DefaultClipCacheBytes = 500 << 20

// ClipRunner cuts a clip: the ffmpeg arguments, the output written to out.
type ClipRunner func(ctx context.Context, args []string, out string) error

// ClipLookup is the probe cache's lookup (a video's path and length).
type ClipLookup interface {
	Lookup(ctx context.Context, id uuid.UUID) (*probe.Row, error)
}

// ClipHandler serves review clips.
type ClipHandler struct {
	Probe      ClipLookup
	CacheDir   string
	CacheBytes int64
	FFmpeg     string
	Run        ClipRunner
	Logger     *slog.Logger

	mu sync.Mutex
}

func (h *ClipHandler) run(ctx context.Context, args []string, out string) error {
	if h.Run != nil {
		return h.Run(ctx, args, out)
	}
	bin := h.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append(args, out)...) // #nosec G204 -- fixed binary, numeric args
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(b[max(0, len(b)-300):])))
	}
	return nil
}

func number(r *http.Request, name string, def float64) (float64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// ServeHTTP handles GET /stream/clip/{video_id}.
func (h *ClipHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	videoID, err := uuid.Parse(auth.SubjectFromContext(r.Context()))
	if err != nil {
		httpx.Write(w, http.StatusBadRequest, "bad-video-id", "video id not a UUID", "")
		return
	}
	track, okT := number(r, "track", 0)
	start, okS := number(r, "start", 0)
	end, okE := number(r, "end", 0)
	if !okT || !okS || !okE || track < 0 || track != math.Trunc(track) || track > 64 {
		httpx.Write(w, http.StatusBadRequest, "bad-window", "track, start and end must be numbers", "")
		return
	}
	row, err := h.Probe.Lookup(r.Context(), videoID)
	if err != nil {
		if errors.Is(err, probe.ErrNotFound) || errors.Is(err, probe.ErrNotProbed) {
			httpx.Write(w, http.StatusNotFound, "media_missing", "the video is not there", "")
			return
		}
		httpx.Write(w, http.StatusServiceUnavailable, "lookup-failed", "the video could not be looked up", "")
		return
	}
	start = math.Max(0, start)
	if row.DurationSec > 0 {
		end = math.Min(end, row.DurationSec)
	}
	if end <= start {
		end = start + 1
	}
	if end-start > MaxClipSec {
		end = start + MaxClipSec
	}
	webm := strings.Contains(r.Header.Get("Accept"), "audio/webm")
	ext, ctype := "m4a", "audio/mp4"
	if webm {
		ext, ctype = "webm", "audio/webm"
	}
	local := filepath.Clean(pathmap.Resolve(row.Path))
	if !filepath.IsAbs(local) || pathHasTraversal(local) {
		httpx.Write(w, http.StatusNotFound, "media_missing", "the video file is not there", "")
		return
	}
	if _, err := os.Stat(local); err != nil {
		httpx.Write(w, http.StatusNotFound, "media_missing", "the video file is not there", "")
		return
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%.2f|%.2f|%s", videoID, int(track), start, end, ext))
	name := hex.EncodeToString(sum[:12]) + "." + ext
	dir := filepath.Clean(h.CacheDir)
	out := filepath.Join(dir, name)
	if _, err := os.Stat(out); err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			httpx.Write(w, http.StatusInternalServerError, "cache", "the clip cache is not writable", "")
			return
		}
		tmp := out + ".part." + ext
		args := []string{"-hide_banner", "-loglevel", "error", "-y",
			"-ss", strconv.FormatFloat(start, 'f', 2, 64), "-t", strconv.FormatFloat(end-start, 'f', 2, 64),
			"-i", local, "-map", fmt.Sprintf("0:a:%d", int(track)), "-vn", "-ac", "1", "-b:a", "64k"}
		if webm {
			args = append(args, "-c:a", "libopus", "-f", "webm")
		} else {
			args = append(args, "-c:a", "aac", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4")
		}
		if err := h.run(r.Context(), args, tmp); err != nil {
			_ = os.Remove(tmp)
			h.log().Warn("review clip: ffmpeg failed", "video_id", videoID.String(), "err", err.Error(),
				"event", "review_clip_failed")
			httpx.Write(w, http.StatusNotFound, "media_missing", "the audio could not be cut", "")
			return
		}
		if err := os.Rename(tmp, out); err != nil {
			httpx.Write(w, http.StatusInternalServerError, "cache", "the clip could not be kept", "")
			return
		}
		h.evict(dir)
	} else {
		now := time.Now()
		_ = os.Chtimes(out, now, now) // most recently used
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, out)
}

func (h *ClipHandler) log() *slog.Logger {
	if h.Logger == nil {
		return slog.Default()
	}
	return h.Logger
}

// evict removes the least recently used clips past the cache's size.
func (h *ClipHandler) evict(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	limit := h.CacheBytes
	if limit <= 0 {
		limit = DefaultClipCacheBytes
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type f struct {
		path string
		size int64
		at   time.Time
	}
	var files []f
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() || strings.Contains(e.Name(), ".part.") {
			continue
		}
		files = append(files, f{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, x := range files {
		if total <= limit {
			break
		}
		if os.Remove(x.path) == nil {
			total -= x.size
		}
	}
}
