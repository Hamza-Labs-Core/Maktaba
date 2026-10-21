package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Live TV on a node's web port: the home's front door while it answers, this machine's
// own channels (from its copy of the home's database) while it is away.
func TestNodeLiveTVFollowsTheHome(t *testing.T) {
	var away bool
	seen := func(name string) (http.Handler, *string) {
		var path string
		return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			path = name + " " + r.URL.Path
		}), &path
	}
	local, localPath := seen("local")
	home, homePath := seen("home")
	h := nodeStreamRouter(local, home, func() bool { return away })
	do := func(p string) string {
		*localPath, *homePath = "", ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		return *localPath + *homePath + " " + rec.Header().Get("X-Maktaba-Stream")
	}
	if got := do("/stream/channel/c1/index.m3u8"); got != "home /stream/channel/c1/index.m3u8 home-front-door" {
		t.Fatalf("home up: %q", got)
	}
	away = true
	if got := do("/stream/channel/c1/seg-3.ts"); got != "local /stream/offline/stream/channel/c1/seg-3.ts local-offline" {
		t.Fatalf("home away: %q", got)
	}
	if got := do("/stream/sessions/s1/master.m3u8"); got != "local /stream/sessions/s1/master.m3u8 local" {
		t.Fatalf("VOD: %q", got)
	}
}
