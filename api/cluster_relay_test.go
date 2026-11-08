package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
)

// The cluster door (the agent channel's) carries Plex only. Even a caller of the home's own
// kind (any process holding the home agent key) is refused every other extension: a
// home-only one (Cloudflare Stream) and the removed dizqueTV, which is no extension at all.
func TestTheClusterDoorCarriesPlexOnly(t *testing.T) {
	cfg := clusterRelayConfig(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(cfg.Extensions) != 1 || cfg.Extensions["plex"] == nil {
		t.Fatalf("the cluster door's extensions: %v", cfg.Extensions)
	}
	cfg.Settings = func(context.Context) (relay.Settings, error) {
		s := relay.DefaultSettings()
		s.Enabled = true
		return s, nil
	}
	cfg.Upstream = func(context.Context, *relay.Extension) (relay.Upstream, error) {
		return relay.Upstream{Enabled: true, URL: "http://127.0.0.1:9"}, nil
	}
	door := relay.New(cfg)
	home := relay.Caller{Kind: "home", WorkerID: "w1", Session: "s1"}
	for ext, want := range map[string]string{"cloudflare_stream": relay.CodeHomeOnly, "dizquetv": relay.CodeUnknown} {
		res := door.Serve(context.Background(), home, relay.Request{Ext: ext, Method: "GET", Target: "/maktaba/channels"}, nil)
		if res.Err == nil || res.Err.Code != want {
			t.Fatalf("%s: %+v; want %s", ext, res, want)
		}
	}
}
