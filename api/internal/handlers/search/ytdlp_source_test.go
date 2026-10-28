package search

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCleanSourceURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://www.youtube.com/@lessons":                        "https://www.youtube.com/@lessons",
		"youtube.com/@lessons/videos":                             "https://youtube.com/@lessons/videos",
		"http://m.youtube.com/channel/UCaaaaaaaaaaaaaaaaaaaaaa#x": "https://m.youtube.com/channel/UCaaaaaaaaaaaaaaaaaaaaaa",
		"https://www.youtube.com/playlist?list=PLabcdefghij":      "https://www.youtube.com/playlist?list=PLabcdefghij",
		" https://www.youtube.com/c/Old ":                         "https://www.youtube.com/c/Old",
		"https://www.youtube.com/user/someone":                    "https://www.youtube.com/user/someone",
	} {
		got, err := CleanSourceURL(raw)
		if err != nil || got != want {
			t.Errorf("%q -> %q %v, want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"", "https://www.youtube.com/watch?v=abcdefghijk", "https://youtu.be/abcdefghijk",
		"https://evil.example/@x", "https://www.youtube.com.evil/@x", "https://u:p@www.youtube.com/@x",
		"https://www.youtube.com:8443/@x", "ftp://www.youtube.com/@x", "https://www.youtube.com/playlist?list=x",
		"--exec=rm https://www.youtube.com/@x", "https://www.youtube.com/@x\n--exec"} {
		if got, err := CleanSourceURL(bad); !errors.Is(err, ErrNotASource) {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestParseSource(t *testing.T) {
	ch, err := parseSource([]byte(`{"_type":"playlist","id":"UCaaaaaaaaaaaaaaaaaaaaaa","title":"Lessons - Videos",
		"channel":"Lessons","channel_id":"UCaaaaaaaaaaaaaaaaaaaaaa","entries":[{"id":"abcdefghijk"}]}`))
	if err != nil || ch.Kind != "channel" || ch.ExternalID != "UCaaaaaaaaaaaaaaaaaaaaaa" || ch.Title != "Lessons" ||
		ch.URL != "https://www.youtube.com/channel/UCaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("channel = %+v %v", ch, err)
	}
	// a channel page answered by its handle: the channel id is channel_id
	h, err := parseSource([]byte(`{"_type":"playlist","id":"@lessons","title":"Lessons - Videos","channel_id":"UCbbbbbbbbbbbbbbbbbbbbbb"}`))
	if err != nil || h.ExternalID != "UCbbbbbbbbbbbbbbbbbbbbbb" || h.Title != "Lessons" {
		t.Fatalf("handle = %+v %v", h, err)
	}
	pl, err := parseSource([]byte(`{"_type":"playlist","id":"PLabcdefghij","title":"Seerah","channel_id":"UCaaaaaaaaaaaaaaaaaaaaaa"}`))
	if err != nil || pl.Kind != "playlist" || pl.ExternalID != "PLabcdefghij" ||
		pl.URL != "https://www.youtube.com/playlist?list=PLabcdefghij" || pl.Title != "Seerah" {
		t.Fatalf("playlist = %+v %v", pl, err)
	}
	for _, bad := range []string{`{"_type":"video","id":"abcdefghijk"}`, `{"_type":"playlist","id":"x"}`, `not json`} {
		if _, err := parseSource([]byte(bad)); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
}

func TestResolveSourceRunsOneFlatPage(t *testing.T) {
	var args []string
	y := NewYtDlpSearcher("yt-dlp")
	y.lookPath = func(string) (string, error) { return "/bin/yt-dlp", nil }
	y.run = func(_ context.Context, bin string, a ...string) ([]byte, error) {
		args = append([]string{bin}, a...)
		return []byte(`{"_type":"playlist","id":"PLabcdefghij","title":"Seerah"}`), nil
	}
	got, err := y.ResolveSource(context.Background(), "https://www.youtube.com/playlist?list=PLabcdefghij")
	if err != nil || got.ExternalID != "PLabcdefghij" {
		t.Fatalf("resolve = %+v %v", got, err)
	}
	line := strings.Join(args, " ")
	if !strings.Contains(line, "--flat-playlist -J --playlist-end 1") ||
		!strings.HasSuffix(line, "-- https://www.youtube.com/playlist?list=PLabcdefghij") {
		t.Fatalf("argv = %v", args)
	}
	if _, err := y.ResolveSource(context.Background(), "https://evil.example/x"); !errors.Is(err, ErrNotASource) {
		t.Fatalf("a foreign URL reached yt-dlp: %v", err)
	}
	y2 := NewYtDlpSearcher("yt-dlp")
	y2.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if _, err := y2.ResolveSource(context.Background(), "https://www.youtube.com/@x"); !errors.Is(err, ErrYouTubeDisabled) {
		t.Fatalf("no yt-dlp: %v", err)
	}
}
