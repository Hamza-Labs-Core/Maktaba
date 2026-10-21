package guide

import (
	"context"
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

// The guide's filler rules, as dizqueTV's (rules tv.guide_* defaults):
// filler up to 120 s joins the programme before it, longer filler is a
// «فواصل» block, and programmes shorter than 60 s are not listed.
func TestCollapseFiller(t *testing.T) {
	gr := loadFillerRules(context.Background(), nil)
	if gr.MaxPadding != 120*time.Second || gr.FillerTitle != "فواصل" || gr.MinProgram != 60*time.Second {
		t.Fatalf("rules' defaults: %+v", gr)
	}
	t0 := tm("2026-06-14T20:00:00Z")
	at := func(s int) string { return t0.Add(time.Duration(s) * time.Second).Format(time.RFC3339) }
	blk := func(kind, title string, from, to int) Block {
		return Block{ChannelID: "c", Kind: kind, Title: title, Start: at(from), Stop: at(to)}
	}
	blocks := []Block{
		blk("program", "A", 0, 1800),
		blk("bumper", "ID", 1800, 1830), blk("filler", "Promo", 1830, 1920), // 120 s: folded into A
		blk("program", "B", 1920, 3600),
		blk("filler", "Promo", 3600, 3700), blk("slate", "", 3700, 3721), // 121 s: its own block
		blk("program", "C", 3721, 5400),
		blk("program", "Tiny", 5400, 5459), // 59 s: not listed, folded into C
		blk("program", "D", 5459, 7200),
		blk("program", "Minute", 7200, 7260), // 60 s: listed
	}
	// now is in the folded filler after A: A is live over its longer span
	out := collapseFiller(blocks, gr, "Kids", t0.Add(1860*time.Second))
	type row struct {
		kind, title string
		start, stop int
	}
	want := []row{{"program", "A", 0, 1920}, {"program", "B", 1920, 3600}, {"filler", "فواصل", 3600, 3721},
		{"program", "C", 3721, 5459}, {"program", "D", 5459, 7200}, {"program", "Minute", 7200, 7260}}
	if len(out) != len(want) {
		t.Fatalf("got %d blocks: %+v", len(out), out)
	}
	for i, w := range want {
		b := out[i]
		if b.Kind != w.kind || b.Title != w.title || b.Start != at(w.start) || b.Stop != at(w.stop) {
			t.Errorf("block %d = %s %q %s..%s; want %s %q %d-%d s", i, b.Kind, b.Title, b.Start, b.Stop,
				w.kind, w.title, w.start, w.stop)
		}
	}
	if !out[0].IsLive || out[0].Progress != 1860.0/1920 || out[1].IsLive {
		t.Errorf("liveness: %+v / %+v", out[0], out[1])
	}

	// A short lead-in joins the programme after it; a long one is its own
	// block; an empty filler title is the channel's name.
	lead := []Block{blk("filler", "", 0, 120), blk("program", "A", 120, 1800)}
	if got := collapseFiller(lead, gr, "Kids", t0); len(got) != 1 || got[0].Title != "A" || got[0].Start != at(0) {
		t.Errorf("short lead-in: %+v", got)
	}
	lead = []Block{blk("slate", "", 0, 600), blk("program", "A", 600, 1800)}
	got := collapseFiller(lead, FillerRules{MaxPadding: gr.MaxPadding, MinProgram: gr.MinProgram}, "Kids", t0)
	if len(got) != 2 || got[0].Title != "Kids" || got[0].Kind != "filler" || got[1].Title != "A" {
		t.Errorf("long lead-in: %+v", got)
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
	writeXMLTV(&sb, "https://maktaba.local", chans, rows)
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

func TestGuideNeverShowsAServerLogoPath(t *testing.T) {
	file, remote := "/srv/art/kids.png", "https://cdn.example/news.png"
	chans := []ChannelMeta{
		{ID: "uuid-1", Slug: "kids", Name: "Kids", LogoPath: &file},
		{ID: "uuid-2", Slug: "news", Name: "News", LogoPath: &remote},
	}
	var sb strings.Builder
	writeXMLTV(&sb, "https://maktaba.local", chans, nil)
	for name, out := range map[string]string{"xmltv": sb.String(), "m3u": buildM3U("https://maktaba.local", chans)} {
		if strings.Contains(out, "/srv/art") {
			t.Errorf("%s leaks the logo path:\n%s", name, out)
		}
		if !strings.Contains(out, "https://maktaba.local/api/channels/uuid-1/logo") || !strings.Contains(out, remote) {
			t.Errorf("%s logo refs wrong:\n%s", name, out)
		}
	}
	if h := toHeader(chans[0]); h.Logo != "" || h.LogoURL != "/api/channels/uuid-1/logo" {
		t.Errorf("header %+v", h)
	}
	if h := toHeader(chans[1]); h.Logo != remote {
		t.Errorf("header %+v", h)
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

// Story 27.11: the strip's line and its parts reach /api/channels/now.
func TestToBlock_CarriesTheStripLine(t *testing.T) {
	s := parseSnapshot([]byte(`{"title":"في الخنادق… لا يوجد ملحدون!","on_air":"من زاوية أخرى · الحلقة 45 — في الخنادق… لا يوجد ملحدون!",
		"strip_title":"في الخنادق… لا يوجد ملحدون!","strip_series":"من زاوية أخرى","strip_episode":"الحلقة 45","strip_ref":"البقرة ١٨٣",
		"source_title":"في الخنادق… لا يوجد ملحدون! ｜ برنامج من زاوية أخرى ｜ الحلقة 45"}`))
	now := time.Now()
	b := toBlock(ProgramRow{Kind: "program", StartAt: now, EndAt: now.Add(time.Hour), Snapshot: s}, now)
	if b.OnAir != "من زاوية أخرى · الحلقة 45 — في الخنادق… لا يوجد ملحدون!" || b.StripSeries != "من زاوية أخرى" ||
		b.StripEpisode != "الحلقة 45" || b.StripRef != "البقرة ١٨٣" || b.StripTitle != "في الخنادق… لا يوجد ملحدون!" {
		t.Fatalf("block %+v", b)
	}
	if s.SourceTitle == "" {
		t.Fatal("source_title not decoded")
	}
}
