package main

// Epic 57 story 57.10: what the web port's gate tells the streaming service
// about who plays a channel, and a channel's signed URL (the home only).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

const p57Viewer = "5a5a5a5a-1111-4222-8333-444444444444"

// p57Gate is a gate over a recording backend with an API that knows four
// sessions: an account's, an admin account's, the admin token's (an admin with
// no account) and one that names no one.
func p57Gate(t *testing.T) (*streamGate, *signedBackend) {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := r.Header.Get("Cookie")
		if r.URL.Path == "/api/auth/me" {
			switch who {
			case "s=viewer":
				_, _ = io.WriteString(w, `{"user_id":"`+p57Viewer+`","is_admin":false}`)
			case "s=admin":
				_, _ = io.WriteString(w, `{"user_id":"`+p57Viewer+`","is_admin":true}`)
			case "s=token":
				_, _ = io.WriteString(w, `{"is_admin":true}`)
			case "s=noone":
				_, _ = io.WriteString(w, `{}`)
			default:
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}
			return
		}
		_, _ = io.WriteString(w, `{}`) // GET /api/channels/{id}: readable
	}))
	t.Cleanup(api.Close)
	au, _ := url.Parse(api.URL)
	be := &signedBackend{}
	g := newStreamGate(be, au)
	g.now = func() time.Time { return signedNow }
	g.channelSig = true
	return g, be
}

// Role test: the gate names the viewer for an account, the admin token for an
// admin with no account, and refuses a session that names no one; what the
// client sent in either header never reaches the streaming service.
func TestStreamGateNamesWhoPlaysAChannel(t *testing.T) {
	path := "/stream/channel/" + chanID + "/index.m3u8"
	cases := []struct {
		name, cookie          string
		want                  int
		wantViewer, wantAdmin string
	}{
		{"no one", "", http.StatusUnauthorized, "", ""},
		{"an unknown session", "s=stranger", http.StatusUnauthorized, "", ""},
		{"a session with no account", "s=noone", http.StatusForbidden, "", ""},
		{"a viewer or child", "s=viewer", http.StatusOK, p57Viewer, ""},
		{"an admin account", "s=admin", http.StatusOK, p57Viewer, ""},
		{"the admin token", "s=token", http.StatusOK, "", "1"},
	}
	for _, c := range cases {
		g, be := p57Gate(t)
		hdr := map[string]string{viewerHeader: "00000000-0000-4000-8000-00000000beef", adminTokenHeader: "1"}
		if c.cookie != "" {
			hdr["Cookie"] = c.cookie
		}
		rec := do(g, http.MethodGet, path, hdr)
		if rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%q)", c.name, rec.Code, c.want, rec.Body.String())
			continue
		}
		seen := be.seen()
		if c.want != http.StatusOK {
			if len(seen) != 0 {
				t.Errorf("%s: refused, yet streamed", c.name)
			}
			continue
		}
		if len(seen) != 1 {
			t.Fatalf("%s: streaming saw %d requests", c.name, len(seen))
		}
		if got := seen[0].Header.Get(viewerHeader); got != c.wantViewer {
			t.Errorf("%s: viewer %q, want %q", c.name, got, c.wantViewer)
		}
		if got := seen[0].Header.Get(adminTokenHeader); got != c.wantAdmin {
			t.Errorf("%s: admin token header %q, want %q", c.name, got, c.wantAdmin)
		}
		if seen[0].Header.Get("Cookie") != "" {
			t.Errorf("%s: the session reached streaming", c.name)
		}
	}
}

// A video session is never given the admin token header's weight by a client.
func TestStreamGateDropsAClientAdminHeaderOnVideo(t *testing.T) {
	g, be := p57Gate(t)
	rec := do(g, http.MethodGet, "/stream/"+sessID+"/manifest.m3u8",
		map[string]string{"Cookie": "s=viewer", adminTokenHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
	if got := be.seen()[0].Header.Get(adminTokenHeader); got != "" {
		t.Fatalf("the client's admin token header reached streaming: %q", got)
	}
}

// At the home a channel's path passes on its own signed URL, with no viewer
// and no admin header (the streaming service reads the viewer from the token
// it verifies); another audience or another channel's token is a 403, something
// that is no token a 401.
func TestStreamGateChannelSignedURL(t *testing.T) {
	future := signedNow.Add(time.Hour)
	index := "/stream/channel/" + chanID + "/index.m3u8"
	good := fakeToken(t, audChannel, chanID, future)
	cases := []struct {
		name, target string
		want         int
	}{
		{"the channel's token", withSig(index, good), http.StatusOK},
		{"the channel's token: a segment", withSig("/stream/channel/"+chanID+"/seg-7.ts", good), http.StatusOK},
		{"the channel's token: live.ts", withSig("/stream/channel/"+chanID+"/live.ts", good), http.StatusOK},
		{"another channel's token", withSig(index, fakeToken(t, audChannel, chanB, future)), http.StatusForbidden},
		{"a session token", withSig(index, fakeToken(t, audSession, chanID, future)), http.StatusForbidden},
		{"a direct token", withSig(index, fakeToken(t, audDirect, chanID, future)), http.StatusForbidden},
		{"a channel token on a session path", withSig("/stream/"+chanID+"/manifest.m3u8", good), http.StatusForbidden},
		{"not a token", withSig(index, "letmein"), http.StatusUnauthorized},
		{"no sig", index, http.StatusUnauthorized},
		{"staging", withSig("/stream/staging/channel/"+chanID+"/index.m3u8", good), http.StatusUnauthorized},
	}
	for _, c := range cases {
		g, be := p57Gate(t)
		rec := do(g, http.MethodGet, c.target, map[string]string{viewerHeader: p57Viewer, adminTokenHeader: "1"})
		if rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%q)", c.name, rec.Code, c.want, rec.Body.String())
			continue
		}
		seen := be.seen()
		if c.want != http.StatusOK {
			if len(seen) != 0 {
				t.Errorf("%s: refused, yet streamed", c.name)
			}
			continue
		}
		if len(seen) != 1 || seen[0].URL.Query().Get("sig") != good {
			t.Fatalf("%s: the token did not reach streaming", c.name)
		}
		if seen[0].Header.Get(viewerHeader) != "" || seen[0].Header.Get(adminTokenHeader) != "" {
			t.Errorf("%s: a client's header reached streaming: %v", c.name, seen[0].Header)
		}
	}
}

// A node's gate does not take a channel's signed URL (its own offline channels
// check no token): 401, whatever the token says, and nothing is streamed.
func TestStreamGateNodeRefusesChannelSignedURL(t *testing.T) {
	g, be := p57Gate(t)
	g.channelSig = false
	g.allow = allowedNodeStreamPath
	tok := fakeToken(t, audChannel, chanID, signedNow.Add(time.Hour))
	for _, target := range []string{
		withSig("/stream/channel/"+chanID+"/index.m3u8", tok),
		withSig("/stream/via-home/channel/"+chanID+"/index.m3u8", tok),
	} {
		if rec := do(g, http.MethodGet, target, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", target, rec.Code)
		}
	}
	if n := len(be.seen()); n != 0 {
		t.Errorf("streamed %d requests", n)
	}
}

// The node's proxy to the home drops both headers the home's streaming service
// trusts from its own gate.
func TestHomeStreamProxyDropsGateHeaders(t *testing.T) {
	var got http.Header
	home := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	t.Cleanup(home.Close)
	rec := do(homeStreamProxy(home.URL), http.MethodGet, "/stream/channel/"+chanID+"/index.m3u8",
		map[string]string{viewerHeader: p57Viewer, adminTokenHeader: "1", "Cookie": "s=viewer"})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
	for _, h := range []string{viewerHeader, adminTokenHeader, "Cookie"} {
		if got.Get(h) != "" {
			t.Errorf("%s reached the home: %q", h, got.Get(h))
		}
	}
}

// Review finding 11: the gate hands the streaming service the sign-in the session
// is (its family and its device's age limit, as /api/auth/me says them), so a
// channel played through the web port is held to the device's limit; what a client
// sent in those headers never reaches the streaming service.
func TestStreamGatePassesTheSignIn(t *testing.T) {
	const family = "0f0f0f0f-1111-4222-8333-444444444444"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			switch r.Header.Get("Authorization") {
			case "Bearer child-device":
				_, _ = io.WriteString(w, `{"user_id":"`+p57Viewer+`","is_admin":false,"sign_in":"`+family+`","age_ceiling":9}`)
			case "Bearer nothing-allowed":
				_, _ = io.WriteString(w, `{"user_id":"`+p57Viewer+`","is_admin":false,"sign_in":"`+family+`","age_ceiling":-1}`)
			case "Bearer plain":
				_, _ = io.WriteString(w, `{"user_id":"`+p57Viewer+`","is_admin":false}`)
			default:
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(api.Close)
	au, _ := url.Parse(api.URL)
	path := "/stream/channel/" + chanID + "/index.m3u8"
	for _, c := range []struct{ authz, wantFamily, wantCeiling string }{
		{"Bearer child-device", family, "9"},
		{"Bearer nothing-allowed", family, "-1"},
		{"Bearer plain", "", ""},
	} {
		be := &signedBackend{}
		g := newStreamGate(be, au)
		g.now = func() time.Time { return signedNow }
		// twice: the second is answered from the gate's cache of the session
		for i := 0; i < 2; i++ {
			rec := do(g, http.MethodGet, path, map[string]string{"Authorization": c.authz,
				viewerSignInHeader: "ffffffff-ffff-4fff-8fff-ffffffffffff", viewerCeilingHeader: "18"})
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: %d (%q)", c.authz, rec.Code, rec.Body.String())
			}
		}
		seen := be.seen()
		if len(seen) != 2 {
			t.Fatalf("%s: streaming saw %d requests", c.authz, len(seen))
		}
		for i, r := range seen {
			if r.Header.Get(viewerHeader) != p57Viewer || r.Header.Get(viewerSignInHeader) != c.wantFamily ||
				r.Header.Get(viewerCeilingHeader) != c.wantCeiling {
				t.Errorf("%s, request %d: viewer %q sign-in %q ceiling %q, want %q %q %q", c.authz, i,
					r.Header.Get(viewerHeader), r.Header.Get(viewerSignInHeader), r.Header.Get(viewerCeilingHeader),
					p57Viewer, c.wantFamily, c.wantCeiling)
			}
		}
	}
	// a signed URL names its own sign-in: the headers a client sent are dropped
	be := &signedBackend{}
	g := newStreamGate(be, au)
	g.now = func() time.Time { return signedNow }
	g.channelSig = true
	sig := fakeToken(t, audChannel, chanID, signedNow.Add(time.Hour))
	rec := do(g, http.MethodGet, path+"?sig="+sig, map[string]string{viewerSignInHeader: family, viewerCeilingHeader: "18"})
	if rec.Code != http.StatusOK {
		t.Fatalf("a signed channel URL: %d (%q)", rec.Code, rec.Body.String())
	}
	if r := be.seen()[0]; r.Header.Get(viewerSignInHeader) != "" || r.Header.Get(viewerCeilingHeader) != "" {
		t.Error("a client's sign-in headers reached streaming beside a signed URL")
	}
}
