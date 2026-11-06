package plugins

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

// T01 (table): which capability each route needs comes from capabilities.toml.
func TestCapabilityOfRoutes(t *testing.T) {
	cases := map[string][]string{
		"data/put":            nil,
		"job/grants":          nil,
		"log/write":           nil,
		"library/video":       {"library.read"},
		"transcript/words":    {"transcript.read"},
		"media/open":          {"media.read"},
		"media/read":          {"media.read"},
		"media/frame":         {"media.frames"},
		"media/audio":         {"media.audio"},
		"media/info":          {"library.read"},
		"write/set-field":     {"metadata.write"},
		"write/add-tags":      {"tags.write"},
		"write/set-chapters":  {"chapters.write"},
		"write/add-subtitle":  {"subtitles.write"},
		"write/set-artwork":   {"artwork.write"},
		"suggest/add":         {"suggest"},
		"queue/request":       {"jobs.enqueue"},
		"notify/admin":        {"notify.admin"},
		"exports/read":        {"plugin-data.read"},
		"write/delete-videos": {"-"},
	}
	for route, want := range cases {
		iface, fn, _ := strings.Cut(route, "/")
		got := capabilityOf(iface, fn)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: %v, want %v", route, got, want)
		}
	}
}

// Every function of the WIT world has a route, and every route a WIT function: the API
// and the host cannot drift apart.
func TestEveryWITFunctionHasARoute(t *testing.T) {
	src, err := os.ReadFile("../../../shared/plugins/wit/maktaba-plugin.wit")
	if err != nil {
		t.Fatal(err)
	}
	ifaceRe := regexp.MustCompile(`(?m)^interface ([a-z-]+) \{`)
	fnRe := regexp.MustCompile(`(?m)^  %?([a-z][a-z-]*): func`)
	s := &Server{}
	s.init()
	want := map[string]bool{}
	locs := ifaceRe.FindAllStringSubmatchIndex(string(src), -1)
	for i, l := range locs {
		name := string(src[l[2]:l[3]])
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if name == "types" {
			continue
		}
		for _, m := range fnRe.FindAllStringSubmatch(string(src[l[0]:end]), -1) {
			want[name+"/"+m[1]] = true
		}
	}
	// handled in the host itself, never forwarded: progress and checkpoints go in the
	// dispatch frames, scratch stays in the host's memory, the subject and payload ride Run
	for _, local := range []string{"job/job-subject", "job/payload", "job/progress", "job/checkpoint", "job/last-checkpoint",
		"job/scratch-put", "job/scratch-get", "log/write"} {
		delete(want, local)
	}
	for r := range want {
		if s.routes[r] == nil {
			t.Errorf("WIT function %s has no route", r)
		}
	}
	for r := range s.routes {
		if !want[r] && r != "log/write" {
			t.Errorf("route %s is no WIT function", r)
		}
	}
}

func TestFieldAllowList(t *testing.T) {
	id := "dev.example.x"
	for _, f := range []string{"description", "sort_title", "year", "content_rating", "original_language", "local_ids." + ShortID(id)} {
		if !FieldAllowed(id, f) {
			t.Errorf("%s refused", f)
		}
	}
	for _, f := range []string{"title", "state", "path", "library_id", "local_ids.other", "local_ids." + ShortID("dev.example.y"), "display_title"} {
		if FieldAllowed(id, f) {
			t.Errorf("%s allowed", f)
		}
	}
}

// T07: subtitles are parsed and written again: tags gone, SRT becomes WebVTT, nothing
// that is not a cue survives.
func TestSubtitleIsRewritten(t *testing.T) {
	srt := "1\r\n00:00:01,000 --> 00:00:02,500\r\n<b>Hello</b> <script>x</script>\r\n\r\n2\r\n00:00:03,000 --> 00:00:04,000\r\n<a href=\"http://x\">link</a>\r\n"
	got, err := Subtitle(srt)
	if err != nil {
		t.Fatal(err)
	}
	want := "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHello x\n\n00:00:03.000 --> 00:00:04.000\nlink\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	vtt := "WEBVTT\n\nNOTE a note\n\nSTYLE\n::cue { color: red }\n\n01:00:00.000 --> 01:00:01.000 align:start\n<v Bob>Hi &amp; bye\n"
	got, err = Subtitle(vtt)
	if err != nil || got != "WEBVTT\n\n01:00:00.000 --> 01:00:01.000\nHi & bye\n" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := Subtitle("no cues here"); err == nil {
		t.Fatal("a text without cues was accepted")
	}
	if _, err := Subtitle("\xff\xfe"); err == nil {
		t.Fatal("not UTF-8 was accepted")
	}
}

// T08: 200 calls a second, then plugin_rate.
func TestRateLimit(t *testing.T) {
	now := time.Unix(1000, 0)
	inv := &Invocation{bucket: RateBurst, bucketAt: now}
	ok := 0
	for i := 0; i < 300; i++ {
		if inv.takeRate(now) {
			ok++
		}
	}
	if ok != 200 {
		t.Fatalf("%d calls passed in one instant, want 200", ok)
	}
	if !inv.takeRate(now.Add(10 * time.Millisecond)) {
		t.Fatal("the bucket did not refill")
	}
}

func TestQueueNameMatchesTheManifestPackage(t *testing.T) {
	if QueueName("dev.example.silence-chapters", "x") != pluginmanifest.QueueName("dev.example.silence-chapters", "x") {
		t.Fatal("two queue names for one stage")
	}
	if len(ShortID("dev.example.x")) != 8 {
		t.Fatal("short id")
	}
}

// T05 (no token): every call without a token, or with an unknown one, is 401.
func TestNoTokenIsRefused(t *testing.T) {
	s := &Server{Tokens: NewTokens(nil)}
	for _, auth := range []string{"", "Bearer x", "Plugin "} {
		req := httptest.NewRequest(http.MethodPost, "/plugin-api/v1/data/get", strings.NewReader("{}"))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Code != 401 || !strings.Contains(w.Body.String(), CodeToken) {
			t.Fatalf("%q: %d %s", auth, w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plugin-api/v1/data/get", nil))
	if w.Code != 404 {
		t.Fatalf("GET: %d", w.Code)
	}
}

func TestImageKinds(t *testing.T) {
	if imageExt([]byte("\x89PNG\r\n\x1a\nxxxx")) != ".png" || imageExt([]byte{0xff, 0xd8, 0xff, 0xe0}) != ".jpg" || imageExt([]byte("<svg")) != "" {
		t.Fatal("image kinds")
	}
}
