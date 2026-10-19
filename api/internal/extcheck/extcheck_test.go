package extcheck

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

const goodToken = "tok-SECRET-plex-123456"

type fakeExt map[string]map[string]any

func (f fakeExt) ExtensionSettings(_ context.Context, name string) (map[string]any, bool, error) {
	if s, ok := f[name]; ok {
		return s, true, nil
	}
	return map[string]any{}, false, nil
}

// fakePlex is a Plex server: /identity without a token, the rest with goodToken.
type fakePlex struct {
	mu     sync.Mutex
	tokens []string // every X-Plex-Token seen
	livetv string   // "ok" (DVRs), "none" (no DVR), "missing" (404)
	delay  time.Duration
	srv    *httptest.Server
}

func newFakePlex(t *testing.T, livetv string) *fakePlex {
	f := &fakePlex{livetv: livetv}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePlex) serve(w http.ResponseWriter, r *http.Request) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-r.Context().Done():
			return
		}
	}
	tok := r.Header.Get("X-Plex-Token")
	f.mu.Lock()
	if tok != "" {
		f.tokens = append(f.tokens, tok)
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/identity" {
		_, _ = w.Write([]byte(`{"MediaContainer":{"size":0,"claimed":true,"machineIdentifier":"abcdef0123456789","version":"1.41.0.1234"}}`))
		return
	}
	if tok != goodToken {
		// a real Plex answers 401 with an HTML body; echo the token to prove it is never copied
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("<html>401 Unauthorized " + tok + "</html>"))
		return
	}
	switch r.URL.Path {
	case "/":
		_, _ = w.Write([]byte(`{"MediaContainer":{"friendlyName":"Home NAS","version":"1.41.0.1234","platform":"Linux"}}`))
	case "/library/sections":
		_, _ = w.Write([]byte(`{"MediaContainer":{"size":2,"Directory":[
			{"key":"1","title":"Movies","type":"movie","Location":[{"path":"/volume1/Movies"}]},
			{"key":"2","title":"Lessons","type":"show","Location":[{"path":"/volume1/Originals/Lessons"}]}]}}`))
	case "/livetv/dvrs":
		switch f.livetv {
		case "missing":
			w.WriteHeader(http.StatusNotFound)
		case "none":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":0}}`))
		default:
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":2,"Dvr":[
				{"key":"5","title":"Maktaba","Device":{"key":"7","uuid":"device://tv.plex.grabbers.hdhomerun/1A2B3C4D","uri":"http://10.0.0.5:8781","title":"Maktaba"}},
				{"key":"6","title":"dizqueTV","Device":[{"key":"8","uuid":"device://tv.plex.grabbers.hdhomerun/DZ000001","uri":"http://10.0.0.6:8000","make":"Silicondust","model":"HDTC-2US","title":"dizqueTV"}]}]}}`))
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakePlex) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tokens...)
}

func testDB(t *testing.T, deviceID, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE hdhr_device (id INTEGER PRIMARY KEY, device_id TEXT NOT NULL)`,
		`CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT NOT NULL, roots TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if deviceID != "" {
		if _, err := db.Exec(`INSERT INTO hdhr_device (id, device_id) VALUES (1, ?)`, deviceID); err != nil {
			t.Fatal(err)
		}
	}
	if root != "" {
		b, _ := json.Marshal([]string{root})
		if _, err := db.Exec(`INSERT INTO libraries (id, name, roots) VALUES ('l1', 'Lessons', ?)`, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

type harness struct {
	logs *bytes.Buffer
	deps Deps
}

func newHarness(t *testing.T, ext fakeExt, token string, db *sql.DB) *harness {
	t.Helper()
	store := secrets.NewMemory()
	if token != "" {
		if err := store.Put(context.Background(), PlexTokenSecret, []byte(token), "test"); err != nil {
			t.Fatal(err)
		}
	}
	buf := &bytes.Buffer{}
	return &harness{logs: buf, deps: Deps{
		Ext: ext, Secrets: store, DB: db, Timeout: 2 * time.Second,
		Logger: slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}}
}

func lineOf(t *testing.T, res Result, key string) Line {
	t.Helper()
	for _, l := range res.Lines {
		if l.Key == key {
			return l
		}
	}
	t.Fatalf("no line %q in %+v", key, res.Lines)
	return Line{}
}

// noToken: the token is in neither the answer nor the logs.
func noToken(t *testing.T, h *harness, res Result) {
	t.Helper()
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), goodToken) || strings.Contains(string(b), "bad-token-xyz") {
		t.Fatalf("token in the answer: %s", b)
	}
	if strings.Contains(h.logs.String(), goodToken) || strings.Contains(h.logs.String(), "bad-token-xyz") {
		t.Fatalf("token in the logs: %s", h.logs.String())
	}
}

func TestPlexSuccess(t *testing.T) {
	fp := newFakePlex(t, "ok")
	ext := fakeExt{
		"plex":     {"url": fp.srv.URL + "/", "path_map": `{"smb://nas/Originals/": "/volume1/Originals/"}`},
		"dizquetv": {"url": "http://10.0.0.6:8000"},
	}
	h := newHarness(t, ext, goodToken, testDB(t, "1a2b3c4d", "smb://nas/Originals/Lessons"))
	res, err := (&Plex{h.deps}).Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Message != "Plex is ready" {
		t.Fatalf("not ok: %+v", res)
	}
	for _, k := range []string{"reachable", "token", "server", "livetv", "maktaba_tuner", "dizquetv_tuner", "path_map"} {
		if l := lineOf(t, res, k); !l.OK {
			t.Errorf("%s not ok: %+v", k, l)
		}
	}
	if d := lineOf(t, res, "reachable").Detail; !strings.Contains(d, "1.41.0.1234") || !strings.Contains(d, "ms") {
		t.Errorf("reachable detail %q", d)
	}
	if lineOf(t, res, "reachable").LatencyMS == nil {
		t.Error("no latency")
	}
	if d := lineOf(t, res, "token").Detail; !strings.Contains(d, "2 libraries") {
		t.Errorf("token detail %q", d)
	}
	if d := lineOf(t, res, "server").Detail; !strings.Contains(d, "Home NAS") {
		t.Errorf("server detail %q", d)
	}
	if d := lineOf(t, res, "livetv").Detail; d != "2 DVRs" {
		t.Errorf("livetv detail %q", d)
	}
	if d := lineOf(t, res, "dizquetv_tuner").Detail; !strings.Contains(d, "present") {
		t.Errorf("dizquetv detail %q", d)
	}
	if d := lineOf(t, res, "path_map").Detail; !strings.Contains(d, `"Lessons"`) {
		t.Errorf("path_map detail %q", d)
	}
	noToken(t, h, res)
	for _, tok := range fp.seen() {
		if tok != goodToken {
			t.Errorf("unexpected token sent %q", tok)
		}
	}
}

func TestPlexBadToken(t *testing.T) {
	fp := newFakePlex(t, "ok")
	h := newHarness(t, fakeExt{"plex": {"url": fp.srv.URL}}, "bad-token-xyz", nil)
	res, _ := (&Plex{h.deps}).Test(context.Background())
	l := lineOf(t, res, "token")
	if res.OK || l.OK || l.Code != CodeTokenRejected || !strings.Contains(l.Detail, "Sign in with Plex again") {
		t.Fatalf("got %+v / %+v", res, l)
	}
	if !lineOf(t, res, "reachable").OK || !lineOf(t, res, "livetv").Skipped {
		t.Fatalf("lines %+v", res.Lines)
	}
	noToken(t, h, res)
}

func TestPlexNoToken(t *testing.T) {
	fp := newFakePlex(t, "ok")
	h := newHarness(t, fakeExt{"plex": {"url": fp.srv.URL}}, "", nil)
	res, _ := (&Plex{h.deps}).Test(context.Background())
	if l := lineOf(t, res, "token"); res.OK || l.Code != CodeNoToken {
		t.Fatalf("got %+v", l)
	}
	if len(fp.seen()) != 0 {
		t.Fatal("a token was sent")
	}
}

func TestPlexNotConfigured(t *testing.T) {
	h := newHarness(t, fakeExt{}, goodToken, nil)
	res, _ := (&Plex{h.deps}).Test(context.Background())
	if l := lineOf(t, res, "reachable"); res.OK || l.Code != CodeNotConfigured {
		t.Fatalf("got %+v", l)
	}
	if len(res.Lines) != 7 {
		t.Fatalf("want every check listed, got %d", len(res.Lines))
	}
}

func closedURL(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "http://" + addr
}

func TestPlexUnreachable(t *testing.T) {
	h := newHarness(t, fakeExt{"plex": {"url": closedURL(t)}}, goodToken, nil)
	res, _ := (&Plex{h.deps}).Test(context.Background())
	l := lineOf(t, res, "reachable")
	if res.OK || l.Code != CodeUnreachable {
		t.Fatalf("got %+v", l)
	}
	if !lineOf(t, res, "token").Skipped {
		t.Fatal("token must be skipped")
	}
	noToken(t, h, res)
}

func TestPlexTimeout(t *testing.T) {
	fp := newFakePlex(t, "ok")
	fp.delay = 2 * time.Second
	h := newHarness(t, fakeExt{"plex": {"url": fp.srv.URL}}, goodToken, nil)
	h.deps.Timeout = 150 * time.Millisecond
	start := time.Now()
	res, _ := (&Plex{h.deps}).Test(context.Background())
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("took %s", time.Since(start))
	}
	if l := lineOf(t, res, "reachable"); res.OK || l.Code != CodeTimeout {
		t.Fatalf("got %+v", l)
	}
}

func TestPlexLiveTVMissing(t *testing.T) {
	for _, mode := range []string{"missing", "none"} {
		t.Run(mode, func(t *testing.T) {
			fp := newFakePlex(t, mode)
			h := newHarness(t, fakeExt{"plex": {"url": fp.srv.URL}}, goodToken, testDB(t, "1A2B3C4D", ""))
			res, _ := (&Plex{h.deps}).Test(context.Background())
			l := lineOf(t, res, "livetv")
			if !res.OK || l.OK || !l.Optional || l.Code != CodeLiveTVMissing {
				t.Fatalf("Live TV missing is a warning: %+v / %+v", res, l)
			}
			if mt := lineOf(t, res, "maktaba_tuner"); mt.OK {
				t.Fatalf("tuner %+v", mt)
			}
			if pm := lineOf(t, res, "path_map"); !pm.Skipped {
				t.Fatalf("path_map without a library must be skipped: %+v", pm)
			}
		})
	}
}

func TestPlexTunerNotRegisteredAndPathMapMismatch(t *testing.T) {
	fp := newFakePlex(t, "ok")
	h := newHarness(t, fakeExt{"plex": {"url": fp.srv.URL}}, goodToken, testDB(t, "FFFF0000", "Y:/Elsewhere"))
	res, _ := (&Plex{h.deps}).Test(context.Background())
	if !res.OK {
		t.Fatalf("warnings only: %+v", res)
	}
	if l := lineOf(t, res, "maktaba_tuner"); l.OK || l.Code != CodeNotFound || !strings.Contains(l.Detail, "FFFF0000") {
		t.Fatalf("tuner %+v", l)
	}
	if l := lineOf(t, res, "path_map"); l.OK || l.Code != CodePathMap {
		t.Fatalf("path_map %+v", l)
	}
}

// A redirect to another host is refused and the token never reaches it.
func TestPlexRedirectToOtherHostRefused(t *testing.T) {
	var mu sync.Mutex
	evil := []string{}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		evil = append(evil, r.Header.Get("X-Plex-Token"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"x"}}`))
	}))
	defer other.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity" {
			_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"abc","version":"1"}}`))
			return
		}
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	defer front.Close()
	h := newHarness(t, fakeExt{"plex": {"url": front.URL}}, goodToken, nil)
	res, _ := (&Plex{h.deps}).Test(context.Background())
	if l := lineOf(t, res, "token"); l.OK || l.Code != CodeRedirect {
		t.Fatalf("got %+v", l)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(evil) != 0 {
		t.Fatalf("the other host was called: %v", evil)
	}
	noToken(t, h, res)
}

func TestPlexTLSUntrusted(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	h := newHarness(t, fakeExt{"plex": {"url": srv.URL}}, goodToken, nil)
	res, _ := (&Plex{h.deps}).Test(context.Background())
	l := lineOf(t, res, "reachable")
	if l.OK || l.Code != CodeTLSUntrusted || !strings.Contains(l.Detail, "plex.direct") {
		t.Fatalf("got %+v", l)
	}
}

func TestPathMap(t *testing.T) {
	pm, err := ParsePathMap(`Y:\=/volume1/Y/;smb://nas/O/=/volume1/O/`)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		`Y:\Shows\a.mp4`:  "/volume1/Y/Shows/a.mp4",
		"smb://nas/O/x":   "/volume1/O/x",
		"smb://nas/O":     "/volume1/O",
		"/unmapped/a.mp4": "/unmapped/a.mp4",
	} {
		if got := pm.Rewrite(in); got != want {
			t.Errorf("%s: %s want %s", in, got, want)
		}
	}
	if _, err := ParsePathMap("{bad"); err == nil {
		t.Error("bad JSON must fail")
	}
	for in, want := range map[string]string{
		`{"/a/b","c"}`:   "/a/b",
		`{/a/b,/c}`:      "/a/b",
		`{"/a \"q\"/b"}`: `/a "q"/b`,
		`["/x"]`:         "/x",
		`{}`:             "",
	} {
		if got := firstRoot(in); got != want {
			t.Errorf("firstRoot(%s) = %q want %q", in, got, want)
		}
	}
}

// fakeDizque is a dizqueTV 1.x server (src/api.js).
func newFakeDizque(t *testing.T, servers string, delay time.Duration) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if r.Method != http.MethodGet {
			t.Errorf("dizqueTV test must be read-only, got %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"dizquetv":"1.7.0","ffmpeg":"6.0","nodejs":"v20.1.0"}`))
		case "/api/channelNumbers":
			_, _ = w.Write([]byte(`[1,2,3,10]`))
		case "/api/plex-servers":
			_, _ = w.Write([]byte(servers))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDizqueSuccess(t *testing.T) {
	srv := newFakeDizque(t, `[{"name":"Home NAS","uri":"http://10.0.0.5:32400","accessToken":"`+goodToken+`","index":0}]`, 0)
	h := newHarness(t, fakeExt{"dizquetv": {"url": srv.URL}, "plex": {"url": "http://10.0.0.5:32400/"}}, "", nil)
	res, err := (&DizqueTV{h.deps}).Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Message != "dizqueTV is ready" {
		t.Fatalf("got %+v", res)
	}
	if d := lineOf(t, res, "reachable").Detail; !strings.Contains(d, "1.7.0") {
		t.Errorf("reachable %q", d)
	}
	if d := lineOf(t, res, "channels").Detail; d != "4 channels" {
		t.Errorf("channels %q", d)
	}
	if d := lineOf(t, res, "plex_servers").Detail; d != "Home NAS (Home NAS is Maktaba's Plex)" {
		t.Errorf("plex_servers %q", d)
	}
	noToken(t, h, res)
}

func TestDizqueNoPlexServer(t *testing.T) {
	srv := newFakeDizque(t, `[]`, 0)
	h := newHarness(t, fakeExt{"dizquetv": {"url": srv.URL}}, "", nil)
	res, _ := (&DizqueTV{h.deps}).Test(context.Background())
	l := lineOf(t, res, "plex_servers")
	if !res.OK || l.OK || !l.Optional || l.Code != CodeNotFound {
		t.Fatalf("got %+v / %+v", res, l)
	}
}

func TestDizqueUnreachableAndTimeout(t *testing.T) {
	h := newHarness(t, fakeExt{"dizquetv": {"url": closedURL(t)}}, "", nil)
	res, _ := (&DizqueTV{h.deps}).Test(context.Background())
	if l := lineOf(t, res, "reachable"); res.OK || l.Code != CodeUnreachable {
		t.Fatalf("unreachable: %+v", l)
	}
	if !lineOf(t, res, "channels").Skipped {
		t.Fatal("channels must be skipped")
	}

	srv := newFakeDizque(t, `[]`, 2*time.Second)
	h = newHarness(t, fakeExt{"dizquetv": {"url": srv.URL}}, "", nil)
	h.deps.Timeout = 150 * time.Millisecond
	res, _ = (&DizqueTV{h.deps}).Test(context.Background())
	if l := lineOf(t, res, "reachable"); res.OK || l.Code != CodeTimeout {
		t.Fatalf("timeout: %+v", l)
	}
}

func TestDizqueNotDizque(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>hello</html>`))
	}))
	defer srv.Close()
	h := newHarness(t, fakeExt{"dizquetv": {"url": srv.URL}}, "", nil)
	res, _ := (&DizqueTV{h.deps}).Test(context.Background())
	if l := lineOf(t, res, "reachable"); res.OK || l.Code != CodeBadAnswer {
		t.Fatalf("got %+v", l)
	}
}

func TestSameHost(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"http://10.0.0.6:8000/x", "10.0.0.6:8000", true},
		{"https://h", "http://h:443", true},
		{"http://h", "http://h:8000", false},
		{"", "http://h", false},
	}
	for _, c := range cases {
		if got := SameHost(c.a, c.b); got != c.want {
			t.Errorf("SameHost(%q,%q)=%v", c.a, c.b, got)
		}
	}
}
