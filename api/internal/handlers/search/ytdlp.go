package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// YtDlpSearcher is the production YouTubeSearcher (Story 26.8): it runs
// `yt-dlp ytsearchN:<query>` in flat-playlist mode, so no API key is
// needed and nothing is downloaded. It is the same binary the TV live
// slots use (MAKTABA_YTDLP, else yt-dlp on PATH).
//
// Every search has a hard timeout and at most MaxConcurrent searches run
// at once, so a burst of queries never fans out into many yt-dlp
// processes. Results are cached by the handler (youtube_search_cache).
type YtDlpSearcher struct {
	// Bin is the yt-dlp binary: a path or a name looked up on PATH.
	Bin string
	// Limit is N in ytsearchN (default 10).
	Limit int
	// Timeout caps one yt-dlp run (default DefaultYtDlpTimeout).
	Timeout time.Duration
	// MaxConcurrent caps simultaneous yt-dlp runs (default 2).
	MaxConcurrent int

	// run executes yt-dlp and returns its stdout; tests substitute it.
	run func(ctx context.Context, bin string, args ...string) ([]byte, error)
	// lookPath resolves Bin; tests substitute it.
	lookPath func(string) (string, error)

	once sync.Once
	sem  chan struct{}

	mu        sync.Mutex
	availAt   time.Time
	availPath string
}

// DefaultYtDlpTimeout caps one search. A flat ytsearch normally answers
// in two to five seconds.
const DefaultYtDlpTimeout = 15 * time.Second

// availabilityTTL is how long a PATH lookup is trusted, so installing
// yt-dlp later is picked up without a restart.
const availabilityTTL = time.Minute

// maxQueryRunes caps the query text handed to yt-dlp.
const maxQueryRunes = 200

// NewYtDlpSearcher builds a searcher around bin ("" ⇒ "yt-dlp").
func NewYtDlpSearcher(bin string) *YtDlpSearcher {
	if strings.TrimSpace(bin) == "" {
		bin = "yt-dlp"
	}
	return &YtDlpSearcher{Bin: bin}
}

func (y *YtDlpSearcher) init() {
	y.once.Do(func() {
		n := y.MaxConcurrent
		if n <= 0 {
			n = 2
		}
		y.sem = make(chan struct{}, n)
		if y.run == nil {
			y.run = runYtDlp
		}
		if y.lookPath == nil {
			y.lookPath = exec.LookPath
		}
	})
}

func runYtDlp(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	// A killed yt-dlp can leave its pipes open through a child; never
	// wait on them past the deadline.
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("yt-dlp: %w: %s", err, msg)
	}
	return out, nil
}

// binPath resolves the binary, caching the answer for availabilityTTL.
func (y *YtDlpSearcher) binPath() (string, bool) {
	y.init()
	y.mu.Lock()
	defer y.mu.Unlock()
	if !y.availAt.IsZero() && time.Since(y.availAt) < availabilityTTL {
		return y.availPath, y.availPath != ""
	}
	p, err := y.lookPath(y.Bin)
	if err != nil {
		p = ""
	}
	y.availPath, y.availAt = p, time.Now()
	return p, p != ""
}

// Available reports whether yt-dlp can be run on this machine. The Search
// page hides the "From YouTube" section when it cannot.
func (y *YtDlpSearcher) Available() bool {
	_, ok := y.binPath()
	return ok
}

// SearchYouTube runs one flat ytsearch for query.
func (y *YtDlpSearcher) SearchYouTube(ctx context.Context, query string) ([]YTItem, error) {
	bin, ok := y.binPath()
	if !ok {
		return nil, ErrYouTubeDisabled
	}
	q := cleanQuery(query)
	if q == "" {
		return []YTItem{}, nil
	}
	timeout := y.Timeout
	if timeout <= 0 {
		timeout = DefaultYtDlpTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case y.sem <- struct{}{}:
		defer func() { <-y.sem }()
	case <-ctx.Done():
		return nil, ErrYouTubeRateLimited
	}

	limit := y.Limit
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	// The query is one argument after "--", so yt-dlp never reads it as
	// an option; the ytsearchN: prefix makes it a search, never a URL.
	out, err := y.run(ctx, bin,
		"--flat-playlist", "-J", "--no-warnings", "--ignore-config", "--no-update",
		"--extractor-args", "youtubetab:approximate_date",
		"--", fmt.Sprintf("ytsearch%d:%s", limit, q))
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("youtube search timed out: %w", ctx.Err())
		}
		return nil, err
	}
	return parseYtDlpSearch(out)
}

// cleanQuery drops control characters (newlines included) and caps the
// length. Leading dashes are harmless (the query follows "--" and the
// ytsearch prefix) but are trimmed anyway so the text is plain words.
func cleanQuery(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			r = ' '
		}
		b.WriteRune(r)
		n++
		if n >= maxQueryRunes {
			break
		}
	}
	return strings.TrimLeft(strings.Join(strings.Fields(b.String()), " "), "-")
}

// ytdlpPlaylist is the subset of `yt-dlp -J --flat-playlist` read here.
type ytdlpPlaylist struct {
	Entries []ytdlpEntry `json:"entries"`
}

type ytdlpEntry struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Description      *string  `json:"description"`
	Duration         *float64 `json:"duration"`
	Channel          string   `json:"channel"`
	Uploader         string   `json:"uploader"`
	ViewCount        *int64   `json:"view_count"`
	UploadDate       string   `json:"upload_date"`
	Timestamp        *float64 `json:"timestamp"`
	ReleaseTimestamp *float64 `json:"release_timestamp"`
	Thumbnails       []struct {
		URL    string `json:"url"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"thumbnails"`
}

// youtubeIDRe is the shape of a YouTube video id.
var youtubeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ValidYouTubeID reports whether id looks like a YouTube video id.
func ValidYouTubeID(id string) bool { return youtubeIDRe.MatchString(id) }

// parseYtDlpSearch maps yt-dlp's JSON to YTItems. Entries that are not
// videos (channels, playlists) or carry a malformed id are skipped.
func parseYtDlpSearch(raw []byte) ([]YTItem, error) {
	var pl ytdlpPlaylist
	if err := json.Unmarshal(bytes.TrimSpace(raw), &pl); err != nil {
		return nil, errors.New("yt-dlp: unreadable search output")
	}
	items := make([]YTItem, 0, len(pl.Entries))
	for _, e := range pl.Entries {
		if !ValidYouTubeID(e.ID) || strings.TrimSpace(e.Title) == "" {
			continue
		}
		it := YTItem{
			YouTubeID: e.ID,
			Title:     strings.TrimSpace(e.Title),
			Channel:   strings.TrimSpace(e.Channel),
			ViewCount: e.ViewCount,
			Thumbnail: bestThumbnail(e),
		}
		if it.Channel == "" {
			it.Channel = strings.TrimSpace(e.Uploader)
		}
		if e.Description != nil {
			it.Description = snippet(*e.Description, 300)
		}
		if e.Duration != nil && *e.Duration > 0 {
			d := int(math.Round(*e.Duration))
			it.DurationSec = &d
		}
		it.PublishedAt = publishedAt(e)
		items = append(items, it)
	}
	return items, nil
}

func bestThumbnail(e ytdlpEntry) string {
	best, bestArea := "", -1
	for _, t := range e.Thumbnails {
		if !SafeThumbnailURL(t.URL) {
			continue
		}
		if a := t.Width * t.Height; a > bestArea {
			best, bestArea = t.URL, a
		}
	}
	if best == "" {
		best = "https://i.ytimg.com/vi/" + e.ID + "/hqdefault.jpg"
	}
	return best
}

// SafeThumbnailURL accepts only https URLs on YouTube's image hosts, so a
// staged thumbnail can never point the library at an arbitrary site.
func SafeThumbnailURL(u string) bool {
	rest, ok := strings.CutPrefix(u, "https://")
	if !ok {
		return false
	}
	host, _, _ := strings.Cut(rest, "/")
	host = strings.ToLower(host)
	return host == "i.ytimg.com" || strings.HasSuffix(host, ".ytimg.com") ||
		host == "yt3.ggpht.com" || strings.HasSuffix(host, ".googleusercontent.com")
}

func publishedAt(e ytdlpEntry) string {
	if len(e.UploadDate) == 8 {
		if t, err := time.Parse("20060102", e.UploadDate); err == nil {
			return t.Format("2006-01-02")
		}
	}
	for _, ts := range []*float64{e.ReleaseTimestamp, e.Timestamp} {
		if ts != nil && *ts > 0 {
			return time.Unix(int64(*ts), 0).UTC().Format("2006-01-02")
		}
	}
	return ""
}

func snippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}
