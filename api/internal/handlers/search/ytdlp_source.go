package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ResolvedSource is a YouTube channel or playlist a library can follow
// (Epic 47's youtube_sources): its kind, its stable id and the URL the
// pipeline lists it from.
type ResolvedSource struct {
	Kind       string `json:"kind"` // channel | playlist
	ExternalID string `json:"external_id"`
	URL        string `json:"url"`
	Title      string `json:"title"`
}

// ErrNotASource is a URL that is not a YouTube channel or playlist.
var ErrNotASource = errors.New("not a YouTube channel or playlist")

// DefaultResolveTimeout caps one source resolution: yt-dlp reads one page.
const DefaultResolveTimeout = 30 * time.Second

var (
	channelIDRe  = regexp.MustCompile(`^UC[A-Za-z0-9_-]{22}$`)
	playlistIDRe = regexp.MustCompile(`^(?:PL|OL|UU|FL|LL)[A-Za-z0-9_-]{8,64}$`)
	sourceHosts  = map[string]bool{"youtube.com": true, "www.youtube.com": true, "m.youtube.com": true,
		"music.youtube.com": true}
)

// CleanSourceURL accepts an http(s) URL on YouTube's own hosts (a channel,
// @handle or playlist page) and returns it normalised, else ErrNotASource.
func CleanSourceURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 500 || strings.ContainsAny(raw, " \t\r\n\\") {
		return "", ErrNotASource
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
		return "", ErrNotASource
	}
	if !sourceHosts[strings.ToLower(u.Hostname())] {
		return "", ErrNotASource
	}
	p := u.EscapedPath()
	switch {
	case strings.HasPrefix(p, "/playlist") && playlistIDRe.MatchString(u.Query().Get("list")):
	case strings.HasPrefix(p, "/@"), strings.HasPrefix(p, "/channel/"), strings.HasPrefix(p, "/c/"),
		strings.HasPrefix(p, "/user/"):
	default:
		return "", ErrNotASource
	}
	u.Scheme, u.Fragment = "https", ""
	return u.String(), nil
}

type ytdlpSource struct {
	Type      string `json:"_type"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
	Uploader  string `json:"uploader"`
}

// parseSource maps yt-dlp's -J of a channel or playlist page.
func parseSource(raw []byte) (ResolvedSource, error) {
	var s ytdlpSource
	if err := json.Unmarshal(bytes.TrimSpace(raw), &s); err != nil {
		return ResolvedSource{}, errors.New("yt-dlp: unreadable output")
	}
	if s.Type != "playlist" {
		return ResolvedSource{}, ErrNotASource
	}
	switch {
	case playlistIDRe.MatchString(s.ID):
		return ResolvedSource{Kind: "playlist", ExternalID: s.ID,
			URL: "https://www.youtube.com/playlist?list=" + s.ID, Title: strings.TrimSpace(s.Title)}, nil
	case channelIDRe.MatchString(s.ID) || channelIDRe.MatchString(s.ChannelID):
		id := s.ChannelID
		if !channelIDRe.MatchString(id) {
			id = s.ID
		}
		title := strings.TrimSpace(s.Channel)
		if title == "" {
			title = strings.TrimSpace(s.Uploader)
		}
		if title == "" {
			title = strings.TrimSpace(s.Title)
			for _, tab := range []string{" - Videos", " - Shorts", " - Live", " - Home"} {
				title = strings.TrimSuffix(title, tab)
			}
		}
		return ResolvedSource{Kind: "channel", ExternalID: id, URL: "https://www.youtube.com/channel/" + id,
			Title: title}, nil
	}
	return ResolvedSource{}, ErrNotASource
}

// ResolveSource names the channel or playlist at rawURL with
// `yt-dlp --flat-playlist -J --playlist-end 1`: one page read, nothing
// downloaded. It shares the searcher's binary, timeout cap and concurrency.
func (y *YtDlpSearcher) ResolveSource(ctx context.Context, rawURL string) (ResolvedSource, error) {
	clean, err := CleanSourceURL(rawURL)
	if err != nil {
		return ResolvedSource{}, err
	}
	bin, ok := y.binPath()
	if !ok {
		return ResolvedSource{}, ErrYouTubeDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultResolveTimeout)
	defer cancel()
	select {
	case y.sem <- struct{}{}:
		defer func() { <-y.sem }()
	case <-ctx.Done():
		return ResolvedSource{}, ErrYouTubeRateLimited
	}
	// The URL is one argument after "--", so yt-dlp never reads it as an option.
	out, err := y.run(ctx, bin, "--flat-playlist", "-J", "--playlist-end", "1", "--no-warnings",
		"--ignore-config", "--no-update", "--", clean)
	if err != nil {
		if ctx.Err() != nil {
			return ResolvedSource{}, fmt.Errorf("youtube source timed out: %w", ctx.Err())
		}
		return ResolvedSource{}, err
	}
	return parseSource(out)
}
