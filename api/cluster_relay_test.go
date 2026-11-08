package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
)

// The cluster door (the agent channel's) carries Plex only: dizqueTV's operations are the
// plugin API's door, for a plugin whose manifest lists the service (issue #827). Even a
// caller of the home's own kind (any process holding the home agent key) is refused them.
func TestTheClusterDoorRefusesDizqueTV(t *testing.T) {
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
	for _, target := range []string{"/maktaba/shows", "/maktaba/channels", "/maktaba/plex-servers"} {
		res := door.Serve(context.Background(), relay.Caller{Kind: "home", WorkerID: "w1", Session: "s1"},
			relay.Request{Ext: "dizquetv", Method: "GET", Target: target}, nil)
		if res.Err == nil || res.Err.Code != relay.CodeHomeOnly {
			t.Fatalf("%s: %+v", target, res)
		}
	}
}
