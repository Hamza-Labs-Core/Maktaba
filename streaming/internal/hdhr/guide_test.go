package hdhr

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type fakeGuide struct {
	chans []GuideChannel
	progs []GuideProgram
}

func (f *fakeGuide) GuideChannels(context.Context) ([]GuideChannel, error) { return f.chans, nil }
func (f *fakeGuide) GuidePrograms(_ context.Context, start, end time.Time) ([]GuideProgram, error) {
	var out []GuideProgram
	for _, p := range f.progs {
		if p.StartAt.Before(end) && p.EndAt.After(start) {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakeGuide) ChannelLogo(context.Context, uuid.UUID) (string, error) { return "", nil }

func snap(v map[string]any) []byte { b, _ := json.Marshal(v); return b }

type xmltvDoc struct {
	Channels []struct {
		ID    string   `xml:"id,attr"`
		Names []string `xml:"display-name"`
		Icon  struct {
			Src string `xml:"src,attr"`
		} `xml:"icon"`
	} `xml:"channel"`
	Programmes []struct {
		Start   string `xml:"start,attr"`
		Stop    string `xml:"stop,attr"`
		Channel string `xml:"channel,attr"`
		Title   string `xml:"title"`
		Sub     string `xml:"sub-title"`
		Eps     []struct {
			System string `xml:"system,attr"`
			Value  string `xml:",chardata"`
		} `xml:"episode-num"`
	} `xml:"programme"`
}

func guideFixture() (*fakeGuide, uuid.UUID, time.Time) {
	t0 := time.Date(2026, 6, 14, 20, 0, 0, 0, time.UTC)
	kids := uuid.New()
	s, e := 2, 5
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	return &fakeGuide{
		chans: []GuideChannel{{ID: kids, Number: 7, Name: "Kids & Co", Slug: "kids", LogoPath: "Y:/logos/kids.png", Category: "kids"}},
		progs: []GuideProgram{
			{ChannelID: kids, Kind: "filler", StartAt: at(0), EndAt: at(2)},
			{ChannelID: kids, Kind: "program", StartAt: at(2), EndAt: at(30), Snapshot: snap(map[string]any{
				"title": "Show A", "episode_title": "Pilot", "season": s, "episode": e})},
			{ChannelID: kids, Kind: "bumper", StartAt: at(30), EndAt: at(31)},
			{ChannelID: kids, Kind: "filler", StartAt: at(31), EndAt: at(32)},
			{ChannelID: kids, Kind: "program", StartAt: at(32), EndAt: at(60), Snapshot: snap(map[string]any{"title": "Show B"})},
			{ChannelID: kids, Kind: "program", StartAt: at(60), EndAt: at(90), Snapshot: snap(map[string]any{"title": "Show B"})},
			// gap 90→100, then a slate run with no programme before it
			{ChannelID: kids, Kind: "slate", StartAt: at(100), EndAt: at(110)},
			{ChannelID: kids, Kind: "live", StartAt: at(110), EndAt: at(170)},
			{ChannelID: uuid.New(), Kind: "program", StartAt: at(0), EndAt: at(10)}, // disabled/unknown channel
		},
	}, kids, t0
}

func TestXMLTV_ShapeAndCollapse(t *testing.T) {
	g, kids, t0 := guideFixture()
	var b strings.Builder
	WriteXMLTV(&b, "http://web:8780", g.chans, g.progs, testRules)
	var doc xmltvDoc
	if err := xml.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, b.String())
	}
	if len(doc.Channels) != 1 || doc.Channels[0].ID != "7" || doc.Channels[0].Names[0] != "Kids & Co" || doc.Channels[0].Names[1] != "7" {
		t.Fatalf("channel header: %+v", doc.Channels)
	}
	if want := "http://web:8780/api/channels/" + kids.String() + "/logo"; doc.Channels[0].Icon.Src != want {
		t.Errorf("icon = %q, want %q", doc.Channels[0].Icon.Src, want)
	}
	type row struct{ start, stop, title string }
	ts := func(m int) string { return t0.Add(time.Duration(m) * time.Minute).Format("20060102150405 -0700") }
	want := []row{
		{ts(0), ts(32), "Show A"},   // filler lead-in absorbed; bumper+filler after folded in
		{ts(32), ts(90), "Show B"},  // split programme merged
		{ts(100), ts(110), "فواصل"}, // a 10-minute slate run: its own filler entry
		{ts(110), ts(170), "Live"},
	}
	if len(doc.Programmes) != len(want) {
		t.Fatalf("got %d programmes: %+v", len(doc.Programmes), doc.Programmes)
	}
	for i, w := range want {
		p := doc.Programmes[i]
		if p.Start != w.start || p.Stop != w.stop || p.Title != w.title || p.Channel != "7" {
			t.Errorf("programme %d = %s-%s %q ch=%s; want %s-%s %q", i, p.Start, p.Stop, p.Title, p.Channel, w.start, w.stop, w.title)
		}
	}
	p0 := doc.Programmes[0]
	if p0.Sub != "Pilot" || len(p0.Eps) != 2 || p0.Eps[0].Value != "1.4." || p0.Eps[1].Value != "S02E05" {
		t.Errorf("episode info: %+v", p0)
	}
}

func TestGuideHandler_RoutesAndBase(t *testing.T) {
	g, kids, t0 := guideFixture()
	h := &GuideHandler{Repo: g, Now: func() time.Time { return t0.Add(time.Hour) }}
	r := chi.NewRouter()
	h.Mount(r)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/xmltv.xml", nil)
	req.Host = "192.168.1.5:8781"
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `<icon src="http://192.168.1.5:8781/api/channels/`) {
		t.Errorf("xmltv via request host: %d %s", rec.Code, rec.Body.String())
	}

	h.PublicBase = "http://tv.lan:8781/"
	h.WebBase = "http://tv.lan:8780"
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/lineup.m3u", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`#EXTM3U url-tvg="http://tv.lan:8781/xmltv.xml"`,
		`tvg-id="7" tvg-chno="7" tvg-name="Kids & Co"`,
		`tvg-logo="http://tv.lan:8780/api/channels/` + kids.String() + `/logo"`,
		"http://tv.lan:8781/stream/channel/" + kids.String() + "/index.m3u8",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("m3u missing %q:\n%s", want, body)
		}
	}
}

func TestLineup_UsesPublicBase(t *testing.T) {
	repo := &fakeRepo{dev: enabledDevice(), channels: sampleChannels()}
	h := New(repo, &fakeStreamer{})
	h.PublicBase = "http://10.0.0.9:8781/"
	rec := httptest.NewRecorder()
	mux(h).ServeHTTP(rec, httptest.NewRequest("GET", "/lineup.json", nil))
	if !strings.Contains(rec.Body.String(), `"URL":"http://10.0.0.9:8781/auto/v5"`) {
		t.Errorf("lineup: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux(h).ServeHTTP(rec, httptest.NewRequest("GET", "/discover.json", nil))
	if !strings.Contains(rec.Body.String(), `"LineupURL":"http://10.0.0.9:8781/lineup.json"`) {
		t.Errorf("discover: %s", rec.Body.String())
	}
}

func TestXMLEscape_DropsIllegalControlChars(t *testing.T) {
	got := xmlEscape("A\x00B\x01C\x0bD\tE\nF&<\"\uFFFEG")
	if got != "ABCD\tE\nF&amp;&lt;&quot;G" {
		t.Errorf("xmlEscape = %q", got)
	}
	var b strings.Builder
	WriteXMLTV(&b, "http://h", []GuideChannel{{ID: uuid.New(), Number: 1, Name: "Bad\x07Name"}}, nil, GuideRules{})
	var doc xmltvDoc
	if err := xml.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("control char broke the XML: %v", err)
	}
}

func TestBaseURL_IgnoresForwardedHeadersUnlessTrusted(t *testing.T) {
	req := httptest.NewRequest("GET", "/lineup.json", nil)
	req.Host = "10.0.0.5:8081"
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	t.Setenv("MAKTABA_TRUST_PROXY_HEADERS", "")
	if got := baseURL(req); got != "http://10.0.0.5:8081" {
		t.Errorf("untrusted forwarded headers used: %s", got)
	}
	t.Setenv("MAKTABA_TRUST_PROXY_HEADERS", "1")
	if got := baseURL(req); got != "https://evil.example" {
		t.Errorf("trusted proxy headers ignored: %s", got)
	}
}

type logoGuide struct {
	fakeGuide
	logo string
}

func (l *logoGuide) ChannelLogo(context.Context, uuid.UUID) (string, error) { return l.logo, nil }

func TestLogo_OnlyImagesAndHTTPRedirects(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "logo.png")
	txt := filepath.Join(dir, "secret.txt")
	_ = os.WriteFile(png, []byte("\x89PNG"), 0o644)
	_ = os.WriteFile(txt, []byte("secret"), 0o644)
	cases := map[string]int{
		png:                          200,
		txt:                          404,
		"https://cdn.example/l.png":  302,
		"javascript://x/%0aalert(1)": 404,
		"file:///etc/passwd":         404,
	}
	for logo, want := range cases {
		g := &GuideHandler{Repo: &logoGuide{logo: logo}}
		r := chi.NewRouter()
		g.Mount(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/channel/"+uuid.NewString()+"/logo", nil))
		if rec.Code != want {
			t.Errorf("logo %q → %d, want %d", logo, rec.Code, want)
		}
	}
}
