package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

const (
	chanID = "0a1b2c3d-0000-4000-8000-00000000abcd"
	sessID = "11111111-2222-4333-8444-555555555555"
)

// streamBackend records what reaches the streaming service.
type streamBackend struct {
	mu      sync.Mutex
	paths   []string
	cookies []string
	authz   []string
}

func (b *streamBackend) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.paths = append(b.paths, r.URL.Path)
		b.cookies = append(b.cookies, r.Header.Get("Cookie"))
		b.authz = append(b.authz, r.Header.Get("Authorization"))
		b.mu.Unlock()
		_, _ = io.WriteString(w, "ok:"+r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeAPI answers /api/auth/me: 200 for the "good" session.
type fakeAPI struct {
	mu    sync.Mutex
	calls int
}

func (a *fakeAPI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.calls++
		a.mu.Unlock()
		if r.URL.Path != "/api/auth/me" {
			http.NotFound(w, r)
			return
		}
		if c, err := r.Cookie("mkt_sess"); err == nil && c.Value == "admin" {
			_, _ = io.WriteString(w, `{"user_id":"a","is_admin":true}`)
			return
		}
		if c, err := r.Cookie("mkt_sess"); err == nil && c.Value == "good" {
			_, _ = io.WriteString(w, `{"user_id":"u"}`)
			return
		}
		if r.Header.Get("Authorization") == "Bearer good" {
			_, _ = io.WriteString(w, `{"user_id":"u"}`)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// webMux is the web port's live-TV wiring (as startWebServer builds it).
func webMux(t *testing.T) (*http.ServeMux, *streamBackend, *fakeAPI, *streamGate) {
	t.Helper()
	be, api := &streamBackend{}, &fakeAPI{}
	bs, as := be.server(t), api.server(t)
	bu, _ := url.Parse(bs.URL)
	au, _ := url.Parse(as.URL)
	mux := http.NewServeMux()
	proxy := newStreamProxy("0.0.0.0:" + bu.Port())
	for _, p := range publicStreamPaths {
		mux.Handle(p, proxy)
	}
	gate := newStreamGate(proxy, au)
	mux.Handle("/stream/", gate)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "spa") })
	return mux, be, api, gate
}

func get(h http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var signedIn = map[string]string{"Cookie": "mkt_sess=good; mkt_csrf=x", "Authorization": "Bearer good"}

func TestStreamProxyForwardsLiveTVPaths(t *testing.T) {
	mux, be, _, _ := webMux(t)
	paths := []string{
		"/stream/channel/" + chanID + "/index.m3u8",
		"/stream/channel/" + chanID + "/master.m3u8",
		"/stream/channel/" + chanID + "/manifest.m3u8",
		"/stream/channel/" + chanID + "/seg-12.ts",
		"/stream/channel/" + chanID + "/live.ts",
		"/stream/" + sessID + "/manifest.m3u8",
		"/stream/" + sessID + "/v0/index.m3u8",
		"/stream/" + sessID + "/v0/seg-3.ts",
		"/stream/" + sessID + "/subs/auto.vtt",
		"/stream/direct/" + sessID,
	}
	for _, path := range paths {
		if rec := get(mux, path, signedIn); rec.Code != 200 || rec.Body.String() != "ok:"+path {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
	// the guide and lineup stay public
	for _, path := range []string{"/xmltv.xml", "/lineup.m3u"} {
		if rec := get(mux, path, nil); rec.Code != 200 || rec.Body.String() != "ok:"+path {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
	if rec := get(mux, "/guide", nil); rec.Body.String() != "spa" {
		t.Errorf("/guide should stay on the SPA, got %q", rec.Body.String())
	}
	// a public path with a session cookie: the cookie still never reaches streaming
	get(mux, "/xmltv.xml", signedIn)
	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.paths) != len(paths)+3 {
		t.Errorf("backend saw %v", be.paths)
	}
	for i := range be.paths {
		if be.cookies[i] != "" || be.authz[i] != "" {
			t.Errorf("%s reached streaming with Cookie %q / Authorization %q", be.paths[i], be.cookies[i], be.authz[i])
		}
	}
}

func TestStreamGateNeedsASignedInSession(t *testing.T) {
	mux, be, _, _ := webMux(t)
	path := "/stream/channel/" + chanID + "/index.m3u8"
	for name, hdr := range map[string]map[string]string{
		"anonymous":    nil,
		"bad cookie":   {"Cookie": "mkt_sess=forged"},
		"bad bearer":   {"Authorization": "Bearer forged"},
		"other cookie": {"Cookie": "theme=dark"},
	} {
		if rec := get(mux, path, hdr); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := get(mux, path, map[string]string{"Cookie": "mkt_sess=good"}); rec.Code != 200 {
		t.Errorf("signed in: %d", rec.Code)
	}
	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.paths) != 1 {
		t.Errorf("unauthenticated requests reached streaming: %v", be.paths)
	}
}

func TestStreamGateAllowList(t *testing.T) {
	mux, be, _, _ := webMux(t)
	for _, path := range []string{
		"/stream/",
		"/stream/channel/not-a-uuid/index.m3u8",
		"/stream/channel/" + chanID + "/secret.txt",
		"/stream/channel/" + chanID + "/seg-1.ts.tmp",
		"/stream/channel/" + chanID + "/sub/index.m3u8",
		"/stream/posters/" + sessID,
		"/stream/healthz", "/stream/metrics",
	} {
		if rec := get(mux, path, signedIn); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
	// a path that climbs out never reaches streaming (the mux redirects
	// the cleaned path, the gate refuses what is left)
	for _, path := range []string{"/stream/channel/" + chanID + "/../../admin", "/stream/" + sessID + "/v0/../../x"} {
		if rec := get(mux, path, signedIn); rec.Code == http.StatusOK {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	req := httptest.NewRequest("POST", "/stream/channel/"+chanID+"/index.m3u8", nil)
	req.Header.Set("Cookie", "mkt_sess=good")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.paths) != 0 {
		t.Errorf("backend saw %v", be.paths)
	}
}

// TestStreamGateSlotSegments: with TV slots the front door's stitched playlist
// lists segments as s/{slot}/seg-N.ts (relative to the channel's playlist), so
// the browser fetches /stream/channel/{id}/s/{slot}/seg-N.ts through the web
// port: the gate must let those through (and nothing else under s/).
func TestStreamGateSlotSegments(t *testing.T) {
	mux, _, _, _ := webMux(t)
	for _, name := range []string{"blue", "green", "amber"} {
		path := "/stream/channel/" + chanID + "/s/" + name + "/seg-1760000000.ts"
		if rec := get(mux, path, signedIn); rec.Code != 200 || rec.Body.String() != "ok:"+path {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{
		"/stream/channel/" + chanID + "/s/purple/seg-1.ts",
		"/stream/channel/" + chanID + "/s/blue/index.m3u8",
		"/stream/channel/" + chanID + "/s/blue/seg-1.ts.tmp",
		"/stream/channel/" + chanID + "/s/blue/x/seg-1.ts",
	} {
		if rec := get(mux, path, signedIn); rec.Code == http.StatusOK {
			t.Errorf("%s: served, want refused", path)
		}
	}
	if rec := get(mux, "/stream/channel/"+chanID+"/s/blue/seg-1.ts", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", rec.Code)
	}
}

func TestStreamGateCachesAcceptedSessions(t *testing.T) {
	mux, _, api, gate := webMux(t)
	now := time.Unix(1_700_000_000, 0)
	gate.now = func() time.Time { return now }
	seg := "/stream/channel/" + chanID + "/seg-1.ts"
	for i := 0; i < 5; i++ {
		if rec := get(mux, seg, map[string]string{"Cookie": "mkt_sess=good"}); rec.Code != 200 {
			t.Fatalf("fetch %d: %d", i, rec.Code)
		}
	}
	api.mu.Lock()
	calls := api.calls
	api.mu.Unlock()
	if calls != 1 {
		t.Fatalf("API asked %d times, want once", calls)
	}
	now = now.Add(streamAuthTTL + time.Second) // re-checked after the TTL
	get(mux, seg, map[string]string{"Cookie": "mkt_sess=good"})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.calls != 2 {
		t.Fatalf("API asked %d times after the TTL, want 2", api.calls)
	}
}

// the API down is not the viewer's sign-in: a 502 (nothing streamed), not a 401 that
// sends them to sign in again.
func TestStreamGateAPIDownIsBadGateway(t *testing.T) {
	be := &streamBackend{}
	bs := be.server(t)
	bu, _ := url.Parse(bs.URL)
	dead, _ := url.Parse("http://127.0.0.1:1")
	gate := newStreamGate(newStreamProxy("0.0.0.0:"+bu.Port()), dead)
	if rec := get(gate, "/stream/channel/"+chanID+"/index.m3u8", signedIn); rec.Code != http.StatusBadGateway {
		t.Fatalf("code %d", rec.Code)
	}
	be.mu.Lock()
	hits := len(be.paths)
	be.mu.Unlock()
	if hits != 0 {
		t.Fatal("streamed without a sign-in check")
	}
}

func TestStreamProxyDownIsBadGateway(t *testing.T) {
	proxy := newStreamProxy("127.0.0.1:1") // nothing listens
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/xmltv.xml", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code %d", rec.Code)
	}
}

// TestStagingGateAdminOnly is story 33.6 TC2: the staging preview on the web port
// needs an admin's session (a signed-in viewer gets 403, nobody else 401), and
// only the preview paths are let through.
func TestStagingGateAdminOnly(t *testing.T) {
	mux, be, _, _ := webMux(t)
	admin := map[string]string{"Cookie": "mkt_sess=admin"}
	pl := "/stream/staging/channel/" + chanID + "/index.m3u8"
	seg := "/stream/staging/channel/" + chanID + "/s/amber/seg-17.ts"
	for _, path := range []string{pl, seg} {
		if rec := get(mux, path+"?pt=x", admin); rec.Code != 200 || rec.Body.String() != "ok:"+path {
			t.Errorf("admin %s: %d %q", path, rec.Code, rec.Body.String())
		}
		if rec := get(mux, path, signedIn); rec.Code != http.StatusForbidden {
			t.Errorf("non-admin %s: %d, want 403", path, rec.Code)
		}
		if rec := get(mux, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s: %d, want 401", path, rec.Code)
		}
	}
	for _, path := range []string{
		"/stream/staging/channel/" + chanID + "/live.ts",
		"/stream/staging/channel/" + chanID + "/s/purple/seg-1.ts",
		"/stream/staging/channel/" + chanID + "/s/amber/../seg-1.ts",
		"/stream/staging/lineup.m3u",
	} {
		if rec := get(mux, path, admin); rec.Code == http.StatusOK {
			t.Errorf("%s: served (%q), want refused", path, rec.Body.String())
		}
	}
	be.mu.Lock()
	defer be.mu.Unlock()
	for i := range be.paths {
		if be.cookies[i] != "" {
			t.Errorf("%s reached streaming with the session cookie", be.paths[i])
		}
	}
}
