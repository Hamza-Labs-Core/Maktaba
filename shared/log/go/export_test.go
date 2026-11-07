package log

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// T01 (story 53.16): a whole report, with titles, library paths, names, emails and
// addresses in every part, leaves the home with none of them.
func TestAReportLeavesTheHomeWithNoTitlePathNameEmailOrAddress(t *testing.T) {
	raw, err := os.ReadFile("testdata/report_export.json")
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	delete(m, "name") // the home drops the sender's name before the pass
	e := NewExport([]string{"/Volumes/Media/Lessons", "/srv/maktaba/library-2/"}, []string{"Fatima Al-Hassan", "Fatima"})
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e.Value(v)); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, gone := range []string{
		"Seerah", "Badr", "al-Kahf", "Tafsir", "/Volumes/Media", "/srv/maktaba", // titles and paths
		"Fatima", "Al-Hassan", "fatima@example.org", // names and emails
		"192.168.1", "10.0.0.5", "203.0.113.9", "fe80", // addresses, even one already masked at home
		"a1b2c3d4e5f60708", "ffeeddccbbaa9988", "abc123", // user hashes and a token
		`"title"`, `"video_title"`, `"file_name"`, `"series_title"`, `"episode_title"`,
	} {
		if strings.Contains(got, gone) {
			t.Errorf("%q left in the export:\n%s", gone, got)
		}
	}
	for _, kept := range []string{
		`"msg":"segment_missing"`, `"fp":"fp-segment"`, `"version":"0.3.95"`, "<library>/…", "[ip]", "[name]",
		`"user_h":"user-1"`, `"user_h":"user-2"`, "127.0.0.1", `"path":"/api/videos/123"`, "stopped at 12:40",
	} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q missing from the export:\n%s", kept, got)
		}
	}
	// one person is one stand-in throughout the report
	if strings.Count(got, `"user_h":"user-1"`) != 3 {
		t.Errorf("user-1 in each of the three lines of the same person:\n%s", got)
	}
}

func TestAPathIsOnlyMaskedUnderTheRootItself(t *testing.T) {
	e := NewExport([]string{"/media/videos"}, nil)
	if got := e.String("open /media/videos2/a.mp4, then /media/videos/b c.mp4; done"); got != "open /media/videos2/a.mp4, then <library>/…; done" {
		t.Fatalf("%q", got)
	}
}

func TestAShortNameIsNotCutOutOfWords(t *testing.T) {
	e := NewExport(nil, []string{"Al", "Omar"})
	if got := e.String("all good, OMAR said"); got != "all good, [name] said" {
		t.Fatalf("%q", got)
	}
}
