package main

// Epic 48 story 48.3 (gap G1): a channel's stream on the web port follows
// library access, as the guide does. The fake API (serve_proxy_test.go's
// fakeAPI) answers GET /api/channels/{id} with the channels handler's canRead.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Role tests: no credentials 401; a viewer without the channel's library 403
// (and the same for no such channel); a viewer with it 200; a multi-library
// channel 200 for any viewer; an all-library viewer and an admin 200. Nothing
// refused reaches streaming.
func TestStreamGateChannelFollowsLibraryAccess(t *testing.T) {
	mux, be, _, _ := webMux(t)
	const unknown = "0a1b2c3d-0000-4000-8000-00000000dddd"
	admin := map[string]string{"Cookie": "mkt_sess=admin"}
	allLibs := map[string]string{"Cookie": "mkt_sess=good"}
	allLibsBearer := map[string]string{"Authorization": "Bearer good"}
	libA := map[string]string{"Cookie": "mkt_sess=lib-a"}
	cases := []struct {
		name string
		hdr  map[string]string
		ch   string
		want int
	}{
		{"signed out", nil, chanID, http.StatusUnauthorized},
		{"signed out, multi-library", nil, chanMulti, http.StatusUnauthorized},
		{"forged session", map[string]string{"Cookie": "mkt_sess=forged"}, chanID, http.StatusUnauthorized},
		{"admin, library A", admin, chanID, 200},
		{"admin, library B", admin, chanB, 200},
		{"admin, multi-library", admin, chanMulti, 200},
		{"all libraries, library A", allLibs, chanID, 200},
		{"all libraries, library B", allLibs, chanB, 200},
		{"all libraries (bearer), library B", allLibsBearer, chanB, 200},
		{"all libraries, multi-library", allLibs, chanMulti, 200},
		{"library A viewer, library A", libA, chanID, 200},
		{"library A viewer, multi-library", libA, chanMulti, 200},
		{"library A viewer, library B", libA, chanB, http.StatusForbidden},
		{"library A viewer, no such channel", libA, unknown, http.StatusForbidden},
		{"all libraries, no such channel", allLibs, unknown, http.StatusForbidden},
	}
	served := 0
	for _, c := range cases {
		for _, file := range []string{"index.m3u8", "master.m3u8", "seg-7.ts", "s/blue/seg-1760000000.ts", "live.ts"} {
			path := "/stream/channel/" + c.ch + "/" + file
			rec := get(mux, path, c.hdr)
			if rec.Code != c.want {
				t.Errorf("%s %s: %d, want %d", c.name, file, rec.Code, c.want)
			}
			if c.want == 200 {
				served++
				if rec.Body.String() != "ok:"+path {
					t.Errorf("%s %s: body %q", c.name, file, rec.Body.String())
				}
			}
		}
	}
	// an upper-case id is the same channel: the check is not dodged by case
	if rec := get(mux, "/stream/channel/"+strings.ToUpper(chanB)+"/index.m3u8", libA); rec.Code != http.StatusForbidden {
		t.Errorf("upper-case library B id: %d, want 403", rec.Code)
	}
	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.paths) != served {
		t.Errorf("streaming saw %d requests, want the %d allowed: %v", len(be.paths), served, be.paths)
	}
	for i := range be.paths {
		if strings.Contains(strings.ToLower(be.paths[i]), chanB) && be.viewers[i] == "v" {
			t.Errorf("the library A viewer reached library B's stream: %s", be.paths[i])
		}
	}
}

// An allowed channel's segments cost no extra API call within the TTL (one
// /api/auth/me and one /api/channels/{id} per viewer and channel); a refusal
// is cached too; an admin costs no channel check; both are asked again after
// the TTL.
func TestStreamGateCachesChannelVerdicts(t *testing.T) {
	mux, _, api, gate := webMux(t)
	now := time.Unix(1_700_000_000, 0)
	gate.now = func() time.Time { return now }
	libA := map[string]string{"Cookie": "mkt_sess=lib-a"}
	for i := 0; i < 20; i++ {
		if rec := get(mux, fmt.Sprintf("/stream/channel/%s/seg-%d.ts", chanID, i), libA); rec.Code != 200 {
			t.Fatalf("segment %d: %d", i, rec.Code)
		}
		if rec := get(mux, "/stream/channel/"+chanB+"/index.m3u8", libA); rec.Code != http.StatusForbidden {
			t.Fatalf("library B %d: %d", i, rec.Code)
		}
	}
	counts := func() (int, int) {
		api.mu.Lock()
		defer api.mu.Unlock()
		return api.calls, api.chanCalls
	}
	if me, ch := counts(); me != 1 || ch != 2 {
		t.Fatalf("API asked /api/auth/me %d and /api/channels %d times, want 1 and 2 (one per channel)", me, ch)
	}
	// another viewer is asked for on their own: the cache is per credential
	if rec := get(mux, "/stream/channel/"+chanB+"/index.m3u8", map[string]string{"Cookie": "mkt_sess=good"}); rec.Code != 200 {
		t.Fatalf("all-library viewer after library A's refusal of library B: %d", rec.Code)
	}
	if _, ch := counts(); ch != 3 {
		t.Fatalf("channel checks %d, want 3", ch)
	}
	for i := 0; i < 3; i++ {
		if rec := get(mux, "/stream/channel/"+chanB+"/seg-1.ts", map[string]string{"Cookie": "mkt_sess=admin"}); rec.Code != 200 {
			t.Fatalf("admin: %d", rec.Code)
		}
	}
	if _, ch := counts(); ch != 3 {
		t.Fatalf("an admin's fetches asked the channel check (%d)", ch)
	}
	now = now.Add(streamAuthTTL + time.Second)
	get(mux, "/stream/channel/"+chanID+"/seg-99.ts", libA)
	if me, ch := counts(); me != 4 || ch != 4 {
		t.Fatalf("after the TTL: /api/auth/me %d, /api/channels %d, want 4 and 4", me, ch)
	}
}

// Video sessions and direct play are not channels: no channel check, as before.
func TestStreamGateVideoSessionsSkipTheChannelCheck(t *testing.T) {
	mux, _, api, _ := webMux(t)
	libA := map[string]string{"Cookie": "mkt_sess=lib-a"}
	for _, path := range []string{
		"/stream/" + sessID + "/manifest.m3u8",
		"/stream/" + sessID + "/v0/seg-3.ts",
		"/stream/" + sessID + "/subs/auto.vtt",
		"/stream/direct/" + sessID,
	} {
		if rec := get(mux, path, libA); rec.Code != 200 || rec.Body.String() != "ok:"+path {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.chanCalls != 0 {
		t.Fatalf("video paths asked the channel check %d times", api.chanCalls)
	}
}

// The staging preview stays admins-only and needs no channel check; a
// library-limited viewer still gets the staging 403.
func TestStagingPreviewUnchangedByTheChannelCheck(t *testing.T) {
	mux, _, api, _ := webMux(t)
	pl := "/stream/staging/channel/" + chanB + "/index.m3u8"
	if rec := get(mux, pl+"?pt=x", map[string]string{"Cookie": "mkt_sess=admin"}); rec.Code != 200 {
		t.Fatalf("admin staging: %d", rec.Code)
	}
	if rec := get(mux, pl, map[string]string{"Cookie": "mkt_sess=lib-a"}); rec.Code != http.StatusForbidden {
		t.Fatalf("library A viewer staging: %d", rec.Code)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.chanCalls != 0 {
		t.Fatalf("staging asked the channel check %d times", api.chanCalls)
	}
}

// The channel check not answering (the API's database down) is a 502 like the
// sign-in check's, never a 401 or a cached refusal; once it answers, the
// viewer plays.
func TestStreamGateChannelCheckDownIsBadGateway(t *testing.T) {
	mux, be, api, _ := webMux(t)
	libA := map[string]string{"Cookie": "mkt_sess=lib-a"}
	path := "/stream/channel/" + chanID + "/index.m3u8"
	api.mu.Lock()
	api.chanDown = true
	api.mu.Unlock()
	if rec := get(mux, path, libA); rec.Code != http.StatusBadGateway {
		t.Fatalf("channel check down: %d, want 502", rec.Code)
	}
	be.mu.Lock()
	hits := len(be.paths)
	be.mu.Unlock()
	if hits != 0 {
		t.Fatal("streamed without a channel verdict")
	}
	api.mu.Lock()
	api.chanDown = false
	api.mu.Unlock()
	if rec := get(mux, path, libA); rec.Code != 200 {
		t.Fatalf("after the API is back: %d (a failure was cached?)", rec.Code)
	}
}

// The channel check's 401 (signed out between the two checks) is a 401, and
// the cached session is dropped so the next fetch asks again.
func TestStreamGateChannelCheckSignedOut(t *testing.T) {
	be := &streamBackend{}
	bs := be.server(t)
	bu, _ := url.Parse(bs.URL)
	var mu sync.Mutex
	meCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			mu.Lock()
			meCalls++
			mu.Unlock()
			_, _ = io.WriteString(w, `{"user_id":"v"}`)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(api.Close)
	au, _ := url.Parse(api.URL)
	gate := newStreamGate(newStreamProxy("0.0.0.0:"+bu.Port()), au)
	hdr := map[string]string{"Cookie": "mkt_sess=lib-a"}
	for i := 0; i < 2; i++ {
		if rec := get(gate, "/stream/channel/"+chanID+"/index.m3u8", hdr); rec.Code != http.StatusUnauthorized {
			t.Fatalf("fetch %d: %d, want 401", i, rec.Code)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if meCalls != 2 {
		t.Fatalf("the session was kept after the channel check's 401 (/api/auth/me asked %d times)", meCalls)
	}
	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.paths) != 0 {
		t.Fatalf("streamed: %v", be.paths)
	}
}

// On a node, /stream/via-home/channel/{id}/… is the same channel: the same check.
func TestStreamGateViaHomeChannelChecked(t *testing.T) {
	mux, _, _, gate := webMux(t)
	gate.allow = func(p string) bool {
		return allowedStreamPath(p) || strings.HasPrefix(p, "/stream/via-home/channel/")
	}
	libA := map[string]string{"Cookie": "mkt_sess=lib-a"}
	if rec := get(mux, "/stream/via-home/channel/"+chanB+"/index.m3u8", libA); rec.Code != http.StatusForbidden {
		t.Fatalf("via-home library B: %d, want 403", rec.Code)
	}
	if rec := get(mux, "/stream/via-home/channel/"+chanID+"/index.m3u8", libA); rec.Code != 200 {
		t.Fatalf("via-home library A: %d, want 200", rec.Code)
	}
}

func TestStreamChannel(t *testing.T) {
	for path, want := range map[string]string{
		"/stream/channel/" + chanID + "/index.m3u8":                chanID,
		"/stream/channel/" + strings.ToUpper(chanID) + "/seg-1.ts": chanID,
		"/stream/via-home/channel/" + chanID + "/s/blue/seg-1.ts":  chanID,
		"/stream/" + sessID + "/manifest.m3u8":                     "",
		"/stream/direct/" + sessID:                                 "",
		"/stream/staging/channel/" + chanID + "/index.m3u8":        "",
		"/stream/channelx/" + chanID + "/index.m3u8":               "",
	} {
		if got := streamChannel(path); got != want {
			t.Errorf("streamChannel(%q) = %q, want %q", path, got, want)
		}
	}
}
