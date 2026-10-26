// Package handlers contains the byte-pumping HTTP handlers
// (Stories 8.3 direct, 8.4 remux, 8.5 HLS manifests/segments,
// 8.11 subtitles, 8.12 chapters, 8.13 posters/sprites/thumbs).
package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
)

// FileOpener abstracts os.Open so tests inject in-memory readers.
type FileOpener interface {
	Open(path string) (FileReader, error)
}

// FileReader is the subset of *os.File the direct-play handler needs:
// stat for size+mtime, range reads via ReadAt, and Close. We hide the
// concrete *os.File so tests can mock it.
type FileReader interface {
	Stat() (os.FileInfo, error)
	Read(p []byte) (int, error)
	Seek(offset int64, whence int) (int64, error)
	Close() error
}

// OSFileOpener implements FileOpener with the real filesystem.
type OSFileOpener struct{}

// Open opens a file on disk. Returns an error compatible with handler
// expectations (NotFound vs other I/O).
func (OSFileOpener) Open(path string) (FileReader, error) { return os.Open(path) }

// DirectHandler serves Story 8.3 — range-served direct play.
type DirectHandler struct {
	Probe    *probe.Cache
	Profiles *capability.Registry
	Files    FileOpener
	NowFn    func() time.Time
	// AllowAllProfiles, when true, skips the matrix decision and serves
	// any video as direct. Used by the remux handler after the file has
	// already been transcoded into a direct-playable container.
	AllowAllProfiles bool
	// Logger receives the file's open and read failures (nil: slog's default).
	Logger *slog.Logger
}

func (h *DirectHandler) log() *slog.Logger {
	if h.Logger == nil {
		return slog.Default()
	}
	return h.Logger
}

// errSourceRead marks a failed read of the source file (not of the client's
// connection): copyReader wraps it.
type errSourceRead struct{ err error }

func (e errSourceRead) Error() string { return "read source: " + e.err.Error() }
func (e errSourceRead) Unwrap() error { return e.err }

// copyFailed logs a copy that stopped on the source file (a NAS read error);
// one the client went away from is not the server's failure.
func (h *DirectHandler) copyFailed(videoID uuid.UUID, path string, offset, written int64, err error) {
	var src errSourceRead
	if err == nil || !errors.As(err, &src) {
		return
	}
	h.log().Error("direct play: the video file could not be read", "video_id", videoID.String(),
		"path", path, "offset", offset, "written", written, "err", src.err.Error(),
		"event", "vod_direct_read_failed")
}

// ServeHTTP handles GET and HEAD on /stream/direct/{video_id}. The
// signed-URL middleware has already validated the JWT; we read claims
// from context for logging.
func (h *DirectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		httpx.Write(w, http.StatusMethodNotAllowed, "method-not-allowed", "method not allowed", "")
		return
	}

	subj := auth.SubjectFromContext(r.Context())
	videoID, err := uuid.Parse(subj)
	if err != nil {
		httpx.Write(w, http.StatusBadRequest, "bad-video-id", "video id not a UUID", err.Error())
		return
	}

	row, err := h.Probe.Lookup(r.Context(), videoID)
	if err != nil {
		if errors.Is(err, probe.ErrNotProbed) {
			httpx.Write(w, http.StatusFailedDependency, "video-not-probed",
				"video has no media_info row", "Pipeline must probe before streaming")
			return
		}
		httpx.Write(w, http.StatusNotFound, "video-not-found", "video not found", err.Error())
		return
	}

	// Profile/matrix decision: AC-4 says if the video isn't direct-playable for the
	// requesting profile, the response is a 409 with a hint to use the manifest URL.
	if !h.AllowAllProfiles {
		if profileName := r.URL.Query().Get("profile"); profileName != "" {
			profile, _ := h.Profiles.Get(profileName)
			src := capability.Source{
				Container: row.Container, VideoCodec: row.VideoCodec, AudioCodec: row.AudioCodec,
				Height: row.Height, BitrateKbps: row.BitrateKbps, AudioChannels: row.AudioChannels,
			}
			v := h.Profiles.Decide(profile, src, capability.Override{})
			if v.Mode != capability.ModeDirect {
				httpx.Write(w, http.StatusConflict, "not-direct-playable",
					"video not direct-playable for profile",
					fmt.Sprintf("profile=%s mode=%s reason=%s", profileName, v.Mode, v.Reason))
				return
			}
		}
	}

	// row.Path is a server-trusted absolute media path written by
	// Pipeline into media_info — the request only ever supplies the
	// validated video UUID, never a path component. Normalise and
	// assert the invariant (absolute, no traversal artefact) at the
	// filesystem boundary so a corrupt/poisoned probe row cannot widen
	// the open and so the Clean + containment barrier is visible to
	// static analysis (CodeQL go/path-injection).
	// Story 31.3: a canonical smb://server/share/... path is this
	// machine's mount of the share.
	local := pathmap.Resolve(row.Path)
	mediaPath := filepath.Clean(local)
	if !filepath.IsAbs(mediaPath) || mediaPath != filepath.Clean(local) || pathHasTraversal(mediaPath) {
		httpx.Write(w, http.StatusNotFound, "video-bytes-missing", "video file not on disk", "")
		return
	}
	f, err := h.Files.Open(mediaPath)
	if err != nil {
		// a missing file is a Warn; another failure (macOS privacy denial, NAS
		// unmounted, EIO) an Error: the file is there and cannot be read
		level := slog.LevelError
		if errors.Is(err, fs.ErrNotExist) {
			level = slog.LevelWarn
		}
		h.log().Log(r.Context(), level, "direct play: the video file could not be opened",
			"video_id", videoID.String(), "path", mediaPath, "err", err.Error(), "event", "vod_direct_open_failed")
		httpx.Write(w, http.StatusNotFound, "video-bytes-missing", "video file not on disk", err.Error())
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		h.log().Error("direct play: the video file could not be read", "video_id", videoID.String(),
			"path", mediaPath, "err", err.Error(), "event", "vod_direct_read_failed")
		httpx.Write(w, http.StatusInternalServerError, "stat-failed", "stat failed", err.Error())
		return
	}

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", contentTypeFor(row))
	w.Header().Set("Last-Modified", stat.ModTime().UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "private, max-age=60")

	rng := r.Header.Get("Range")
	if rng == "" {
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		n, cerr := copyReader(w, f, 0, stat.Size())
		h.copyFailed(videoID, mediaPath, 0, n, cerr)
		return
	}

	start, end, multi, err := parseRange(rng, stat.Size())
	if multi {
		// AC-3: multipart byteranges → 416. Players degrade.
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(stat.Size(), 10))
		httpx.Write(w, http.StatusRequestedRangeNotSatisfiable, "multi-range-unsupported",
			"multipart byteranges not supported",
			"server only emits single-range 206 responses; resend with one Range value")
		return
	}
	if err != nil {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(stat.Size(), 10))
		httpx.Write(w, http.StatusRequestedRangeNotSatisfiable, "bad-range",
			"range not satisfiable", err.Error())
		return
	}
	length := end - start + 1
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, stat.Size()))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusPartialContent)
		return
	}
	w.WriteHeader(http.StatusPartialContent)
	// a client that went away ends the copy too: only a source failure is logged
	n, cerr := copyReader(w, f, start, length)
	h.copyFailed(videoID, mediaPath, start, n, cerr)
}

// pathHasTraversal reports whether p still contains a "." or ".."
// path element after cleaning — i.e. it is not a fully-resolved
// canonical absolute path. A clean absolute media path never does.
func pathHasTraversal(p string) bool {
	for _, seg := range strings.Split(p, string(filepath.Separator)) {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// parseRange parses a single-range Range header. Returns start, end,
// whether the request was multipart (which we reject), and an error
// for any other malformed/unsatisfiable range.
func parseRange(value string, size int64) (start, end int64, multi bool, err error) {
	const prefix = "bytes="
	if !strings.HasPrefix(value, prefix) {
		return 0, 0, false, errors.New("malformed Range header (no bytes=)")
	}
	spec := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if strings.Contains(spec, ",") {
		return 0, 0, true, nil
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, errors.New("malformed range spec")
	}
	if parts[0] == "" {
		// suffix range: "bytes=-N" → last N bytes
		n, perr := strconv.ParseInt(parts[1], 10, 64)
		if perr != nil || n <= 0 {
			return 0, 0, false, errors.New("bad suffix range")
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, false, nil
	}
	s, perr := strconv.ParseInt(parts[0], 10, 64)
	if perr != nil || s < 0 {
		return 0, 0, false, errors.New("bad start")
	}
	if parts[1] == "" {
		return s, size - 1, false, nil
	}
	e, perr := strconv.ParseInt(parts[1], 10, 64)
	if perr != nil || e < s {
		return 0, 0, false, errors.New("bad end")
	}
	if e >= size {
		e = size - 1
	}
	if s >= size {
		return 0, 0, false, errors.New("start past EOF")
	}
	return s, e, false, nil
}

// copyReader copies length bytes starting at offset from r to w.
// Uses Seek + io.CopyN-style loop so we don't need ReadAt. A failure of the
// source (seek, read) comes back as errSourceRead; one of w as itself.
func copyReader(w http.ResponseWriter, r FileReader, offset, length int64) (int64, error) {
	if offset > 0 {
		if _, err := r.Seek(offset, 0); err != nil {
			return 0, errSourceRead{err}
		}
	}
	buf := make([]byte, 32*1024)
	var written int64
	remaining := length
	for remaining > 0 {
		n := int64(len(buf))
		if n > remaining {
			n = remaining
		}
		read, rerr := r.Read(buf[:n])
		if read > 0 {
			if _, werr := w.Write(buf[:read]); werr != nil {
				return written, werr
			}
			written += int64(read)
			remaining -= int64(read)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return written, nil
			}
			return written, errSourceRead{rerr}
		}
	}
	return written, nil
}

// contentTypeFor picks the Content-Type for a direct-play response.
// Falls back to application/octet-stream on unknowns so dumb players
// still treat the body as binary. The pipeline stores ffprobe's
// format_name as the container, a list of the names one demuxer reads
// ("mov,mp4,m4a,3gp,3g2,mj2", "matroska,webm"): any of them counts, and
// a .webm file is WebM (Matroska and WebM probe alike). A
// player that cannot sniff (AVPlayer on a URL with no extension) opens
// only a file it is told the type of.
func contentTypeFor(row *probe.Row) string {
	if row.MIMEType != "" {
		return row.MIMEType
	}
	// one demuxer reads both: the file says which it is
	if strings.HasSuffix(strings.ToLower(row.Path), ".webm") {
		return "video/webm"
	}
	for _, name := range strings.Split(strings.ToLower(row.Container), ",") {
		switch strings.TrimSpace(name) {
		case "mp4", "mov", "m4v":
			return "video/mp4"
		case "mkv", "matroska":
			return "video/x-matroska"
		case "webm":
			return "video/webm"
		case "ts", "m2ts", "mpegts":
			return "video/MP2T"
		case "avi":
			return "video/x-msvideo"
		}
	}
	return "application/octet-stream"
}

// HelperContext for tests.
var _ = context.Background
