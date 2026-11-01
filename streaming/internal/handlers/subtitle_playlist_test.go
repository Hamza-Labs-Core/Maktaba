package handlers

import (
	"context"
	"errors"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files under testdata/")

// golden compares got with testdata/<name> (rewritten with -update).
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s differs\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// subsFixture is a transcode session whose on-disk master is the
// orchestrator's (BuildMasterPlaylist), served through a router with the
// subtitle routes.
type subsFixture struct {
	id     uuid.UUID
	router chi.Router
	master []byte
}

func newSubsFixture(t *testing.T, lister SubtitleLister) subsFixture {
	t.Helper()
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	id := uuid.New()
	dir := layout.HLSDir(id.String())
	_ = os.MkdirAll(dir, 0o755)
	master := BuildMasterPlaylist(ffmpeg.DefaultLadder(0))
	if err := os.WriteFile(filepath.Join(dir, "master.m3u8"), master, 0o644); err != nil {
		t.Fatal(err)
	}
	store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
		id: {ID: id, Format: session.FormatHLS, Mode: session.ModeTranscode},
	}}
	h := &ManifestHandler{Sessions: store, Layout: layout, Subtitles: lister}
	r := chi.NewRouter()
	r.Route("/stream/{session_id}", func(sub chi.Router) {
		sub.Use(injectSubject(id.String()))
		sub.Get("/manifest.m3u8", h.ServeMaster)
		sub.Get("/{rendition}/index.m3u8", h.ServeRenditionIndex)
		sub.Get("/subs-{lang}/index.m3u8", h.ServeSubtitlePlaylist)
	})
	return subsFixture{id: id, router: r, master: master}
}

func (f subsFixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+f.id.String()+path, nil))
	return rec.Code, rec.Body.String()
}

func listerOf(set SubtitleSet, err error) SubtitleLister {
	return func(context.Context) (SubtitleSet, error) { return set, err }
}

// AC 3: a session without subtitles is served the master as it was before
// renditions existed: byte for byte what is on disk, whether no lister is
// wired, the lister finds none, fails, or the video's length is unknown.
func TestSubtitleRenditions_MasterWithoutSubtitlesUnchanged(t *testing.T) {
	cases := map[string]SubtitleLister{
		"no lister":      nil,
		"no sidecar":     listerOf(SubtitleSet{DurationSec: 3600}, nil),
		"lister fails":   listerOf(SubtitleSet{}, errors.New("session not found")),
		"length unknown": listerOf(SubtitleSet{Langs: []string{"en"}}, nil),
		"bad language":   listerOf(SubtitleSet{DurationSec: 3600, Langs: []string{"../x"}}, nil),
	}
	for name, lister := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSubsFixture(t, lister)
			code, body := f.get(t, "/manifest.m3u8")
			if code != 200 {
				t.Fatalf("status %d %s", code, body)
			}
			if body != string(f.master) {
				t.Fatalf("master changed:\n%s", body)
			}
			golden(t, "master_no_subtitles.m3u8", []byte(body))
		})
	}
}

// AC 1: a session with subtitles lists each as an EXT-X-MEDIA TYPE=SUBTITLES
// rendition (language, name, DEFAULT=NO) whose URI is its one-segment
// playlist, every variant names the group, and every URI carries the
// session's sig.
func TestSubtitleRenditions_MasterListsEachSubtitle(t *testing.T) {
	f := newSubsFixture(t, listerOf(SubtitleSet{DurationSec: 5400.25, Langs: []string{"ar", "en"}}, nil))
	code, body := f.get(t, "/manifest.m3u8?sig=tok.one")
	if code != 200 {
		t.Fatalf("status %d %s", code, body)
	}
	golden(t, "master_subtitles_signed.m3u8", []byte(body))
	for _, want := range []string{
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="العربية",LANGUAGE="ar",DEFAULT=NO,AUTOSELECT=YES,URI="subs-ar/index.m3u8?sig=tok.one"`,
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,URI="subs-en/index.m3u8?sig=tok.one"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in\n%s", want, body)
		}
	}
	if n := strings.Count(body, `#EXT-X-STREAM-INF:`); n != 3 || strings.Count(body, `,SUBTITLES="subs"`) != n {
		t.Errorf("each variant names the group:\n%s", body)
	}
	// the renditions come before the first variant
	if strings.Index(body, "#EXT-X-MEDIA:") > strings.Index(body, "#EXT-X-STREAM-INF:") {
		t.Errorf("EXT-X-MEDIA after a variant:\n%s", body)
	}
}

// AC 1: each rendition's playlist is one WebVTT segment, the video's length,
// over the existing sidecar route (asked for as an HLS segment: ?hls=1),
// signed like the rest.
func TestSubtitleRenditions_OneSegmentPlaylist(t *testing.T) {
	f := newSubsFixture(t, listerOf(SubtitleSet{DurationSec: 5400.25, Langs: []string{"ar", "en"}}, nil))
	code, body := f.get(t, "/subs-en/index.m3u8?sig=tok.one")
	if code != 200 {
		t.Fatalf("status %d %s", code, body)
	}
	golden(t, "subtitle_en_signed.m3u8", []byte(body))
	if strings.Count(body, "#EXTINF:") != 1 || !strings.Contains(body, "#EXTINF:5400.250,\n../subs/sidecar/en.vtt?hls=1&sig=tok.one\n") {
		t.Errorf("one signed segment at the sidecar route:\n%s", body)
	}
	if !strings.Contains(body, "#EXT-X-TARGETDURATION:5401\n") || !strings.HasSuffix(body, "#EXT-X-ENDLIST\n") {
		t.Errorf("a whole VOD playlist:\n%s", body)
	}
	// the segment URI, resolved against the playlist's URL, is the sidecar route
	base, _ := httptest.NewRequest("GET", "/stream/"+f.id.String()+"/subs-en/index.m3u8", nil).URL.Parse("../subs/sidecar/en.vtt")
	if base.Path != "/stream/"+f.id.String()+"/subs/sidecar/en.vtt" {
		t.Errorf("resolves to %s", base.Path)
	}
}

// A language the session has no sidecar in, or none at all, has no playlist.
func TestSubtitleRenditions_UnknownLanguage404(t *testing.T) {
	f := newSubsFixture(t, listerOf(SubtitleSet{DurationSec: 60, Langs: []string{"en"}}, nil))
	for _, path := range []string{"/subs-fr/index.m3u8", "/subs-en.x/index.m3u8"} {
		if code, _ := f.get(t, path); code != 404 {
			t.Errorf("%s: %d", path, code)
		}
	}
	none := newSubsFixture(t, nil)
	if code, _ := none.get(t, "/subs-en/index.m3u8"); code != 404 {
		t.Errorf("no lister: %d", code)
	}
	// the transcode's own renditions still route to their playlists
	if code, _ := f.get(t, "/v0/index.m3u8"); code != 404 { // not written in this fixture
		t.Errorf("v0: %d", code)
	}
}

// A CRLF master keeps its line endings; a variant already naming a group
// keeps it.
func TestWithSubtitleRenditions_LineEndingsAndExisting(t *testing.T) {
	in := "#EXTM3U\r\n#EXT-X-STREAM-INF:BANDWIDTH=1\r\nv0/index.m3u8\r\n#EXT-X-STREAM-INF:BANDWIDTH=2,SUBTITLES=\"x\"\r\nv1/index.m3u8\r\n"
	got := string(WithSubtitleRenditions([]byte(in), []string{"en", "eng", "xx"}))
	want := "#EXTM3U\r\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,URI="subs-en/index.m3u8"` + "\r\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English (eng)",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,URI="subs-eng/index.m3u8"` + "\r\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="xx",DEFAULT=NO,AUTOSELECT=YES,URI="subs-xx/index.m3u8"` + "\r\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1,SUBTITLES=\"subs\"\r\nv0/index.m3u8\r\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=2,SUBTITLES=\"x\"\r\nv1/index.m3u8\r\n"
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	if out := WithSubtitleRenditions([]byte("#EXTM3U\nv0/index.m3u8\n"), []string{"en"}); string(out) != "#EXTM3U\nv0/index.m3u8\n" {
		t.Errorf("a master with no variant: %q", out)
	}
}

// ListSidecars lists what ReadSidecar serves: movie.<lang>.vtt/.srt and a
// bare language tag's file, each language once, and nothing else.
func TestListSidecars(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"Movie.mkv", "Movie.en.srt", "Movie.en.vtt", "Movie.ar.vtt", "fr.srt",
		"Movie.en.1.srt",            // a dot in the language: not a path the renditions carry
		"notes.srt", "Other.de.srt", // not this video's
		"Movie.es.ass", "Movie.nfo", // not a format served
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Mkdir(filepath.Join(dir, "Movie.it.vtt"), 0o755) // a folder
	got := ListSidecars(filepath.Join(dir, "Movie.mkv"), os.ReadDir)
	if want := []string{"ar", "en", "fr"}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// each listed language is one ReadSidecar serves
	for _, lang := range got {
		if _, err := ReadSidecar(filepath.Join(dir, "Movie.mkv"), lang, osOpener{}); err != nil {
			t.Errorf("%s: %v", lang, err)
		}
	}
	if got := ListSidecars("/no/such/dir/x.mkv", os.ReadDir); got != nil {
		t.Errorf("an unreadable folder: %v", got)
	}
}

type osOpener struct{}

func (osOpener) Open(path string) (FileReader, error) { return os.Open(path) }

// The paths a rendition adds stay inside the web port's allowed /stream/
// paths. These are cmd/maktaba-server/stream_gate.go's streamPathRes for a
// video session, copied: if the gate changes, this test names what the
// renditions need from it.
func TestSubtitleRenditions_PathsPassTheWebPortGate(t *testing.T) {
	const uuidRe = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`
	gate := []*regexp.Regexp{
		regexp.MustCompile(`^/stream/` + uuidRe + `/(manifest\.m3u8|manifest\.mpd|chapters\.json|subs/auto\.vtt|subs/sidecar/[A-Za-z0-9_-]{1,32}\.vtt)$`),
		regexp.MustCompile(`^/stream/` + uuidRe + `/[A-Za-z0-9_-]{1,32}/(index\.m3u8|[A-Za-z0-9_-]{1,64}\.(ts|m4s|mp4|aac|vtt))$`),
	}
	allowed := func(p string) bool {
		return slices.ContainsFunc(gate, func(re *regexp.Regexp) bool { return re.MatchString(p) })
	}
	id := uuid.New().String()
	longest := strings.Repeat("a", 27)
	for _, lang := range []string{"en", "pt-BR", "zh_Hans", longest} {
		if !subtitleLang.MatchString(lang) {
			t.Fatalf("%s not a rendition language", lang)
		}
		for _, p := range []string{
			"/stream/" + id + "/" + subtitleDirPrefix + lang + "/index.m3u8",
			"/stream/" + id + "/subs/sidecar/" + lang + ".vtt",
		} {
			if !allowed(p) {
				t.Errorf("the gate refuses %s", p)
			}
		}
	}
	if subtitleLang.MatchString(longest + "a") {
		t.Errorf("a language longer than the gate's folder name is taken")
	}
}

// The web's hls.js test (web/src/pages/hlsSubtitleRenditions.test.ts) parses
// copies of the golden masters with the real hls.js: it carries each one
// verbatim, so a change here is checked there too.
func TestSubtitleRenditions_WebTestCarriesTheGoldens(t *testing.T) {
	web, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "pages", "hlsSubtitleRenditions.test.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"master_subtitles_signed.m3u8", "master_no_subtitles.m3u8"} {
		g, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(web), "// "+name+"\nconst ") || !strings.Contains(string(web), "= `"+string(g)+"`;") {
			t.Errorf("the web's hls.js test does not carry testdata/%s as it is: copy it there", name)
		}
	}
}
