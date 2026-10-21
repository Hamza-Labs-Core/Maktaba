package channel

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/procattr"
)

// LiveResolver turns a live block's source_url into something ffmpeg can
// open: page URLs (YouTube, Twitch, …) go through yt-dlp; direct stream
// URLs are used as is.
type LiveResolver interface {
	Resolve(ctx context.Context, req LiveRequest) (string, error)
}

// LiveRequest is one live block's lookup.
type LiveRequest struct {
	SourceURL string
	// MaxHeight is the tallest rendition asked for: the channel's output
	// height (rules tv_output resolution); 0: DefaultProfile's.
	MaxHeight int
	// Reuse is how long a lookup is shared (rules tv
	// live_lookup_reuse_seconds); 0: DefaultLiveLookupReuse.
	Reuse time.Duration
}

// DefaultLiveLookupReuse is rules tv live_lookup_reuse_seconds' default.
const DefaultLiveLookupReuse = 10 * time.Minute

// liveLookupTimeout bounds one yt-dlp run.
const liveLookupTimeout = 45 * time.Second

// YtDlp is the production LiveResolver, one per process: every channel
// airing the same page at the same height shares one lookup. A resolved
// URL is reused for the request's Reuse (it is signed and expires, but
// re-resolving on every block/retry would hammer the site), and a lookup
// already running is joined rather than started again.
type YtDlp struct {
	Bin string
	// run executes yt-dlp; tests substitute it.
	run func(ctx context.Context, bin string, args ...string) ([]byte, error)

	mu       sync.Mutex
	cache    map[ytKey]ytEntry
	inflight singleflight.Group
}

type ytKey struct {
	url    string
	height int
}

type ytEntry struct {
	url string
	at  time.Time
}

// NewYtDlp builds a resolver around the yt-dlp binary (MAKTABA_YTDLP).
func NewYtDlp(bin string) *YtDlp {
	if bin == "" {
		bin = "yt-dlp"
	}
	return &YtDlp{Bin: bin, cache: map[ytKey]ytEntry{}, run: runOutput}
}

func runOutput(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	procattr.Tie(cmd)
	return cmd.Output()
}

// IsDirectStream reports whether u already points at a stream ffmpeg can
// read (a playlist/media file URL or a streaming protocol), as opposed
// to a web page that needs yt-dlp.
func IsDirectStream(u string) bool {
	pu, err := url.Parse(strings.TrimSpace(u))
	if err != nil || pu.Scheme == "" {
		return false
	}
	switch strings.ToLower(pu.Scheme) {
	case "rtmp", "rtmps", "rtsp", "srt":
		return pu.Host != ""
	case "http", "https":
		if pu.Host == "" {
			return false
		}
	default:
		return false // file:, udp:, concat:, … are never accepted from the DB
	}
	switch strings.ToLower(path.Ext(pu.Path)) {
	case ".m3u8", ".m3u", ".ts", ".mp4", ".mkv", ".flv", ".mpd", ".aac", ".mp3", ".webm", ".mov":
		return true
	}
	return false
}

// Resolve returns a playable URL for req.SourceURL.
func (y *YtDlp) Resolve(ctx context.Context, req LiveRequest) (string, error) {
	sourceURL := strings.TrimSpace(req.SourceURL)
	if sourceURL == "" {
		return "", errors.New("live: empty source_url")
	}
	if IsDirectStream(sourceURL) {
		return sourceURL, nil
	}
	if !isHTTP(sourceURL) {
		return "", errors.New("live: source_url must be an http(s) page or a stream URL")
	}
	height := req.MaxHeight
	if height <= 0 {
		height = DefaultProfile().Height
	}
	reuse := req.Reuse
	if reuse <= 0 {
		reuse = DefaultLiveLookupReuse
	}
	key := ytKey{url: sourceURL, height: height}
	y.mu.Lock()
	if e, ok := y.cache[key]; ok && time.Since(e.at) < reuse {
		y.mu.Unlock()
		return e.url, nil
	}
	y.mu.Unlock()

	// Channels asking at once share the one run. It is not tied to the
	// first caller's ctx (one station stopping must not fail the others);
	// each caller stops waiting when its own ctx ends.
	ch := y.inflight.DoChan(strconv.Itoa(height)+" "+sourceURL, func() (any, error) {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveLookupTimeout)
		defer cancel()
		resolved, err := y.lookup(lctx, sourceURL, height)
		if err != nil {
			return "", err
		}
		y.mu.Lock()
		y.cache[key] = ytEntry{url: resolved, at: time.Now()}
		y.mu.Unlock()
		return resolved, nil
	})
	select {
	case r := <-ch:
		if r.Err != nil {
			return "", r.Err
		}
		return r.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// lookup runs yt-dlp for the best rendition no taller than height. Like
// the search's yt-dlp, it ignores any yt-dlp config on the host and never
// updates itself.
func (y *YtDlp) lookup(ctx context.Context, sourceURL string, height int) (string, error) {
	// "--" ends option parsing: a source_url can never be read as a flag.
	out, err := y.run(ctx, y.Bin, "-f", "best[height<="+strconv.Itoa(height)+"][protocol^=m3u8]/best",
		"-g", "--no-playlist", "--ignore-config", "--no-update", "--", sourceURL)
	if err != nil {
		return "", err
	}
	var resolved string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			resolved = l
			break
		}
	}
	if resolved == "" {
		return "", errors.New("live: yt-dlp returned no URL")
	}
	if !IsStreamURL(resolved) {
		return "", errors.New("live: yt-dlp returned a non-network URL")
	}
	return resolved, nil
}

// Forget drops sourceURL's cached resolutions, at every height (after
// the resolved URL failed).
func (y *YtDlp) Forget(sourceURL string) {
	sourceURL = strings.TrimSpace(sourceURL)
	y.mu.Lock()
	for k := range y.cache {
		if k.url == sourceURL {
			delete(y.cache, k)
		}
	}
	y.mu.Unlock()
}
