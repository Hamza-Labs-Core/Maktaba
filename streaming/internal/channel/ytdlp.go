package channel

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"
)

// LiveResolver turns a live block's source_url into something ffmpeg can
// open: page URLs (YouTube, Twitch, …) go through yt-dlp; direct stream
// URLs are used as is.
type LiveResolver interface {
	Resolve(ctx context.Context, sourceURL string) (string, error)
}

// YtDlp is the production LiveResolver. Resolved URLs are cached for TTL
// (default 10 min) — they are signed and expire, but re-resolving on
// every block/retry would hammer the site.
type YtDlp struct {
	Bin string
	TTL time.Duration
	// run executes yt-dlp; tests substitute it.
	run func(ctx context.Context, bin string, args ...string) ([]byte, error)

	mu    sync.Mutex
	cache map[string]ytEntry
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
	return &YtDlp{Bin: bin, TTL: 10 * time.Minute, cache: map[string]ytEntry{}, run: runOutput}
}

func runOutput(ctx context.Context, bin string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, bin, args...).Output()
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
	case "rtmp", "rtmps", "rtsp", "srt", "udp", "rtp", "mms", "file":
		return true
	case "http", "https":
	default:
		return false
	}
	switch strings.ToLower(path.Ext(pu.Path)) {
	case ".m3u8", ".m3u", ".ts", ".mp4", ".mkv", ".flv", ".mpd", ".aac", ".mp3", ".webm", ".mov":
		return true
	}
	return false
}

// Resolve returns a playable URL for sourceURL.
func (y *YtDlp) Resolve(ctx context.Context, sourceURL string) (string, error) {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return "", errors.New("live: empty source_url")
	}
	if IsDirectStream(sourceURL) {
		return sourceURL, nil
	}
	y.mu.Lock()
	if e, ok := y.cache[sourceURL]; ok && time.Since(e.at) < y.TTL {
		y.mu.Unlock()
		return e.url, nil
	}
	y.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := y.run(ctx, y.Bin, "-f", "best[height<=1080][protocol^=m3u8]/best", "-g", "--no-playlist", sourceURL)
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
	y.mu.Lock()
	y.cache[sourceURL] = ytEntry{url: resolved, at: time.Now()}
	y.mu.Unlock()
	return resolved, nil
}

// Forget drops a cached resolution (after the resolved URL failed).
func (y *YtDlp) Forget(sourceURL string) {
	y.mu.Lock()
	delete(y.cache, strings.TrimSpace(sourceURL))
	y.mu.Unlock()
}
