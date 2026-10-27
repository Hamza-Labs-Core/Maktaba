package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

func chiURLParam(r *http.Request, name string) string { return chi.URLParam(r, name) }

// SessionStreamReader exposes the subset of the session store the
// HTTP-side handlers need. Test fakes implement it directly.
type SessionStreamReader interface {
	Get(ctx context.Context, id uuid.UUID) (*session.Row, bool, error)
	Touch(ctx context.Context, id uuid.UUID, at time.Time) error
}

// HLSDirResolver maps a session id to its on-disk HLS folder. Backed
// by the cache layout in production.
type HLSDirResolver interface {
	HLSDir(sessionID string) string
}

// ManifestHandler serves Story 8.5/8.6 — master.m3u8, the per-rendition
// index.m3u8, manifest.mpd, and the segments under each.
type ManifestHandler struct {
	Sessions SessionStreamReader
	Layout   HLSDirResolver
	Now      func() time.Time
	// Logger receives the session's files that are there and cannot be read
	// (nil: slog's default).
	Logger *slog.Logger
	// FirstWrite is how long a playlist not yet on disk is waited for while its
	// transcode runs (0: answered at once). A session is handed out the moment its
	// transcode starts, and a player asks for the playlist within milliseconds: a
	// 404 then ends playback in a player that does not retry it (Safari's own HLS,
	// AVPlayer), though the playlist is a second away.
	FirstWrite time.Duration
}

// writtenSoon waits up to FirstWrite for path (under base) while sub's transcode
// runs: the path, read-safe, once it is on disk ("" when it is not).
func (h *ManifestHandler) writtenSoon(r *http.Request, base, path, sub string) string {
	if h.FirstWrite <= 0 {
		return ""
	}
	// the path is checked again here, right before the filesystem is asked
	path, err := httpx.EnsureUnder(base, path)
	if err != nil {
		return ""
	}
	id, err := uuid.Parse(sub)
	if err != nil {
		return ""
	}
	deadline := time.Now().Add(h.FirstWrite)
	for tick := 0; time.Now().Before(deadline); tick++ {
		if tick%10 == 0 { // its transcode still running (every second)
			row, ok, err := h.Sessions.Get(r.Context(), id)
			if err != nil || !ok || row.Transcoder == nil || !row.Transcoder.Active() ||
				row.TranscodeCut() || row.TranscodeFinished() {
				return ""
			}
		}
		select {
		case <-r.Context().Done():
			return ""
		case <-time.After(100 * time.Millisecond):
		}
		if clean := filepath.Clean(path); strings.HasPrefix(clean, filepath.Clean(base)+string(filepath.Separator)) {
			if _, err := os.Stat(clean); err == nil {
				return clean
			}
		}
	}
	return ""
}

// readFailed logs a failed read of a session's playlist or segment that is
// not "not written yet" (permissions, a full or failing disk): the player
// sees a 404 like a segment still to come, and waits.
func (h *ManifestHandler) readFailed(r *http.Request, path string, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	l := h.Logger
	if l == nil {
		l = slog.Default()
	}
	l.ErrorContext(r.Context(), "a stream file could not be read", "session_id", auth.SubjectFromContext(r.Context()),
		"path", path, "err", err.Error(), "event", "vod_file_read_failed")
}

// ServeMaster handles GET /stream/{session_id}/manifest.{m3u8,mpd}.
func (h *ManifestHandler) ServeMaster(w http.ResponseWriter, r *http.Request) {
	sub := auth.SubjectFromContext(r.Context())
	id, err := uuid.Parse(sub)
	if err != nil {
		httpx.Write(w, http.StatusBadRequest, "bad-session-id", "session id not a UUID", err.Error())
		return
	}
	row, ok, err := h.Sessions.Get(r.Context(), id)
	if err != nil || !ok {
		httpx.Write(w, http.StatusNotFound, "session-not-found", "session not found", "")
		return
	}

	// Path determines which manifest to look at.
	wantDASH := strings.HasSuffix(r.URL.Path, ".mpd")
	wantHLS := strings.HasSuffix(r.URL.Path, ".m3u8")
	if (wantDASH && row.Format != session.FormatDASH) || (wantHLS && row.Format != session.FormatHLS) {
		httpx.Write(w, http.StatusConflict, "format-mismatch",
			"session format mismatch",
			fmt.Sprintf("session.format=%s url=%s", row.Format, r.URL.Path))
		return
	}

	dir := h.Layout.HLSDir(sub)
	var path string
	switch {
	case wantDASH:
		path = filepath.Join(dir, "manifest.mpd")
	default:
		path = filepath.Join(dir, "master.m3u8")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if p := filepath.Clean(h.writtenSoon(r, dir, path, sub)); strings.HasPrefix(p, filepath.Clean(dir)+string(filepath.Separator)) {
			data, err = os.ReadFile(p)
		}
	}
	if err != nil {
		h.readFailed(r, path, err)
		if errors.Is(err, os.ErrNotExist) {
			if !h.reloaded(w, r, row) {
				return // over before it wrote it: never coming
			}
			httpx.Write(w, http.StatusNotFound, "manifest-not-found",
				"manifest not yet on disk",
				"the FFmpeg subprocess hasn't written the master playlist; client should retry")
			return
		}
		httpx.Write(w, http.StatusInternalServerError, "read-failed", "manifest read failed", "")
		return
	}
	if !h.reloaded(w, r, row) {
		return
	}
	if wantDASH {
		written := h.now()
		if info, err := os.Stat(path); err == nil {
			written = info.ModTime()
		}
		data = transcodeMPD(data, dir, written, h.now())
	}
	if sig := r.URL.Query().Get("sig"); sig != "" {
		if wantDASH {
			data = SignMPD(data, sig)
		} else {
			data = SignPlaylist(data, sig)
		}
	}
	w.Header().Set("Content-Type", manifestContentType(wantDASH))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// ServeRenditionIndex handles /stream/{session_id}/{rendition}/index.m3u8.
func (h *ManifestHandler) ServeRenditionIndex(w http.ResponseWriter, r *http.Request) {
	sub := auth.SubjectFromContext(r.Context())
	rendition := chiURLParam(r, "rendition")
	if rendition == "" {
		httpx.Write(w, http.StatusBadRequest, "missing-rendition", "rendition path missing", "")
		return
	}
	// Story 23.5 AC-2: rendition is an untrusted URL segment — route
	// it through the single canonicalizer so `..`/symlink-escape
	// cannot read outside this session's HLS directory.
	base := h.Layout.HLSDir(sub)
	path, err := httpx.CanonicalUnder(base, rendition, "index.m3u8")
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "rendition-not-found", "rendition index not on disk", "")
		return
	}
	// Defence-in-depth, statically-visible containment recheck before
	// the filesystem sink (Story 23.5 AC-2). CanonicalUnder already
	// guarantees this; the explicit Clean+prefix gate clears CodeQL.
	path, err = httpx.EnsureUnder(base, path)
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "rendition-not-found", "rendition index not on disk", "")
		return
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if p := filepath.Clean(h.writtenSoon(r, base, path, sub)); strings.HasPrefix(p, filepath.Clean(base)+string(filepath.Separator)) {
			data, err = os.ReadFile(p)
		}
	}
	if err != nil {
		h.readFailed(r, path, err)
		if h.overBeforeWritten(w, r, sub) {
			return
		}
		httpx.Write(w, http.StatusNotFound, "rendition-not-found", "rendition index not on disk", "")
		return
	}
	id, err := uuid.Parse(sub)
	if err != nil {
		httpx.Write(w, http.StatusBadRequest, "bad-session-id", "session id not a UUID", err.Error())
		return
	}
	row, ok, err := h.Sessions.Get(r.Context(), id)
	if err != nil || !ok {
		httpx.Write(w, http.StatusNotFound, "session-not-found", "session not found", "")
		return
	}
	if !h.reloaded(w, r, row) {
		return
	}
	if !row.TranscodeFinished() {
		// ffmpeg writes it on SIGTERM too: until its end is recorded (then a
		// 410), a reload must not end the video at the cut
		data = withoutEndlist(data)
	}
	data = startAt(gapsBefore(data, row.StartSec), row.StartSec)
	if sig := r.URL.Query().Get("sig"); sig != "" {
		data = SignPlaylist(data, sig)
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// ServeSegment handles /stream/{session_id}/{rendition}/seg-{n}.ts, and a
// DASH session's /stream/{session_id}/{segment}.m4s (its init and chunks sit in
// the session's folder itself), and updates the session heartbeat (Story 8.9
// AC-2).
func (h *ManifestHandler) ServeSegment(w http.ResponseWriter, r *http.Request) {
	sub := auth.SubjectFromContext(r.Context())
	id, err := uuid.Parse(sub)
	if err != nil {
		httpx.Write(w, http.StatusBadRequest, "bad-session-id", "session id not a UUID", err.Error())
		return
	}
	rendition := chiURLParam(r, "rendition")
	segment := chiURLParam(r, "segment")
	if segment == "" {
		httpx.Write(w, http.StatusBadRequest, "missing-segment", "segment path incomplete", "")
		return
	}
	parts := []string{rendition, segment}
	if rendition == "" {
		// the session's folder itself: a DASH segment only
		if !strings.HasSuffix(segment, ".m4s") {
			httpx.Write(w, http.StatusNotFound, "segment-not-found", "not a segment", "")
			return
		}
		parts = []string{segment}
	}

	// Story 23.5 AC-2: rendition + segment are untrusted URL segments
	// — canonicalize before touching disk.
	base := h.Layout.HLSDir(sub)
	path, err := httpx.CanonicalUnder(base, parts...)
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "segment-not-found",
			"segment not yet written by FFmpeg", "")
		return
	}
	// Defence-in-depth, statically-visible containment recheck before
	// the filesystem sink (Story 23.5 AC-2).
	path, err = httpx.EnsureUnder(base, path)
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "segment-not-found",
			"segment not yet written by FFmpeg", "")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		h.readFailed(r, path, err)
		// AC-3: 404 if segment not yet on disk. Players will refetch
		// the variant playlist and try again.
		httpx.Write(w, http.StatusNotFound, "segment-not-found",
			"segment not yet written by FFmpeg", err.Error())
		return
	}

	_ = h.Sessions.Touch(r.Context(), id, h.now())

	w.Header().Set("Content-Type", segmentContentType(segment))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// startAt has an EVENT playlist (a transcode's, every segment listed) start at
// its stream's first segment, offset seconds in (after the gaps before it): a
// player starts a playlist without #EXT-X-ENDLIST near its end (hls.js three
// target durations before it), which skips the stream's start when the
// transcode runs ahead of the player. A position the player is asked to start
// at (a resume) still wins.
func startAt(data []byte, offset int) []byte {
	if !bytes.HasPrefix(data, []byte("#EXTM3U")) || !bytes.Contains(data, []byte("#EXT-X-PLAYLIST-TYPE:EVENT")) ||
		bytes.Contains(data, []byte("#EXT-X-START:")) {
		return data
	}
	i := bytes.IndexByte(data, '\n')
	if i < 0 {
		return data
	}
	start := "#EXT-X-START:TIME-OFFSET=" + strconv.Itoa(offset) + lineEnd(data, i)
	out := make([]byte, 0, len(data)+len(start))
	out = append(out, data[:i+1]...)
	out = append(out, start...)
	return append(out, data[i+1:]...)
}

// lineEnd is the playlist's line ending, as its line ending at i has it.
func lineEnd(data []byte, i int) string {
	if i > 0 && data[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// gapsBefore lists the part of the video before a stream that starts start
// seconds in as gaps (#EXT-X-GAP: segments no player loads, gap-<n>.ts, none
// longer than the target duration): the playlist's times are then the
// video's, and its segments' timestamps (-output_ts_offset), so the seek bar,
// the position (progress, the resume point), chapters and subtitles keep to
// the video. A seek into the gaps is the player's to answer (the web opens the
// video again there).
func gapsBefore(data []byte, start int) []byte {
	if start <= 0 {
		return data
	}
	first := bytes.Index(data, []byte("#EXTINF:"))
	if first < 0 {
		return data
	}
	target := segmentTarget
	if m := targetDuration.FindSubmatch(data); m != nil {
		if t, err := strconv.Atoi(string(m[1])); err == nil && t > 0 {
			target = t
		}
	}
	nl := lineEnd(data, bytes.IndexByte(data, '\n'))
	var gaps bytes.Buffer
	for n, left := 0, start; left > 0; n++ {
		d := min(left, target)
		fmt.Fprintf(&gaps, "#EXTINF:%d.000000,%s#EXT-X-GAP%sgap-%d.ts%s", d, nl, nl, n, nl)
		left -= d
	}
	out := make([]byte, 0, len(data)+gaps.Len())
	out = append(out, data[:first]...)
	out = append(out, gaps.Bytes()...)
	return append(out, data[first:]...)
}

// segmentTarget is the target duration when a playlist names none (ffmpeg's
// segments are 6 s).
const segmentTarget = 6

var targetDuration = regexp.MustCompile(`#EXT-X-TARGETDURATION:(\d+)`)

// withoutEndlist drops a playlist's #EXT-X-ENDLIST.
func withoutEndlist(data []byte) []byte {
	i := bytes.Index(data, []byte("#EXT-X-ENDLIST"))
	if i < 0 {
		return data
	}
	end := i + len("#EXT-X-ENDLIST")
	for end < len(data) && (data[end] == '\r' || data[end] == '\n') {
		end++
	}
	return append(append([]byte{}, data[:i]...), data[end:]...)
}

// reloaded handles a player's (re)load of a session's playlist or MPD: a
// transcode over short of the video's end (stopped by a close or the reaper,
// or failed) answers 410, so the player opens the video again where it is
// instead of ending there or waiting for segments that never come; while the
// transcode runs, the reload is activity (a paused player keeps its session).
// False when it answered.
func (h *ManifestHandler) reloaded(w http.ResponseWriter, r *http.Request, row *session.Row) bool {
	if row.TranscodeCut() {
		httpx.Write(w, http.StatusGone, "transcode-stopped", "the transcode stopped before the end",
			"open the session again at the position reached")
		return false
	}
	if row.Transcoder != nil && row.Transcoder.Active() {
		_ = h.Sessions.Touch(r.Context(), row.ID, h.now())
	}
	return true
}

// overBeforeWritten answers 410 for a playlist not on disk whose transcode is over
// short of the end (it failed before its first segment): a 404 would have the player
// wait for it, buffering, where a 410 has it open the video again or say it cannot.
func (h *ManifestHandler) overBeforeWritten(w http.ResponseWriter, r *http.Request, sub string) bool {
	id, err := uuid.Parse(sub)
	if err != nil {
		return false
	}
	row, ok, err := h.Sessions.Get(r.Context(), id)
	if err != nil || !ok || !row.TranscodeCut() {
		return false
	}
	return !h.reloaded(w, r, row)
}

func (h *ManifestHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now().UTC()
}

func manifestContentType(isDASH bool) string {
	if isDASH {
		return "application/dash+xml"
	}
	return "application/vnd.apple.mpegurl"
}

func segmentContentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".ts"):
		return "video/MP2T"
	case strings.HasSuffix(name, ".m4s"):
		return "video/iso.segment"
	case strings.HasSuffix(name, ".mp4"):
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

// BuildMasterPlaylist composes the HLS master m3u8 from the ladder
// (Story 8.5 §4.3 shape). Delegates to ffmpeg.BuildMasterPlaylistFor so
// the on-disk master the orchestrator writes (HLB-328) and what tests
// assert come from a single source of truth.
func BuildMasterPlaylist(ladder []ffmpeg.Rendition) []byte {
	return ffmpeg.BuildMasterPlaylistFor(ladder)
}
