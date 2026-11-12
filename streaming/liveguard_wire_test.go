package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/hdhr"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/liveguard"
)

// Every route the TV mounts register is walked: a channel's stream refuses a caller
// with no signed URL (401), the tuner and the guide files a caller whose address may
// not use the tuner (403), before the handler runs (the handlers here have nothing
// behind them: reaching one would panic). A route added to a mount later is guarded
// or fails this test's list of what stays open.
func TestEveryTVRouteIsGuarded(t *testing.T) {
	mounts := []func(chi.Router){
		(&channel.HTTPHandler{}).Mount,
		(&frontdoor.Handler{}).Mount,
		(&hdhr.Handler{}).Mount,
		(&hdhr.GuideHandler{}).Mount,
		(&hdhr.GuideHandler{}).MountArt,
		(&frontdoor.Handler{}).MountPreview,
	}
	// open: not live TV's to guard here (art has no viewer; the staging preview asks
	// for the change's preview token and an admin's session at the web port)
	open := map[string]bool{
		"/channel/{id}/logo": true, "/tv/art/channel/{id}/video/{video}": true,
		"/stream/staging/channel/{id}/index.m3u8": true, "/stream/staging/channel/{id}/s/{slot}/{segment}": true,
		"/staging/lineup.m3u": true, "/staging/xmltv.xml": true,
	}
	guard := &liveguard.Guard{}
	seen := 0
	for _, mount := range guardMounts(guard, mounts) {
		r := chi.NewRouter()
		mount(r)
		err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if open[route] {
				return nil
			}
			seen++
			path := strings.NewReplacer("{id}", "11111111-1111-4111-8111-111111111111", "{slot}", "blue",
				"{segment}", "seg-1.ts", "{channel}", "12", "/*", "/x").Replace(route)
			want := http.StatusForbidden
			if strings.HasPrefix(path, "/stream/channel/") {
				want = http.StatusUnauthorized
			}
			if strings.ContainsAny(path, "{*") {
				t.Errorf("%s %s: a route this test cannot fill in; guard it and add it here", method, route)
				return nil
			}
			req := httptest.NewRequest(method, path, nil)
			req.RemoteAddr = "203.0.113.9:4000"
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != want {
				t.Errorf("%s %s from a stranger = %d, want %d", method, route, w.Code, want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen < 14 {
		t.Fatalf("only %d guarded routes walked: the mounts changed", seen)
	}
}
