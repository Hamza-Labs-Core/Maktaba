package relay

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Story 50.04: relay processes that share a directory forward to each other
// and hold requests while a home reconnects.

var peerSecret = []byte(strings.Repeat("z", 32))

// simLookup resolves the simulated homes' slugs; seen sets LastSeenAt.
type simLookup struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (l *simLookup) BySlug(_ context.Context, slug string) (stores.Server, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sv := stores.Server{ID: slug, Slug: slug, Plan: "pro"}
	if at, ok := l.seen[slug]; ok {
		sv.LastSeenAt = sql.NullTime{Time: at, Valid: true}
	}
	return sv, nil
}

func (l *simLookup) saw(slug string, at time.Time) {
	l.mu.Lock()
	if l.seen == nil {
		l.seen = map[string]time.Time{}
	}
	l.seen[slug] = at
	l.mu.Unlock()
}

// proc is one relay process: tunnels, the public handler, the peer listener.
type proc struct {
	id     string
	reg    *relaypkg.Registry
	meter  *fakeMeter
	deps   *Deps
	public *httptest.Server
	peer   *httptest.Server
	tunnel *httptest.Server
}

func newProc(t *testing.T, id string, dir *relaypkg.MemDirectory, store *simhome.Store, lookup ServerLookup, holdMax time.Duration, holds *Holds) *proc {
	t.Helper()
	p := &proc{id: id, reg: relaypkg.NewRegistry(), meter: &fakeMeter{}}
	auth := relaypkg.NewPeerAuth(peerSecret)
	// the peer listener's address must be known before the directory view
	peerHandler := &switchHandler{}
	p.peer = httptest.NewServer(peerHandler)
	addr := strings.TrimPrefix(p.peer.URL, "http://")
	view := dir.For(id, addr)
	p.deps = &Deps{Registry: p.reg, Lookup: lookup, Meter: p.meter, PublicHost: "relay.test",
		RelayID: id, Directory: view, PeerAuth: auth, HoldMax: holdMax, Holds: holds,
		Logger: slog.New(slog.DiscardHandler)}
	peerDeps := *p.deps
	peerDeps.PeerMode = true
	peerHandler.set(peerDeps.Handler())
	p.public = httptest.NewServer(p.deps.Handler())
	p.tunnel = httptest.NewServer(&relaypkg.TunnelServer{Registry: p.reg, Store: store,
		Logger: slog.New(slog.DiscardHandler), Directory: view})
	t.Cleanup(func() { p.public.Close(); p.peer.Close(); p.tunnel.Close() })
	return p
}

func (p *proc) endpoint() string {
	return "ws" + strings.TrimPrefix(p.tunnel.URL, "http") + "/v1/relay/ws"
}

type switchHandler struct {
	mu sync.Mutex
	h  http.Handler
}

func (s *switchHandler) set(h http.Handler) { s.mu.Lock(); s.h = h; s.mu.Unlock() }
func (s *switchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	h := s.h
	s.mu.Unlock()
	h.ServeHTTP(w, r)
}

func getVia(t *testing.T, srv *httptest.Server, slug, path string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	req.Host = slug + ".relay.test"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func waitOn(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// T03: H's tunnel is on blue; a request for H at green is forwarded to blue,
// the client gets the home's body, and the bytes are metered on blue only.
func TestForwardToTheProcessWithTheTunnel(t *testing.T) {
	api := simhome.API()
	defer api.Close()
	dir, store, lookup := relaypkg.NewMemDirectory(), simhome.NewStore(), &simLookup{}
	blue := newProc(t, "a-blue", dir, store, lookup, 0, nil)
	green := newProc(t, "a-green", dir, store, lookup, 0, nil)
	h := simhome.Start(1, blue.endpoint(), api.URL, simhome.Options{})
	defer h.Stop()
	waitOn(t, "H in the directory", func() bool { p, ok := dir.Row(h.Slug); return ok && p.RelayID == "a-blue" })

	resp := getVia(t, green.public, h.Slug, "/stream?bytes=300000&ms=100", nil)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || len(body) != 300000 {
		t.Fatalf("via green: %d, %d bytes", resp.StatusCode, len(body))
	}
	for i, c := range body {
		if c != simhome.Pattern(i) {
			t.Fatalf("byte %d wrong", i)
		}
	}
	// the owner meters once the body is through, just after the client's last byte
	waitOn(t, "blue's meter", func() bool { calls, out := blue.meter.snapshot(); return calls > 0 && out >= 300000 })
	if calls, _ := green.meter.snapshot(); calls != 0 {
		t.Errorf("green metered %d calls, want 0 (it only forwarded)", calls)
	}
}

// A peer request with no or a bad signature: 403. A client sending the hop
// header to the public listener changes nothing (it is stripped).
func TestPeerListenerRefusesUnsignedRequests(t *testing.T) {
	dir, store, lookup := relaypkg.NewMemDirectory(), simhome.NewStore(), &simLookup{}
	blue := newProc(t, "a-blue", dir, store, lookup, -1, nil)
	before := peerStats.authFailed.Load()
	for _, hdr := range []map[string]string{nil, {relaypkg.HeaderPeerAuth: "a-green.1.bad"}} {
		resp := getVia(t, blue.peer, "home-0001", "/api/health", hdr)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("peer request %v: %d, want 403", hdr, resp.StatusCode)
		}
	}
	if peerStats.authFailed.Load()-before != 2 {
		t.Error("relay_peer_auth_failed_total not counted")
	}
	resp := getVia(t, blue.public, "home-0001", "/api/health", map[string]string{relaypkg.HeaderHop: "1"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("public request with a hop header: %d", resp.StatusCode)
	}
}

// T04: a forwarded request (hop) for a home this process lacks is never
// forwarded again: it is held, then 503.
func TestAForwardedRequestIsNotForwardedAgain(t *testing.T) {
	api := simhome.API()
	defer api.Close()
	dir, store, lookup := relaypkg.NewMemDirectory(), simhome.NewStore(), &simLookup{}
	blue := newProc(t, "a-blue", dir, store, lookup, 400*time.Millisecond, nil)
	green := newProc(t, "a-green", dir, store, lookup, 400*time.Millisecond, nil)
	h := simhome.Start(2, blue.endpoint(), api.URL, simhome.Options{})
	defer h.Stop()
	waitOn(t, "H on blue", func() bool { _, ok := dir.Row(h.Slug); return ok })
	lookup.saw(h.Slug, time.Now())
	// signed as from blue, at green's peer port: green has no tunnel and must not bounce it to blue
	resp := getVia(t, green.peer, h.Slug, "/api/health", map[string]string{
		relaypkg.HeaderHop: "1", relaypkg.HeaderPeerAuth: relaypkg.NewPeerAuth(peerSecret).Sign("a-blue", h.Slug)})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" {
		t.Errorf("hopped request: %d Retry-After %q, want 503 held", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if calls, _ := blue.meter.snapshot(); calls != 0 {
		t.Error("the hopped request reached blue")
	}
}

// T05 and T06: a home seen 30 s ago is waited for: one that reconnects in
// 400 ms is answered then; one that never does gets 503 home_reconnecting
// with Retry-After 5 at the hold's end (a browser in its language). T09's
// Arabic page too. A home offline for 10 minutes gets 503 server_offline at once.
func TestHoldWhileTheHomeReconnects(t *testing.T) {
	api := simhome.API()
	defer api.Close()
	dir, store, lookup := relaypkg.NewMemDirectory(), simhome.NewStore(), &simLookup{}
	p := newProc(t, "a-blue", dir, store, lookup, 800*time.Millisecond, nil)
	_, _, slug := simhome.Creds(3)
	lookup.saw(slug, time.Now().Add(-30*time.Second))

	back := make(chan *simhome.Home, 1)
	go func() {
		time.Sleep(400 * time.Millisecond)
		back <- simhome.Start(3, p.endpoint(), api.URL, simhome.Options{})
	}()
	start := time.Now()
	resp := getVia(t, p.public, slug, "/api/health", nil)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	took := time.Since(start)
	if resp.StatusCode != 200 || !strings.HasPrefix(string(b), "ok ") {
		t.Fatalf("held request: %d %q", resp.StatusCode, b)
	}
	if took < 350*time.Millisecond {
		t.Errorf("answered after %s, before the home was back", took)
	}
	(<-back).Stop()

	_, _, gone := simhome.Creds(4)
	lookup.saw(gone, time.Now().Add(-30*time.Second))
	start = time.Now()
	resp = getVia(t, p.public, gone, "/api/health", map[string]string{"Accept": "text/html", "Accept-Language": "ar,en;q=0.5"})
	b, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if took := time.Since(start); took < 750*time.Millisecond {
		t.Errorf("gave up after %s, before the 800 ms hold", took)
	}
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" {
		t.Errorf("never back: %d Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if !strings.Contains(string(b), `dir="rtl"`) || !strings.Contains(string(b), "الجهاز الرئيسي يعيد الاتصال") {
		t.Errorf("Arabic page:\n%s", b)
	}
	resp = getVia(t, p.public, gone, "/api/health", nil)
	b, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(b), `"home_reconnecting"`) {
		t.Errorf("JSON: %s", b)
	}

	_, _, old := simhome.Creds(5)
	lookup.saw(old, time.Now().Add(-10*time.Minute))
	start = time.Now()
	resp = getVia(t, p.public, old, "/api/health", nil)
	b, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "server_offline") || time.Since(start) > 200*time.Millisecond {
		t.Errorf("offline 10 min: %d %s after %s", resp.StatusCode, b, time.Since(start))
	}
}

// T07: past 20 held requests for one home, the next gets 503 at once.
func TestHoldLimits(t *testing.T) {
	dir, store, lookup := relaypkg.NewMemDirectory(), simhome.NewStore(), &simLookup{}
	holds := &Holds{PerHome: 20, Total: 5000}
	p := newProc(t, "a-blue", dir, store, lookup, 2*time.Second, holds)
	_, _, slug := simhome.Creds(6)
	lookup.saw(slug, time.Now())
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := getVia(t, p.public, slug, "/api/health", nil)
			_ = resp.Body.Close()
		}()
	}
	waitOn(t, "20 held", func() bool { return holds.Held() == 20 })
	start := time.Now()
	resp := getVia(t, p.public, slug, "/api/health", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || time.Since(start) > 300*time.Millisecond {
		t.Errorf("the 21st: %d after %s, want 503 at once", resp.StatusCode, time.Since(start))
	}
	wg.Wait()
}

// T08: blue dies (its rows go with its heartbeat): green no longer forwards
// there and holds; a home back on green is served.
func TestADeadPeerIsNotForwardedTo(t *testing.T) {
	api := simhome.API()
	defer api.Close()
	dir, store, lookup := relaypkg.NewMemDirectory(), simhome.NewStore(), &simLookup{}
	blue := newProc(t, "a-blue", dir, store, lookup, 3*time.Second, nil)
	green := newProc(t, "a-green", dir, store, lookup, 3*time.Second, nil)
	h := simhome.Start(7, blue.endpoint(), api.URL, simhome.Options{})
	waitOn(t, "H on blue", func() bool { _, ok := dir.Row(h.Slug); return ok })
	lookup.saw(h.Slug, time.Now())
	h.Stop()
	blue.peer.Close() // blue crashed: its peer port is gone, the directory still says blue
	back := make(chan *simhome.Home, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)
		dir.Drop("a-blue") // its heartbeat aged out
		back <- simhome.Start(7, green.endpoint(), api.URL, simhome.Options{})
	}()
	resp := getVia(t, green.public, h.Slug, "/api/health", nil)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("after blue died: %d %s", resp.StatusCode, b)
	}
	(<-back).Stop()
}

// T09: the relay's pages pick Arabic from Accept-Language, right to left.
func TestRelayPageLanguage(t *testing.T) {
	for _, c := range []struct{ accept, lang string }{
		{"ar", "ar"}, {"ar-EG,en;q=0.8", "ar"}, {"en-US,ar;q=0.9", "en"}, {"fr", "en"}, {"", "en"}, {"en;q=0.3, ar;q=0.7", "ar"}, {"ar, en", "ar"}, {"en, ar", "en"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", c.accept)
		if got := pageLang(r); got != c.lang {
			t.Errorf("%q: %s, want %s", c.accept, got, c.lang)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Sec-Fetch-Dest", "document")
	r.Header.Set("Accept-Language", "ar")
	for _, kind := range []string{"server_offline", "home_reconnecting", "tier_limit", "no_server"} {
		rec := httptest.NewRecorder()
		writeErr(rec, r, http.StatusServiceUnavailable, kind, "")
		body := rec.Body.String()
		if !strings.Contains(body, `lang="ar" dir="rtl"`) {
			t.Errorf("%s: not an Arabic page", kind)
		}
		h, _, _ := pageText(kind, "ar")
		if !strings.Contains(body, h) || h == "" {
			t.Errorf("%s: heading %q missing", kind, h)
		}
	}
	// every English word has its Arabic
	for k := range pageTexts["en"] {
		if pageTexts["ar"][k].heading == "" {
			t.Errorf("no Arabic for %s", k)
		}
	}
}

// The forwarding and holding counters render with their labels.
func TestForwardMetrics(t *testing.T) {
	var b strings.Builder
	if err := WriteMetrics(&b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"maktaba_relay_forwarded_total ", "maktaba_relay_forwarded_bytes_total ",
		`maktaba_relay_held_total{result="timeout"}`, `maktaba_relay_held_total{result="found"}`, "maktaba_relay_peer_auth_failed_total "} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
}
