package main

// Epic 48 story 48.12 (gap G2): an AirPlay or Cast receiver fetches the signed
// URL itself, with no cookie and no bearer token. A video session's paths and
// direct play pass the web port's gate on the URL's ?sig= alone; the streaming
// service still verifies the token in full. Channel paths still need a session.
//
// The fourth role rule, "a viewer's sig for a video outside their libraries
// cannot be minted", is the session open's: POST /api/stream/sessions refuses
// a video of a library the viewer may not read before any URL is signed
// (api/internal/handlers/streaming TestOfflinePlaybackKeepsLibraryAccess), and
// a token whose lib[] does not cover the resource is refused by the streaming
// service's LibraryGuard (streaming/internal/auth TestLibraryGuard_RejectsForeign).

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	videoID    = "22222222-3333-4444-8555-666666666666"
	otherSess  = "99999999-2222-4333-8444-555555555555"
	signedLib  = "0a1b2c3d-0000-4000-8000-0000000000aa"
	signedUser = "u-viewer"
)

var signedNow = time.Unix(1_800_000_000, 0)

// fakeToken is a minted token's shape (api/internal/auth/signedurl): the
// gate reads its claims only, so the signature part is a stand-in; the
// streaming service is the one that checks it.
func fakeToken(t *testing.T, aud, sub string, exp time.Time) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := enc(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	body := enc(map[string]any{
		"iss": "maktaba", "aud": aud, "sub": sub, "iat": exp.Add(-time.Hour).Unix(), "exp": exp.Unix(),
		"usr": signedUser, "lib": []string{signedLib},
	})
	return head + "." + body + ".c2lnbmF0dXJl"
}

// signedBackend records what reaches the streaming service, query included.
type signedBackend struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (b *signedBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.reqs = append(b.reqs, r)
	b.mu.Unlock()
	_, _ = io.WriteString(w, "ok:"+r.URL.Path)
}

func (b *signedBackend) seen() []*http.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*http.Request(nil), b.reqs...)
}

// signedGate is the web port's gate over a recording backend, at signedNow.
func signedGate(t *testing.T, allow func(string) bool) (*streamGate, *signedBackend, *fakeAPI) {
	t.Helper()
	be, api := &signedBackend{}, &fakeAPI{}
	au, _ := url.Parse(api.server(t).URL)
	g := newStreamGate(be, au)
	g.now = func() time.Time { return signedNow }
	if allow != nil {
		g.allow = allow
	}
	return g, be, api
}

func withSig(path, sig string) string {
	return path + "?sig=" + url.QueryEscape(sig)
}

func do(h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A session's every path and direct play pass on the signed URL alone, with the
// token passed on untouched for the streaming service to verify, no credentials
// and no viewer (one the client sent is dropped). No session check is made.
func TestStreamGateSignedURLPassesWithoutASession(t *testing.T) {
	g, be, api := signedGate(t, nil)
	exp := signedNow.Add(4 * time.Hour)
	sessTok := fakeToken(t, audSession, sessID, exp)
	directTok := fakeToken(t, audDirect, videoID, exp)
	type want struct{ method, target, sig string }
	var cases []want
	for _, file := range []string{
		"manifest.m3u8", "manifest.mpd", "chapters.json", "subs/auto.vtt", "subs/sidecar/en.vtt",
		"v0/index.m3u8", "v0/seg-3.ts", "subs-en/index.m3u8",
	} {
		cases = append(cases, want{http.MethodGet, withSig("/stream/"+sessID+"/"+file, sessTok), sessTok})
	}
	cases = append(cases,
		want{http.MethodGet, withSig("/stream/direct/"+videoID, directTok), directTok},
		want{http.MethodHead, withSig("/stream/direct/"+videoID, directTok), directTok},
	)
	for _, c := range cases {
		rec := do(g, c.method, c.target, map[string]string{viewerHeader: "someone-else"})
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s: %d %q", c.method, c.target, rec.Code, rec.Body.String())
		}
	}
	seen := be.seen()
	if len(seen) != len(cases) {
		t.Fatalf("streaming saw %d requests, want %d", len(seen), len(cases))
	}
	for i, r := range seen {
		if got := r.URL.Query().Get("sig"); got != cases[i].sig {
			t.Errorf("%s: the token reached streaming as %q", r.URL.Path, got)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get(viewerHeader) != "" {
			t.Errorf("%s reached streaming with credentials or a viewer: %v", r.URL.Path, r.Header)
		}
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.calls != 0 || api.chanCalls != 0 {
		t.Errorf("a signed request asked the API: %d auth, %d channel", api.calls, api.chanCalls)
	}
}

// Role tests: no session and no sig 401; another session's (or another video's)
// sig 403; a token for the other path family 403; something that is not a token
// 401; a channel's path or the staging preview with any sig 401 (a session is
// needed). Nothing refused reaches streaming. An expired sig is the streaming
// service's 403: TestStreamGateLeavesExpiryToStreaming.
func TestStreamGateSignedURLRoles(t *testing.T) {
	g, be, _ := signedGate(t, nil)
	future, past := signedNow.Add(time.Hour), signedNow.Add(-2*time.Hour)
	manifest := "/stream/" + sessID + "/manifest.m3u8"
	seg := "/stream/" + sessID + "/v0/seg-1.ts"
	direct := "/stream/direct/" + videoID
	chanIndex := "/stream/channel/" + chanID + "/index.m3u8"
	staging := "/stream/staging/channel/" + chanID + "/index.m3u8"
	sessTok := fakeToken(t, audSession, sessID, future)
	cases := []struct {
		name   string
		target string
		want   int
	}{
		{"no session, no sig: session", manifest, http.StatusUnauthorized},
		{"no session, no sig: segment", seg, http.StatusUnauthorized},
		{"no session, no sig: direct", direct, http.StatusUnauthorized},
		{"empty sig", manifest + "?sig=", http.StatusUnauthorized},
		{"another session's sig", withSig(manifest, fakeToken(t, audSession, otherSess, future)), http.StatusForbidden},
		{"another session's sig: segment", withSig(seg, fakeToken(t, audSession, otherSess, future)), http.StatusForbidden},
		{"another video's sig", withSig(direct, fakeToken(t, audDirect, otherSess, future)), http.StatusForbidden},
		{"another session's sig, also expired", withSig(manifest, fakeToken(t, audSession, otherSess, past)), http.StatusForbidden},
		{"direct token on a session path", withSig(manifest, fakeToken(t, audDirect, sessID, future)), http.StatusForbidden},
		{"session token on direct play", withSig(direct, fakeToken(t, audSession, videoID, future)), http.StatusForbidden},
		{"another audience", withSig(manifest, fakeToken(t, "streaming-static", sessID, future)), http.StatusForbidden},
		{"not a token", withSig(manifest, "letmein"), http.StatusUnauthorized},
		{"not base64", withSig(manifest, "a.!!!.c"), http.StatusUnauthorized},
		{"not JSON", withSig(manifest, "a."+base64.RawURLEncoding.EncodeToString([]byte("nope"))+".c"), http.StatusUnauthorized},
		{"too long", withSig(manifest, sessTok+strings.Repeat("A", signedTokenMax)), http.StatusUnauthorized},
		{"a channel with a sig", withSig(chanIndex, sessTok), http.StatusUnauthorized},
		{"a channel with a sig for it", withSig(chanIndex, fakeToken(t, audSession, chanID, future)), http.StatusUnauthorized},
		{"a channel segment with a sig", withSig("/stream/channel/"+chanID+"/seg-7.ts", sessTok), http.StatusUnauthorized},
		{"staging with a sig", withSig(staging, fakeToken(t, audSession, chanID, future)), http.StatusUnauthorized},
		{"a path off the allow-list with a sig", withSig("/stream/posters/"+videoID, fakeToken(t, audDirect, videoID, future)), http.StatusNotFound},
		{"a session file off the allow-list", withSig("/stream/"+sessID+"/secret.txt", sessTok), http.StatusNotFound},
	}
	for _, c := range cases {
		if rec := do(g, http.MethodGet, c.target, nil); rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%q)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	if seen := be.seen(); len(seen) != 0 {
		for _, r := range seen {
			t.Errorf("refused request reached streaming: %s", r.URL)
		}
	}
}

// An expired sig is the streaming service's 403 (Story 23.2 AC-3, proved in
// streaming/internal/auth TestSignedURL_Expired and httpx
// TestWriteSignedURLError_ExpiredIs403), with its own configurable leeway
// (MAKTABA_CLOCK_SKEW_LEEWAY_SEC; for via-home, the home's). The gate has no
// second, differently-tuned expiry check: whatever the token's age, it passes
// the token untouched and relays the streaming service's answer. The stand-in
// here applies the streaming rule (now - leeway > exp, in Unix seconds) with a
// non-default 120 s leeway.
func TestStreamGateLeavesExpiryToStreaming(t *testing.T) {
	const leeway = 120 * time.Second
	streamNow := signedNow
	var mu sync.Mutex
	var forwarded []string
	streaming := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig := r.URL.Query().Get("sig")
		mu.Lock()
		forwarded = append(forwarded, sig)
		mu.Unlock()
		parts := strings.Split(sig, ".")
		raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c struct{ Exp int64 }
		_ = json.Unmarshal(raw, &c)
		if streamNow.Add(-leeway).Unix() > c.Exp {
			http.Error(w, `{"type":"signed-url-expired"}`, http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	api := &fakeAPI{}
	au, _ := url.Parse(api.server(t).URL)
	g := newStreamGate(streaming, au)
	g.now = func() time.Time { return signedNow }
	manifest := "/stream/" + sessID + "/manifest.m3u8"
	direct := "/stream/direct/" + videoID
	cases := []struct {
		name string
		path string
		aud  string
		sub  string
		age  time.Duration // how long ago the token expired
		want int
	}{
		{"live", manifest, audSession, sessID, -time.Hour, http.StatusOK},
		{"expired 30 s ago", manifest, audSession, sessID, 30 * time.Second, http.StatusOK},
		{"expired 90 s ago (past the 60 s default, within 120 s)", manifest, audSession, sessID, 90 * time.Second, http.StatusOK},
		{"expired exactly the leeway ago", manifest, audSession, sessID, leeway, http.StatusOK},
		{"expired the leeway and a second ago", manifest, audSession, sessID, leeway + time.Second, http.StatusForbidden},
		{"expired two hours ago", manifest, audSession, sessID, 2 * time.Hour, http.StatusForbidden},
		{"direct, expired two hours ago", direct, audDirect, videoID, 2 * time.Hour, http.StatusForbidden},
		{"direct, expired 90 s ago", direct, audDirect, videoID, 90 * time.Second, http.StatusOK},
	}
	for _, c := range cases {
		tok := fakeToken(t, c.aud, c.sub, signedNow.Add(-c.age))
		if rec := do(g, http.MethodGet, withSig(c.path, tok), nil); rec.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(forwarded) != len(cases) {
		t.Fatalf("the gate refused %d expired tokens itself", len(cases)-len(forwarded))
	}
}

// With a session the request takes the session's way as before (the viewer is
// named), sig or not; a bad session is still a 401 even with a good sig: the
// signed way is only for a request with no credentials at all.
func TestStreamGateSessionWinsOverSig(t *testing.T) {
	g, be, _ := signedGate(t, nil)
	tok := fakeToken(t, audSession, sessID, signedNow.Add(time.Hour))
	target := withSig("/stream/"+sessID+"/manifest.m3u8", tok)
	if rec := do(g, http.MethodGet, target, map[string]string{"Cookie": "mkt_sess=good"}); rec.Code != http.StatusOK {
		t.Fatalf("signed in with a sig: %d", rec.Code)
	}
	if rec := do(g, http.MethodGet, target, map[string]string{"Cookie": "mkt_sess=forged"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a forged session with a sig: %d, want 401", rec.Code)
	}
	seen := be.seen()
	if len(seen) != 1 || seen[0].Header.Get(viewerHeader) != "u" || seen[0].URL.Query().Get("sig") != tok {
		t.Fatalf("streaming saw %d requests, viewer %q", len(seen), func() string {
			if len(seen) == 0 {
				return ""
			}
			return seen[0].Header.Get(viewerHeader)
		}())
	}
}

// On a node the home's sessions come through /stream/via-home/: the same rule
// (a session or direct path with its sig passes; a channel still needs a session).
func TestStreamGateSignedURLViaHome(t *testing.T) {
	g, be, _ := signedGate(t, allowedNodeStreamPath)
	future := signedNow.Add(time.Hour)
	ok := []string{
		withSig("/stream/via-home/"+sessID+"/manifest.m3u8", fakeToken(t, audSession, sessID, future)),
		withSig("/stream/via-home/"+sessID+"/v0/seg-2.ts", fakeToken(t, audSession, sessID, future)),
		withSig("/stream/via-home/direct/"+videoID, fakeToken(t, audDirect, videoID, future)),
		// expired: the home's streaming service judges it with the home's leeway
		withSig("/stream/via-home/"+sessID+"/manifest.m3u8", fakeToken(t, audSession, sessID, signedNow.Add(-90*time.Second))),
	}
	for _, target := range ok {
		if rec := do(g, http.MethodGet, target, nil); rec.Code != http.StatusOK {
			t.Errorf("%s: %d", target, rec.Code)
		}
	}
	refused := map[string]int{
		withSig("/stream/via-home/"+sessID+"/manifest.m3u8", fakeToken(t, audSession, otherSess, future)):   http.StatusForbidden,
		withSig("/stream/via-home/channel/"+chanID+"/index.m3u8", fakeToken(t, audSession, chanID, future)): http.StatusUnauthorized,
	}
	for target, want := range refused {
		if rec := do(g, http.MethodGet, target, nil); rec.Code != want {
			t.Errorf("%s: %d, want %d", target, rec.Code, want)
		}
	}
	if n := len(be.seen()); n != len(ok) {
		t.Errorf("streaming saw %d requests, want %d", n, len(ok))
	}
	// on a home, via-home is not a path at all
	home, _, _ := signedGate(t, nil)
	if rec := do(home, http.MethodGet, ok[0], nil); rec.Code != http.StatusNotFound {
		t.Errorf("via-home on a home: %d, want 404", rec.Code)
	}
}
