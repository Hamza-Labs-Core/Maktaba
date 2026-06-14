package guide

import (
	"strings"
	"testing"
	"time"
)

func tm(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseSnapshot(t *testing.T) {
	s := parseSnapshot([]byte(`{"title":"Ep","season":2,"episode":5,"episode_title":"Pilot"}`))
	if s.Title != "Ep" || s.EpisodeTitle != "Pilot" {
		t.Fatalf("bad parse: %+v", s)
	}
	if !s.IsEpisodic() || *s.Season != 2 || *s.Episode != 5 {
		t.Fatalf("episodic fields wrong: %+v", s)
	}
	// Garbage / empty → zero value, no panic.
	if z := parseSnapshot(nil); z.Title != "" || z.IsEpisodic() {
		t.Fatalf("nil snapshot not zero: %+v", z)
	}
	if z := parseSnapshot([]byte("not json")); z.Title != "" {
		t.Fatalf("garbage snapshot not zero: %+v", z)
	}
}

func TestProgress(t *testing.T) {
	start := tm("2026-06-14T20:00:00Z")
	end := tm("2026-06-14T21:00:00Z")
	cases := []struct {
		now  string
		want float64
	}{
		{"2026-06-14T20:00:00Z", 0},
		{"2026-06-14T20:30:00Z", 0.5},
		{"2026-06-14T21:00:00Z", 1},
		{"2026-06-14T19:00:00Z", 0}, // before
		{"2026-06-14T22:00:00Z", 1}, // after, clamped
	}
	for _, c := range cases {
		if got := progress(start, end, tm(c.now)); got != c.want {
			t.Errorf("progress(now=%s) = %v want %v", c.now, got, c.want)
		}
	}
}

func TestToBlock_Liveness(t *testing.T) {
	pr := ProgramRow{
		ChannelID: "ch1",
		Kind:      "program",
		StartAt:   tm("2026-06-14T20:00:00Z"),
		EndAt:     tm("2026-06-14T21:00:00Z"),
		Snapshot:  Snapshot{Title: "Movie"},
	}
	live := toBlock(pr, tm("2026-06-14T20:15:00Z"))
	if !live.IsLive || live.Progress != 0.25 {
		t.Errorf("expected live with 0.25 progress, got %+v", live)
	}
	notLive := toBlock(pr, tm("2026-06-14T19:00:00Z"))
	if notLive.IsLive {
		t.Errorf("expected not live, got %+v", notLive)
	}
}

func TestCollapseFiller(t *testing.T) {
	blocks := []Block{
		{ChannelID: "c", Kind: "program", Title: "Movie", Start: "T0", Stop: "T1"},
		{ChannelID: "c", Kind: "bumper", Title: "ID", Start: "T1", Stop: "T1b"},
		{ChannelID: "c", Kind: "filler", Title: "Promo", Start: "T1b", Stop: "T2", IsLive: true, Progress: 0.3},
		{ChannelID: "c", Kind: "program", Title: "News", Start: "T2", Stop: "T3"},
	}
	out := collapseFiller(blocks)
	if len(out) != 3 {
		t.Fatalf("expected 3 blocks after collapse, got %d: %+v", len(out), out)
	}
	mid := out[1]
	if mid.Kind != "filler" || mid.Title != "Up Next" {
		t.Errorf("merged block wrong: %+v", mid)
	}
	if mid.Start != "T1" || mid.Stop != "T2" {
		t.Errorf("merged span wrong: %s..%s", mid.Start, mid.Stop)
	}
	if !mid.IsLive || mid.Progress != 0.3 {
		t.Errorf("merged block should carry liveness: %+v", mid)
	}
}

func TestXMLTVTime(t *testing.T) {
	got := xmltvTime(tm("2026-06-14T08:30:05Z"))
	if got != "20260614083005 +0000" {
		t.Errorf("xmltvTime = %q", got)
	}
}

func TestXMLEscape(t *testing.T) {
	got := xmlEscape(`Tom & Jerry <"x">`)
	want := "Tom &amp; Jerry &lt;&quot;x&quot;&gt;"
	if got != want {
		t.Errorf("xmlEscape = %q want %q", got, want)
	}
}

func season(n int) *int { return &n }

func TestWriteXMLTV_EpisodeNums(t *testing.T) {
	chans := []ChannelMeta{{ID: "id1", Slug: "kids", Name: "Kids", Number: 5}}
	rows := []ProgramRow{{
		ChannelID: "id1",
		Kind:      "program",
		StartAt:   tm("2026-06-14T20:00:00Z"),
		EndAt:     tm("2026-06-14T20:30:00Z"),
		Snapshot:  Snapshot{Title: "Show", Season: season(1), Episode: season(3)},
	}}
	var sb strings.Builder
	writeXMLTV(&sb, chans, rows)
	out := sb.String()
	if !strings.Contains(out, `<channel id="kids">`) {
		t.Error("missing channel element with slug id")
	}
	if !strings.Contains(out, `system="xmltv_ns">0.2.`) {
		t.Errorf("xmltv_ns episode-num wrong:\n%s", out)
	}
	if !strings.Contains(out, `system="onscreen">S1E3`) {
		t.Errorf("onscreen episode-num wrong:\n%s", out)
	}
}

func TestBuildM3U_TvgIDMatchesSlug(t *testing.T) {
	logo := "http://x/logo.png"
	chans := []ChannelMeta{
		{ID: "uuid-1", Slug: "kids", Name: "Kids", Number: 5, Category: "family", LogoPath: &logo},
		{ID: "uuid-2", Slug: "news", Name: "News 24", Number: 6, Category: "news"},
	}
	out := buildM3U("https://maktaba.local", chans)
	if !strings.HasPrefix(out, "#EXTM3U") {
		t.Error("missing #EXTM3U header")
	}
	if !strings.Contains(out, `url-tvg="https://maktaba.local/api/channels/xmltv"`) {
		t.Error("missing url-tvg pointing at xmltv")
	}
	// Every tvg-id must equal the slug, and the live URL must reference
	// the channel id (AC5/AC6).
	if !strings.Contains(out, `tvg-id="kids"`) || !strings.Contains(out, `tvg-chno="5"`) {
		t.Errorf("kids EXTINF wrong:\n%s", out)
	}
	if !strings.Contains(out, "https://maktaba.local/stream/channel/uuid-1/manifest.m3u8") {
		t.Errorf("kids live URL missing:\n%s", out)
	}
	if !strings.Contains(out, `tvg-id="news"`) {
		t.Errorf("news EXTINF wrong:\n%s", out)
	}
}
