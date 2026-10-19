package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a fake yt-dlp: with FAKE_YTDLP set it
// records its arguments and prints canned search output, so the tests
// run the real exec path without network or a real yt-dlp.
const fakeYtDlpEnv = "MAKTABA_FAKE_YTDLP"

const fakeSearchJSON = `{"_type":"playlist","id":"q","entries":[
 {"_type":"url","id":"AvctFaYGuR0","title":"Surah Al Kahf | Lesson 1","description":"A   long\nlesson","duration":633.0,
  "channel":"Hisham","uploader":"Hisham","view_count":99953,"upload_date":"20240115",
  "thumbnails":[{"url":"https://i.ytimg.com/vi/AvctFaYGuR0/default.jpg","width":120,"height":90},
                {"url":"https://i.ytimg.com/vi/AvctFaYGuR0/hq720.jpg","width":720,"height":404},
                {"url":"https://evil.example/x.jpg","width":4000,"height":4000}]},
 {"_type":"url","id":"bad id","title":"not a video"},
 {"_type":"url","id":"Zx9_-abcDEF","title":"Second","description":null,"duration":null,
  "channel":"","uploader":"Up Loader","timestamp":1700000000,"thumbnails":[]}
]}`

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeYtDlpEnv); mode != "" {
		os.Exit(fakeYtDlp(mode))
	}
	os.Exit(m.Run())
}

func fakeYtDlp(mode string) int {
	if f := os.Getenv("MAKTABA_FAKE_YTDLP_ARGS"); f != "" {
		b, _ := json.Marshal(os.Args[1:])
		_ = os.WriteFile(f, b, 0o600)
	}
	switch mode {
	case "ok":
		fmt.Fprint(os.Stdout, fakeSearchJSON)
		return 0
	case "slow":
		time.Sleep(10 * time.Second)
		return 0
	case "garbage":
		fmt.Fprint(os.Stdout, "not json")
		return 0
	default:
		fmt.Fprint(os.Stderr, "ERROR: boom")
		return 1
	}
}

// fakeYtDlpSearcher returns a searcher whose binary is this test executable
// in the given mode, and the file its arguments are written to.
func fakeYtDlpSearcher(t *testing.T, mode string) (*YtDlpSearcher, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args.json")
	t.Setenv(fakeYtDlpEnv, mode)
	t.Setenv("MAKTABA_FAKE_YTDLP_ARGS", argsFile)
	return NewYtDlpSearcher(exe), argsFile
}

func TestYtDlp_SearchParsesResults(t *testing.T) {
	y, argsFile := fakeYtDlpSearcher(t, "ok")
	items, err := y.SearchYouTube(context.Background(), "surah kahf")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items (bad id skipped), got %d: %+v", len(items), items)
	}
	a := items[0]
	if a.YouTubeID != "AvctFaYGuR0" || a.Title != "Surah Al Kahf | Lesson 1" || a.Channel != "Hisham" {
		t.Fatalf("first item: %+v", a)
	}
	if a.DurationSec == nil || *a.DurationSec != 633 {
		t.Fatalf("duration: %v", a.DurationSec)
	}
	if a.Thumbnail != "https://i.ytimg.com/vi/AvctFaYGuR0/hq720.jpg" {
		t.Fatalf("thumbnail must be the largest YouTube image, got %q", a.Thumbnail)
	}
	if a.PublishedAt != "2024-01-15" || a.Description != "A long lesson" {
		t.Fatalf("published/description: %q %q", a.PublishedAt, a.Description)
	}
	if a.ViewCount == nil || *a.ViewCount != 99953 {
		t.Fatalf("views: %v", a.ViewCount)
	}
	b := items[1]
	if b.Channel != "Up Loader" || b.PublishedAt != "2023-11-14" || b.DurationSec != nil {
		t.Fatalf("second item: %+v", b)
	}
	if b.Thumbnail != "https://i.ytimg.com/vi/Zx9_-abcDEF/hqdefault.jpg" {
		t.Fatalf("fallback thumbnail: %q", b.Thumbnail)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	_ = json.Unmarshal(raw, &args)
	want := []string{"--flat-playlist", "-J", "--no-warnings", "--ignore-config", "--no-update"}
	for _, w := range want {
		if !contains(args, w) {
			t.Fatalf("missing %s in %v", w, args)
		}
	}
	if len(args) < 2 || args[len(args)-2] != "--" || args[len(args)-1] != "ytsearch10:surah kahf" {
		t.Fatalf("query must be the one argument after --: %v", args)
	}
}

// Query text is data: dashes, flags and newlines never become options.
func TestYtDlp_QueryIsNeverAFlag(t *testing.T) {
	y, argsFile := fakeYtDlpSearcher(t, "ok")
	if _, err := y.SearchYouTube(context.Background(), "--exec rm\n-rf /  --output x"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsFile)
	var args []string
	_ = json.Unmarshal(raw, &args)
	dd := -1
	for i, a := range args {
		if a == "--" {
			dd = i
		}
		if a == "--exec" || a == "--output" || a == "-rf" {
			t.Fatalf("query leaked into options: %v", args)
		}
	}
	if dd != len(args)-2 {
		t.Fatalf("-- must precede the single query argument: %v", args)
	}
	last := args[len(args)-1]
	if !strings.HasPrefix(last, "ytsearch10:") || strings.ContainsAny(last, "\n\r") {
		t.Fatalf("query argument: %q", last)
	}
}

func TestYtDlp_Timeout(t *testing.T) {
	y, _ := fakeYtDlpSearcher(t, "slow")
	y.Timeout = 300 * time.Millisecond
	start := time.Now()
	_, err := y.SearchYouTube(context.Background(), "x")
	if err == nil {
		t.Fatal("want a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("timeout not enforced: %v", time.Since(start))
	}
}

func TestYtDlp_FailureAndGarbage(t *testing.T) {
	y, _ := fakeYtDlpSearcher(t, "fail")
	if _, err := y.SearchYouTube(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want yt-dlp error with stderr, got %v", err)
	}
	g, _ := fakeYtDlpSearcher(t, "garbage")
	if _, err := g.SearchYouTube(context.Background(), "x"); err == nil {
		t.Fatal("garbage output must be an error")
	}
}

func TestYtDlp_Unavailable(t *testing.T) {
	y := NewYtDlpSearcher(filepath.Join(t.TempDir(), "no-such-yt-dlp"))
	if y.Available() {
		t.Fatal("a missing binary must not be available")
	}
	if _, err := y.SearchYouTube(context.Background(), "x"); !errors.Is(err, ErrYouTubeDisabled) {
		t.Fatalf("want ErrYouTubeDisabled, got %v", err)
	}
	h := &Handler{YouTube: y}
	if b := h.fetchYouTube(context.Background(), "x", time.Second); b.Reason != "disabled" || len(b.Items) != 0 {
		t.Fatalf("got %+v", b)
	}
}

func TestYtDlp_ConcurrencyCapIsRateLimit(t *testing.T) {
	y, _ := fakeYtDlpSearcher(t, "ok")
	y.MaxConcurrent = 1
	y.init()
	y.sem <- struct{}{} // one search already running
	defer func() { <-y.sem }()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := y.SearchYouTube(ctx, "x"); !errors.Is(err, ErrYouTubeRateLimited) {
		t.Fatalf("want rate limited, got %v", err)
	}
}

func TestYouTubeStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    *Handler
		want bool
	}{
		{"no searcher", &Handler{}, false},
		{"missing binary", &Handler{YouTube: NewYtDlpSearcher(filepath.Join(t.TempDir(), "nope"))}, false},
		{"plain searcher", &Handler{YouTube: stubYT{}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ytReq(http.MethodGet, "/api/search/youtube/status", "", false)
			rec := httptest.NewRecorder()
			tc.h.YouTubeStatus(rec, r)
			var out map[string]bool
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			if rec.Code != http.StatusOK || out["available"] != tc.want {
				t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	y, _ := fakeYtDlpSearcher(t, "ok")
	rec := httptest.NewRecorder()
	(&Handler{YouTube: y}).YouTubeStatus(rec, ytReq(http.MethodGet, "/api/search/youtube/status", "", false))
	if !strings.Contains(rec.Body.String(), `"available":true`) {
		t.Fatalf("fake yt-dlp should be available: %s", rec.Body.String())
	}
}

// The GET proxy runs the fake yt-dlp end to end.
func TestSearchYouTube_GET_WithFakeYtDlp(t *testing.T) {
	y, _ := fakeYtDlpSearcher(t, "ok")
	h := &Handler{YouTube: y}
	rec := httptest.NewRecorder()
	h.SearchYouTube(rec, ytReq(http.MethodGet, "/api/search/youtube?q=kahf", "", false))
	var block YTBlock
	if err := json.Unmarshal(rec.Body.Bytes(), &block); err != nil {
		t.Fatal(err)
	}
	if block.Reason != "" || len(block.Items) != 2 || block.Items[0].Match.State != "importable" {
		t.Fatalf("got %+v", block)
	}
}

func TestImportRequest_Validate(t *testing.T) {
	d := 10
	ok := importYouTubeRequest{YouTubeID: "AvctFaYGuR0", Title: " T ", Thumbnail: "https://i.ytimg.com/vi/x.jpg",
		PublishedAt: "2024-01-15", DurationSec: &d, Channel: "C", Description: "D"}
	if errs := ok.validate(); len(errs) != 0 {
		t.Fatalf("valid request rejected: %+v", errs)
	}
	m := ok.mapped()
	if m["title"] != "T" || m["poster_path"] != "https://i.ytimg.com/vi/x.jpg" || m["channel"] != "C" ||
		m["description"] != "D" || m["published_at"] != "2024-01-15" || m["duration_sec"] != 10 {
		t.Fatalf("mapped: %+v", m)
	}
	bad := []importYouTubeRequest{
		{YouTubeID: "short", Title: "t"},
		{YouTubeID: "AvctFaYGuR0"},
		{YouTubeID: "AvctFaYGuR0", Title: "t", Thumbnail: "https://evil.example/a.jpg"},
		{YouTubeID: "AvctFaYGuR0", Title: "t", Thumbnail: "http://i.ytimg.com/a.jpg"},
		{YouTubeID: "AvctFaYGuR0", Title: "t", PublishedAt: "yesterday"},
		{YouTubeID: "AvctFaYGuR0", Title: strings.Repeat("x", maxImportTitle+1)},
	}
	for i, b := range bad {
		if errs := b.validate(); len(errs) == 0 {
			t.Errorf("case %d: want a field error", i)
		}
	}
}

func TestImportYouTube_RejectsBadID(t *testing.T) {
	rec := httptest.NewRecorder()
	ytMount().ServeHTTP(rec, ytReq(http.MethodPost, "/api/videos/"+vidID+"/import-youtube",
		`{"youtube_id":"--exec","title":"x"}`, true))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestCleanQuery(t *testing.T) {
	if got := cleanQuery("  --a\tb\nc  "); got != "a b c" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("ب", 500)
	if got := []rune(cleanQuery(long)); len(got) != maxQueryRunes {
		t.Fatalf("len=%d", len(got))
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
