package hdhr

import (
	"context"
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

// artGuide is a GuideSource with one channel's logo, plan art folders and the
// programmes of a schedule (on air: any video a "program" block of the
// channel plays in the window).
type artGuide struct {
	fakeGuide
	logo      string
	planRoots []string
	art       map[uuid.UUID]ProgrammeArt // by video
}

func (a *artGuide) ChannelLogo(context.Context, uuid.UUID) (string, error) { return a.logo, nil }
func (a *artGuide) PlanArtRoots(context.Context) ([]string, error)         { return a.planRoots, nil }
func (a *artGuide) ProgrammeArt(_ context.Context, ch, vid uuid.UUID, start, end time.Time) (ProgrammeArt, error) {
	pa := a.art[vid]
	pa.OnAir = false
	for _, p := range a.progs {
		if p.ChannelID == ch && p.VideoID == vid && p.Kind == "program" && p.StartAt.Before(end) && p.EndAt.After(start) {
			pa.OnAir = true
		}
	}
	return pa, nil
}

func writeFile(t *testing.T, p, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func get(t *testing.T, h *GuideHandler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestLogo_ServedFromArtFoldersOnly(t *testing.T) {
	data := t.TempDir()
	plan := t.TempDir()
	outside := t.TempDir()
	upload := writeFile(t, filepath.Join(data, ArtDirName, "c1", "logo.png"), "\x89PNG-upload")
	planLogo := writeFile(t, filepath.Join(plan, "kids.jpg"), "jpeg-plan")
	stray := writeFile(t, filepath.Join(outside, "logo.png"), "\x89PNG-stray")
	txt := writeFile(t, filepath.Join(data, ArtDirName, "secret.txt"), "secret")
	link := filepath.Join(data, ArtDirName, "escape.png")
	if err := os.Symlink(stray, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		logo string
		code int
		body string
	}{
		{upload, 200, "\x89PNG-upload"},
		{planLogo, 200, "jpeg-plan"},
		{stray, 404, ""}, // not in an art folder
		{txt, 404, ""},   // not an image
		{link, 404, ""},  // a symlink out of the art folder
		{filepath.Join(data, ArtDirName, "..", "..", strings.TrimPrefix(stray, "/")), 404, ""},
		{`\\nas\share\logo.png`, 404, ""},      // UNC
		{"https://cdn.example/l.png", 302, ""}, // a URL logo: redirect
		{"javascript://x/%0aalert(1)", 404, ""},
		{"file:///etc/passwd", 404, ""},
		{"", 404, ""},
	}
	for _, c := range cases {
		h := &GuideHandler{Repo: &artGuide{logo: c.logo, planRoots: []string{plan}}, DataDir: data}
		rec := get(t, h, "/channel/"+uuid.NewString()+"/logo")
		if rec.Code != c.code {
			t.Errorf("logo %q → %d, want %d", c.logo, rec.Code, c.code)
			continue
		}
		if c.code == 200 {
			if rec.Body.String() != c.body {
				t.Errorf("logo %q body %q", c.logo, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
				t.Errorf("logo %q content type %q", c.logo, ct)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
				t.Errorf("cache-control %q", cc)
			}
		}
	}
}

func TestLogo_MaxAgeFollowsRule(t *testing.T) {
	data := t.TempDir()
	logo := writeFile(t, filepath.Join(data, ArtDirName, "l.png"), "png")
	h := &GuideHandler{Repo: &artGuide{logo: logo}, DataDir: data,
		MaxAgeFunc: func(context.Context) time.Duration { return 10 * time.Minute }}
	rec := get(t, h, "/channel/"+uuid.NewString()+"/logo")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=600" {
		t.Errorf("%d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

// programmeFixture: a channel with an episode of a series, a film with its own
// thumbnail, a video with no image, filler, and a video only on air outside the
// guide's window.
type programmeFixture struct {
	h                               *GuideHandler
	g                               *artGuide
	ch, episode, film, bare, offAir uuid.UUID
	seriesPoster, filmThumb, logo   string
	t0                              time.Time
}

func newProgrammeFixture(t *testing.T) programmeFixture {
	t.Helper()
	data := t.TempDir()
	thumbs := t.TempDir()
	f := programmeFixture{ch: uuid.New(), episode: uuid.New(), film: uuid.New(), bare: uuid.New(), offAir: uuid.New(),
		t0: time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)}
	f.logo = writeFile(t, filepath.Join(data, ArtDirName, "logo.png"), "LOGO")
	f.seriesPoster = writeFile(t, filepath.Join(thumbs, "hash-ep1", "poster.jpg"), "SERIES")
	writeFile(t, filepath.Join(thumbs, "hash-ep5", "poster.jpg"), "EPISODE-5")
	f.filmThumb = writeFile(t, filepath.Join(thumbs, "hash-film", "poster.webp"), "FILM")
	at := func(m int) time.Time { return f.t0.Add(time.Duration(m) * time.Minute) }
	s, e := 1, 5
	f.g = &artGuide{logo: f.logo, art: map[uuid.UUID]ProgrammeArt{
		// an episode: the series card (its first episode's poster) is preferred
		f.episode: {SeriesCard: ArtVideo{ID: uuid.NewString(), ContentHash: "hash-ep1"},
			Video: ArtVideo{ID: f.episode.String(), ContentHash: "hash-ep5"}},
		// a film: its own thumbnail; a stray poster_path is never read
		f.film: {Video: ArtVideo{ID: f.film.String(), ContentHash: "hash-film", PosterPath: "/etc/passwd.jpg"}},
		f.bare: {Video: ArtVideo{ID: f.bare.String(), ContentHash: "no-such-hash"}},
	}}
	f.g.chans = []GuideChannel{{ID: f.ch, Number: 4, Name: "Kids", LogoPath: f.logo, Category: "Cartoons"}}
	f.g.progs = []GuideProgram{
		{ChannelID: f.ch, Kind: "program", StartAt: at(0), EndAt: at(30), VideoID: f.episode, Snapshot: snap(map[string]any{
			"title": "Show A", "episode_title": "Pilot", "season": s, "episode": e, "strip_series": "Show A Series",
			"genre": "Animation", "poster": "http://plex.lan:32400/library/metadata/1/thumb?X-Plex-Token=SECRET"})},
		{ChannelID: f.ch, Kind: "filler", StartAt: at(30), EndAt: at(40), VideoID: uuid.New()},
		{ChannelID: f.ch, Kind: "program", StartAt: at(40), EndAt: at(130), VideoID: f.film, Snapshot: snap(map[string]any{
			"title": "A Film"})},
		{ChannelID: f.ch, Kind: "program", StartAt: at(130), EndAt: at(160), VideoID: f.bare, Snapshot: snap(map[string]any{
			"title": "Bare"})},
		{ChannelID: f.ch, Kind: "live", StartAt: at(160), EndAt: at(220), Snapshot: snap(map[string]any{"title": "News"})},
		{ChannelID: f.ch, Kind: "program", StartAt: at(60 * 24 * 30), EndAt: at(60*24*30 + 30), VideoID: f.offAir},
	}
	f.h = &GuideHandler{Repo: f.g, DataDir: data, ThumbnailRoot: thumbs, PublicBase: "http://tv.lan:8081",
		Now: func() time.Time { return f.t0.Add(10 * time.Minute) }}
	return f
}

type xmltvProgDoc struct {
	Programmes []struct {
		Title    string   `xml:"title"`
		Desc     string   `xml:"desc"`
		Category []string `xml:"category"`
		Icon     struct {
			Src string `xml:"src,attr"`
		} `xml:"icon"`
		Previously *struct{} `xml:"previously-shown"`
	} `xml:"programme"`
}

func TestXMLTV_ProgrammeImagesDescAndPreviouslyShown(t *testing.T) {
	f := newProgrammeFixture(t)
	rec := get(t, f.h, "/xmltv.xml")
	if rec.Code != 200 {
		t.Fatalf("xmltv %d", rec.Code)
	}
	body := rec.Body.String()
	var doc xmltvProgDoc
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	art := func(v uuid.UUID) string {
		return "http://tv.lan:8081/tv/art/channel/" + f.ch.String() + "/video/" + v.String()
	}
	logo := "http://tv.lan:8081/channel/" + f.ch.String() + "/logo"
	type want struct {
		title, icon, desc string
		prev              bool
	}
	wants := []want{
		{"Show A", art(f.episode), "Show A Series", true},
		{"Kids", logo, "Kids", true}, // filler: the channel's logo, its name
		{"A Film", art(f.film), "Kids", true},
		{"Bare", art(f.bare), "Kids", true},
		{"News", logo, "Kids", false}, // live: no video, not previously shown
	}
	if len(doc.Programmes) != len(wants) {
		t.Fatalf("got %d programmes:\n%s", len(doc.Programmes), body)
	}
	for i, w := range wants {
		p := doc.Programmes[i]
		if p.Title != w.title || p.Icon.Src != w.icon || p.Desc != w.desc || (p.Previously != nil) != w.prev {
			t.Errorf("programme %d = %q icon=%q desc=%q prev=%v; want %+v", i, p.Title, p.Icon.Src, p.Desc,
				p.Previously != nil, w)
		}
	}
	if c := doc.Programmes[0].Category; len(c) != 2 || c[0] != "Animation" || c[1] != "Cartoons" {
		t.Errorf("categories %v", c)
	}
	// no token or secret: not the snapshot's Plex poster, nothing token-like
	for _, bad := range []string{"SECRET", "Plex-Token", "token", "32400", "/etc/", f.logo} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(bad)) {
			t.Errorf("guide contains %q", bad)
		}
	}
}

func TestProgrammeImage_FallbackOrder(t *testing.T) {
	f := newProgrammeFixture(t)
	path := func(v uuid.UUID) string { return "/tv/art/channel/" + f.ch.String() + "/video/" + v.String() }
	for _, c := range []struct {
		name string
		v    uuid.UUID
		code int
		body string
	}{
		{"episode → series poster", f.episode, 200, "SERIES"},
		{"film → its thumbnail", f.film, 200, "FILM"},
		{"no thumbnail → channel logo", f.bare, 200, "LOGO"},
		{"not on air in the window → 404", f.offAir, 404, ""},
		{"not scheduled at all → 404", uuid.New(), 404, ""},
	} {
		rec := get(t, f.h, path(c.v))
		if rec.Code != c.code || (c.code == 200 && rec.Body.String() != c.body) {
			t.Errorf("%s: %d %q", c.name, rec.Code, rec.Body.String())
			continue
		}
		if c.code == 200 {
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
				t.Errorf("%s: content type %q", c.name, ct)
			}
			if rec.Header().Get("Cache-Control") != "public, max-age=86400" {
				t.Errorf("%s: cache-control %q", c.name, rec.Header().Get("Cache-Control"))
			}
		}
	}
	// the series' own poster beats its first episode's
	own := writeFile(t, filepath.Join(f.h.ThumbnailRoot, "series", "own.png"), "OWN")
	a := f.g.art[f.episode]
	a.SeriesPoster = own
	f.g.art[f.episode] = a
	if rec := get(t, f.h, path(f.episode)); rec.Body.String() != "OWN" {
		t.Errorf("series poster: %d %q", rec.Code, rec.Body.String())
	}
	// another channel's id: the video is not on its air
	if rec := get(t, f.h, "/tv/art/channel/"+uuid.NewString()+"/video/"+f.film.String()); rec.Code != 404 {
		t.Errorf("other channel: %d", rec.Code)
	}
	if rec := get(t, f.h, "/tv/art/channel/x/video/../../etc"); rec.Code != 404 {
		t.Errorf("bad ids: %d", rec.Code)
	}
}

// fakeFetcher stands for the node's cache of the home's files.
type fakeFetcher struct {
	files map[string]string // kind → local file
	asked []string
}

func (f *fakeFetcher) Asset(_ context.Context, _ uuid.UUID, kind, _ string) string {
	f.asked = append(f.asked, kind)
	return f.files[kind]
}

func TestNode_ImagesComeFromTheHome(t *testing.T) {
	f := newProgrammeFixture(t)
	cache := t.TempDir()
	fetched := &fakeFetcher{files: map[string]string{
		AssetGuideLogo:                          writeFile(t, filepath.Join(cache, "aaa.png"), "HOME-LOGO"),
		AssetGuideVideo + "/" + f.film.String(): writeFile(t, filepath.Join(cache, "bbb.webp"), "HOME-FILM"),
	}}
	// a node: no data dir, no thumbnails; everything comes from the home
	node := &GuideHandler{Repo: f.g, Remote: fetched, Now: f.h.Now}
	if rec := get(t, node, "/channel/"+f.ch.String()+"/logo"); rec.Code != 200 || rec.Body.String() != "HOME-LOGO" {
		t.Errorf("logo via node: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(t, node, "/tv/art/channel/"+f.ch.String()+"/video/"+f.film.String()); rec.Code != 200 ||
		rec.Body.String() != "HOME-FILM" || rec.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("programme via node: %d %q", rec.Code, rec.Body.String())
	}
	// the home has none: the logo
	if rec := get(t, node, "/tv/art/channel/"+f.ch.String()+"/video/"+f.bare.String()); rec.Body.String() != "HOME-LOGO" {
		t.Errorf("fallback via node: %d %q", rec.Code, rec.Body.String())
	}
	// not on air: 404 without asking the home
	n := len(fetched.asked)
	if rec := get(t, node, "/tv/art/channel/"+f.ch.String()+"/video/"+f.offAir.String()); rec.Code != 404 || len(fetched.asked) != n {
		t.Errorf("off air via node: %d, asked %v", rec.Code, fetched.asked[n:])
	}
}

func TestHomeFiles_ForTheSignedRoute(t *testing.T) {
	f := newProgrammeFixture(t)
	if p := f.h.LogoFile(context.Background(), f.ch); p == "" {
		t.Error("logo file")
	}
	if p := f.h.ProgrammeFile(context.Background(), f.ch, f.film); p != f.filmThumb {
		t.Errorf("programme file %q", p)
	}
	if p := f.h.ProgrammeFile(context.Background(), f.ch, f.offAir); p != "" {
		t.Errorf("off-air programme file %q", p)
	}
	if p := f.h.ProgrammeFile(context.Background(), f.ch, f.bare); p != "" {
		t.Errorf("no image of its own: %q", p)
	}
}

func TestPlanArtFolders(t *testing.T) {
	got := PlanArtFolders([]byte(`{"config":{"dz_logo_dir":"/art/logos","dz_watermark_dir":" ","dz_watermark_file":"/art/wm/w.png"}}`))
	if strings.Join(got, ",") != "/art/logos,/art/wm" {
		t.Errorf("%v", got)
	}
	if PlanArtFolders([]byte("nope")) != nil {
		t.Error("bad json")
	}
}

func TestM3U_LogosOnThisService(t *testing.T) {
	f := newProgrammeFixture(t)
	body := get(t, f.h, "/lineup.m3u").Body.String()
	if !strings.Contains(body, `tvg-logo="http://tv.lan:8081/channel/`+f.ch.String()+`/logo"`) || strings.Contains(body, "/api/") {
		t.Errorf("m3u: %s", body)
	}
}
