package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const secret = "s3cr3t-PLEX-token-xyz"

// fakePlex records what reached it and answers per path.
type fakePlex struct {
	mu     sync.Mutex
	seen   []string // "METHOD path?query token=<header>"
	handle func(w http.ResponseWriter, r *http.Request)
}

func (f *fakePlex) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.Method+" "+r.URL.RequestURI()+" token="+r.Header.Get("X-Plex-Token"))
	h := f.handle
	f.mu.Unlock()
	if h != nil {
		h(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"MediaContainer":{"machineIdentifier":"abc"}}`)
}

func (f *fakePlex) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

type bufSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
	n   int
}

func (b *bufSink) Chunk(_ context.Context, d []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.n++
	b.buf.Write(d)
	return nil
}

func (b *bufSink) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type harness struct {
	plex     *fakePlex
	srv      *httptest.Server
	door     *Door
	settings Settings
	up       Upstream
	member   error
	audit    []Call
	mu       sync.Mutex
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{plex: &fakePlex{}, settings: DefaultSettings()}
	h.srv = httptest.NewServer(h.plex)
	t.Cleanup(h.srv.Close)
	h.up = Upstream{Enabled: true, URL: h.srv.URL, Secret: secret}
	h.door = New(Config{
		Settings: func(context.Context) (Settings, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.settings, nil
		},
		Upstream: func(context.Context, *Extension) (Upstream, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.up, nil
		},
		Member: func(context.Context, Caller) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.member
		},
		Audit: func(c Call) {
			h.mu.Lock()
			h.audit = append(h.audit, c)
			h.mu.Unlock()
		},
		Chunk:  64,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return h
}

func (h *harness) set(f func(h *harness)) {
	h.mu.Lock()
	f(h)
	h.mu.Unlock()
	h.door.Forget()
}

var node = Caller{Kind: "node", NodeID: "n-1", NodeName: "mac", Session: "s1", WorkerID: "w1", IP: "10.0.0.2"}

func (h *harness) call(method, target string) (Result, string) {
	sink := &bufSink{}
	res := h.door.Serve(context.Background(), node, Request{Ext: "plex", Method: method, Target: target,
		Accept: "application/json"}, sink)
	return res, sink.String()
}

// Every call the node-placed Plex stages make (PlexClient) is allowed.
func TestAllowListAcceptsTheStagesCalls(t *testing.T) {
	uri := "server%3A%2F%2Fabc123%2Fcom.plexapp.plugins.library%2Flibrary%2Fmetadata%2F11%2C12"
	ok := [][2]string{
		{"GET", "/identity"},
		{"GET", "/library/sections"},
		{"GET", "/library/sections/3/all?type=1"},
		{"GET", "/library/sections/3/all?type=4"},
		{"GET", "/library/sections/3/collections"},
		{"GET", "/library/metadata/11"},
		{"GET", "/library/metadata/11,12,13"},
		{"GET", "/library/collections/90/children"},
		{"POST", "/library/collections?type=1&title=%D9%85%D9%84%D9%81%20one&smart=0&sectionId=3&uri=" + uri},
		{"PUT", "/library/collections/90/items?uri=" + uri},
		{"PUT", "/library/collections/90/items/12/move?after=11"},
		{"PUT", "/library/collections/90/items/12/move"},
		{"PUT", "/library/metadata/90/prefs?collectionSort=2"},
		{"PUT", "/library/sections/3/all?type=18&id=90&label[0].tag.tag=maktaba-auto&label.locked=1"},
		{"DELETE", "/library/metadata/90"},
	}
	for _, c := range ok {
		if _, _, _, err := Plex.Match(c[0], c[1]); err != nil {
			t.Errorf("%s %s refused: %v", c[0], c[1], err)
		}
	}
}

func TestAllowListRefusals(t *testing.T) {
	uri := "server%3A%2F%2Fabc%2Fcom.plexapp.plugins.library%2Flibrary%2Fmetadata%2F11"
	bad := []struct{ method, target string }{
		{"GET", "/"},
		{"GET", "/:/prefs"},
		{"PUT", "/:/prefs?FriendlyName=x"},
		{"GET", "/system"},
		{"GET", "/accounts"},
		{"GET", "/library/sections/3/refresh"},
		{"POST", "/library/sections/3/refresh"},
		{"DELETE", "/library/sections/3"},
		{"DELETE", "/library/metadata/1,2"},
		{"GET", "/library/metadata/abc"},
		{"GET", "/library/metadata/1/../../:/prefs"},
		{"GET", "/library/metadata/%31"},
		{"GET", "/library//sections"},
		{"GET", "http://evil.example/identity"},
		{"GET", "//evil.example/identity"},
		{"GET", "identity"},
		{"GET", "/identity?X-Plex-Token=stolen"},
		{"GET", "/identity?x-plex-token=stolen"},
		{"GET", "/identity?extra=1"},
		{"GET", "/library/sections/3/all?type=1&type=4"},
		{"GET", "/library/sections/3/all?type=18"},
		{"POST", "/identity"},
		{"PUT", "/library/sections/3/all?type=1&id=90&title.value=renamed"},
		{"PUT", "/library/sections/3/all?type=18&id=90&label[0].tag.tag=x&summary.value=y"},
		{"POST", "/library/collections?type=1&title=x&smart=1&sectionId=3&uri=" + uri},
		{"POST", "/library/collections?type=1&title=x&smart=0&sectionId=3&uri=http%3A%2F%2Fevil"},
		{"POST", "/library/collections?type=1&title=x&smart=0&sectionId=3"},
		{"PUT", "/library/metadata/90/prefs?collectionSort=2&x=1"},
		{"GET", "/livetv/dvrs"},
		{"POST", "/media/grabbers/devices?uri=http%3A%2F%2Fx"},
		{"GET", "/identity#frag"},
		{"GET", "/identity\\x"},
		{"GET", "/ident ity"},
	}
	for _, c := range bad {
		if r, _, _, err := Plex.Match(c.method, c.target); err == nil {
			t.Errorf("%s %s allowed (rule %s %s)", c.method, c.target, r.Method, r.Path)
		}
	}
}

func TestServeInjectsTheTokenAndNeverReturnsIt(t *testing.T) {
	h := newHarness(t)
	h.plex.handle = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Plex-Token", secret) // never passed back: only the type is
		w.Header().Set("Set-Cookie", "t="+secret)
		// thumb URLs with the token, the literal token, its escaped form
		fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[`)
		for i := 0; i < 40; i++ {
			fmt.Fprintf(w, `{"ratingKey":"%d","thumb":"/library/metadata/%d/thumb?X-Plex-Token=%s","art":"http://p/x?a=1&X-Plex-Token=%s&b=2","note":"%s %s"},`,
				i, i, secret, secret, secret, strings.ReplaceAll(secret, "-", "%2D"))
		}
		fmt.Fprint(w, `{"ratingKey":"x"}]}}`)
	}
	res, body := h.call("GET", "/library/sections/3/all?type=1")
	if res.Err != nil || res.Status != 200 {
		t.Fatalf("result %+v", res)
	}
	calls := h.plex.calls()
	if len(calls) != 1 || calls[0] != "GET /library/sections/3/all?type=1 token="+secret {
		t.Fatalf("plex saw %v", calls)
	}
	if strings.Contains(body, secret) || strings.Contains(strings.ToLower(body), "x-plex-token") {
		t.Fatalf("the answer carries the token: %s", body)
	}
	if !strings.Contains(body, `"thumb":"/library/metadata/7/thumb?"`) || !strings.Contains(body, `"art":"http://p/x?a=1&b=2"`) {
		t.Fatalf("thumb URLs not rewritten as expected: %.400s", body)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("scrubbed answer is not JSON any more: %v", err)
	}
	if res.ContentType != "application/json" {
		t.Fatalf("content type %q", res.ContentType)
	}
	h.mu.Lock()
	audit := h.audit
	h.mu.Unlock()
	if len(audit) != 1 || audit[0].Path != "/library/sections/3/all" || audit[0].Status != 200 || audit[0].NodeID != "n-1" ||
		audit[0].Scrubbed == 0 {
		t.Fatalf("audit %+v", audit)
	}
	b, _ := json.Marshal(audit)
	st, _ := json.Marshal(h.door.Status(context.Background()))
	if strings.Contains(string(b), secret) || strings.Contains(string(st), secret) {
		t.Fatal("the audit or the status carries the token")
	}
}

func TestScrubberAcrossChunkBoundaries(t *testing.T) {
	in := `a?X-Plex-Token=` + secret + `&b=1 "` + secret + `" x-plex-token%3D` + secret + ` end`
	for _, size := range []int{1, 2, 3, 7, 16, 1000} {
		sc := NewScrubber("X-Plex-Token", secret)
		var out []byte
		for i := 0; i < len(in); i += size {
			j := i + size
			if j > len(in) {
				j = len(in)
			}
			out = append(out, sc.Feed([]byte(in[i:j]))...)
		}
		out = append(out, sc.Flush()...)
		if strings.Contains(string(out), secret) || strings.Contains(strings.ToLower(string(out)), "plex-token") {
			t.Fatalf("chunk %d: %q", size, out)
		}
		if !strings.HasPrefix(string(out), "a?&b=1") || !strings.HasSuffix(string(out), " end") {
			t.Fatalf("chunk %d: %q", size, out)
		}
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	h := newHarness(t)
	var followed atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	defer other.Close()
	h.plex.handle = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal?X-Plex-Token="+secret, http.StatusFound)
	}
	res, body := h.call("GET", "/identity")
	if followed.Load() {
		t.Fatal("the redirect was followed (the token header would go along)")
	}
	if res.Status != http.StatusFound || strings.Contains(body, secret) {
		t.Fatalf("res %+v body %q", res, body)
	}
}

func TestRefusals(t *testing.T) {
	h := newHarness(t)
	code := func(res Result) string {
		if res.Err == nil {
			return ""
		}
		return res.Err.Code
	}
	if res, _ := h.call("GET", "/:/prefs"); code(res) != CodeNotAllowed {
		t.Fatalf("not allowed: %+v", res)
	}
	if res, _ := h.call("GET", "/library/metadata/%2e%2e"); code(res) != CodeBadRequest {
		t.Fatalf("bad target: %+v", res)
	}
	res := h.door.Serve(context.Background(), node, Request{Ext: "cloudflare_stream", Method: "GET", Target: "/accounts"}, &bufSink{})
	if code(res) != CodeHomeOnly {
		t.Fatalf("cloudflare: %+v", res)
	}
	res = h.door.Serve(context.Background(), node, Request{Ext: "nope", Method: "GET", Target: "/x"}, &bufSink{})
	if code(res) != CodeUnknown {
		t.Fatalf("unknown: %+v", res)
	}
	h.set(func(h *harness) { h.member = errors.New("revoked") })
	if res, _ := h.call("GET", "/identity"); code(res) != CodeNotMember {
		t.Fatalf("not member: %+v", res)
	}
	h.set(func(h *harness) { h.member = nil; h.up.Secret = "" })
	if res, _ := h.call("GET", "/identity"); code(res) != CodeNotConfigured {
		t.Fatalf("no token: %+v", res)
	}
	h.set(func(h *harness) { h.up.Secret = secret; h.up.Enabled = false })
	if res, _ := h.call("GET", "/identity"); code(res) != CodeNotConfigured {
		t.Fatalf("extension off: %+v", res)
	}
	h.set(func(h *harness) { h.up.Enabled = true; h.settings.Enabled = false })
	if res, _ := h.call("GET", "/identity"); code(res) != CodeOff {
		t.Fatalf("relay off: %+v", res)
	}
	if got := h.plex.calls(); len(got) != 0 {
		t.Fatalf("a refused call reached Plex: %v", got)
	}
	big := Request{Ext: "plex", Method: "PUT", Target: "/library/metadata/1/prefs?collectionSort=2", Body: make([]byte, MaxRequestBody+1)}
	h.set(func(h *harness) { h.settings.Enabled = true })
	if res := h.door.Serve(context.Background(), node, big, &bufSink{}); code(res) != CodeTooLarge {
		t.Fatalf("big body: %+v", res)
	}
}

func TestDeleteOnlyRemovesCollections(t *testing.T) {
	h := newHarness(t)
	h.plex.handle = func(w http.ResponseWriter, r *http.Request) {
		typ := "movie"
		if strings.HasSuffix(r.URL.Path, "/90") {
			typ = "collection"
		}
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","type":%q}]}}`, typ)
		}
	}
	if res, _ := h.call("DELETE", "/library/metadata/5"); res.Err == nil || res.Err.Code != CodeNotAllowed {
		t.Fatalf("deleting a movie: %+v", res)
	}
	if res, _ := h.call("DELETE", "/library/metadata/90"); res.Err != nil || res.Status != 200 {
		t.Fatalf("deleting a collection: %+v", res)
	}
	var deletes []string
	for _, c := range h.plex.calls() {
		if strings.HasPrefix(c, "DELETE") {
			deletes = append(deletes, c)
		}
	}
	if len(deletes) != 1 || !strings.HasPrefix(deletes[0], "DELETE /library/metadata/90 ") {
		t.Fatalf("deletes %v", deletes)
	}
}

func TestRateLimitPerMachine(t *testing.T) {
	h := newHarness(t)
	h.set(func(h *harness) { h.settings.RequestsPerMinute = 1; h.settings.Burst = 2 })
	for i := 0; i < 2; i++ {
		if res, _ := h.call("GET", "/identity"); res.Err != nil {
			t.Fatalf("call %d: %+v", i, res)
		}
	}
	res, _ := h.call("GET", "/identity")
	if res.Err == nil || res.Err.Code != CodeRateLimited {
		t.Fatalf("third call: %+v", res)
	}
	// another machine has its own budget
	other := node
	other.NodeID, other.Session = "n-2", "s2"
	if res := h.door.Serve(context.Background(), other, Request{Ext: "plex", Method: "GET", Target: "/identity"}, &bufSink{}); res.Err != nil {
		t.Fatalf("other machine: %+v", res)
	}
	st := h.door.Status(context.Background())
	var refused int
	for _, tot := range st.Totals {
		if tot.Caller == "node:n-1" {
			refused = tot.Refused
		}
	}
	if refused != 1 {
		t.Fatalf("totals %+v", st.Totals)
	}
}

func TestInFlightCap(t *testing.T) {
	old := SlotWaitMax
	SlotWaitMax = 100 * time.Millisecond
	defer func() { SlotWaitMax = old }()
	h := newHarness(t)
	release := make(chan struct{})
	h.plex.handle = func(http.ResponseWriter, *http.Request) { <-release }
	h.set(func(h *harness) { h.settings.MaxInFlight = 1 })
	first := make(chan Result)
	go func() { r, _ := h.call("GET", "/identity"); first <- r }()
	deadline := time.Now().Add(2 * time.Second)
	for len(h.plex.calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	res, _ := h.call("GET", "/identity")
	close(release)
	if res.Err == nil || res.Err.Code != CodeBusy {
		t.Fatalf("second call: %+v", res)
	}
	if r := <-first; r.Err != nil {
		t.Fatalf("first call: %+v", r)
	}
}

func TestAnswerSizeLimitAndTimeout(t *testing.T) {
	h := newHarness(t)
	h.plex.handle = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity" {
			time.Sleep(1500 * time.Millisecond)
			return
		}
		_, _ = w.Write(bytes.Repeat([]byte("x"), 2<<20))
	}
	h.set(func(h *harness) { h.settings.MaxResponseMB = 1; h.settings.TimeoutSec = 1 })
	res, body := h.call("GET", "/library/sections")
	if res.Err == nil || res.Err.Code != CodeTooLarge || len(body) > 1<<20 {
		t.Fatalf("too large: %+v (%d bytes)", res, len(body))
	}
	res, _ = h.call("GET", "/identity")
	if res.Err == nil || res.Err.Code != CodeTimeout {
		t.Fatalf("timeout: %+v", res)
	}
}

func TestSettingsFromRules(t *testing.T) {
	s := SettingsFrom(map[string]any{"enabled": false, "requests_per_minute": json.Number("30"), "burst": float64(5),
		"max_in_flight": json.Number("0"), "timeout_sec": "x"})
	if s.Enabled || s.RequestsPerMinute != 30 || s.Burst != 5 || s.MaxInFlight != 4 || s.TimeoutSec != 120 {
		t.Fatalf("%+v", s)
	}
}
