package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgpool"
)

func named(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handler", name)
		w.Header().Set("X-Path", r.URL.Path)
	})
}

// A full node's streaming port: the front door's signed channel routes to the placed
// engine, this machine's own channels (the home away) to the offline station, for this
// machine's web port only, everything else to VOD.
func TestSplitFullNodeOfflineStation(t *testing.T) {
	offline := loopbackOnly(http.StripPrefix(offlinePrefix, named("offline")))
	h := splitFullNode(named("tv"), named("vod"), offline)
	do := func(path, from string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = from
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if r := do("/stream/channel/c1/index.m3u8", "192.168.1.5:5000"); r.Header().Get("X-Handler") != "tv" {
		t.Fatalf("a signed channel route: %v", r.Header())
	}
	r := do("/stream/offline/stream/channel/c1/index.m3u8", "127.0.0.1:5000")
	if r.Header().Get("X-Handler") != "offline" || r.Header().Get("X-Path") != "/stream/channel/c1/index.m3u8" {
		t.Fatalf("the offline station from this machine: %v", r.Header())
	}
	if r := do("/stream/offline/stream/channel/c1/index.m3u8", "192.168.1.5:5000"); r.Code != http.StatusNotFound {
		t.Fatalf("the offline station from the network: %d", r.Code)
	}
	if r := do("/stream/sessions/s1/master.m3u8", "192.168.1.5:5000"); r.Header().Get("X-Handler") != "vod" {
		t.Fatalf("VOD: %v", r.Header())
	}
	if r := splitFullNode(named("tv"), named("vod"), nil); true {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/offline/stream/channel/c1/index.m3u8", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("no copy: %d", rec.Code)
		}
	}
}

// This machine's copy of the home's database gets a small pool of its own.
func TestOpenCopyCapsThePool(t *testing.T) {
	t.Setenv(copyEnv, "postgres://maktaba@127.0.0.1:1/maktaba?sslmode=disable")
	bg := newBackground(context.Background())
	defer bg.Stop(time.Second)
	pool := openCopy(bg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if pool == nil {
		t.Fatal("no pool on the copy")
	}
	if got := pool.Config().MaxConns; got != pgpool.Copy {
		t.Fatalf("MaxConns = %d, want %d", got, pgpool.Copy)
	}
}
