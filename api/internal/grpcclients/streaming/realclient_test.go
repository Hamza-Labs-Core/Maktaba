package streaming

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/jsoncodec"
)

func newFakeStreamingClient(t *testing.T, handlers map[string]func(map[string]any) map[string]any) *realClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ForceServerCodec(jsoncodec.Codec{}))

	mk := func(h func(map[string]any) map[string]any) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
		return func(_ any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			req := map[string]any{}
			if err := dec(&req); err != nil {
				return nil, err
			}
			return h(req), nil
		}
	}
	sd := grpc.ServiceDesc{
		ServiceName: streamingService,
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "OpenSession", Handler: mk(handlers["OpenSession"])},
			{MethodName: "CloseSession", Handler: mk(handlers["CloseSession"])},
			{MethodName: "EvictHashCache", Handler: mk(handlers["EvictHashCache"])},
			{MethodName: "GetCapabilities", Handler: mk(handlers["GetCapabilities"])},
			{MethodName: "HealthCheck", Handler: mk(handlers["HealthCheck"])},
		},
	}
	var impl any = struct{}{}
	srv.RegisterService(&sd, impl)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(jsoncodec.Codec{})),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cfg := DefaultConfig()
	cfg.MaxRetries = 0
	return &realClient{
		addr:    "bufnet",
		conn:    conn,
		detail:  "configured",
		breaker: NewBreaker(cfg),
		cfg:     cfg,
	}
}

func TestStreamingClient_OpenSession(t *testing.T) {
	exp := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)
	c := newFakeStreamingClient(t, map[string]func(map[string]any) map[string]any{
		"OpenSession": func(req map[string]any) map[string]any {
			if req["video_id"] != "vid" || req["user_id"] != "usr" {
				t.Errorf("bad req: %v", req)
			}
			return map[string]any{
				"session_id":    "sess-1",
				"mode":          "transcode",
				"manifest_path": "/stream/sess-1/manifest.m3u8",
				"expires_at":    exp.Format(time.RFC3339),
				"state":         "active",
			}
		},
	})
	resp, err := c.OpenSession(context.Background(), OpenSessionRequest{
		UserID: "usr", VideoID: "vid", ClientProfile: "ios-native",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if resp.SessionID != "sess-1" || resp.Mode != "transcode" ||
		resp.ManifestURL != "/stream/sess-1/manifest.m3u8" || !resp.ExpiresAt.Equal(exp) {
		t.Fatalf("bad response: %+v", resp)
	}
}

func TestStreamingClient_OpenSession_ServerError(t *testing.T) {
	c := newFakeStreamingClient(t, map[string]func(map[string]any) map[string]any{
		"OpenSession": func(_ map[string]any) map[string]any {
			return map[string]any{"error": "resource-exhausted"}
		},
	})
	if _, err := c.OpenSession(context.Background(), OpenSessionRequest{}); err == nil {
		t.Fatal("expected in-band error to surface")
	}
}

func TestStreamingClient_OpenSession_NonStringServerError(t *testing.T) {
	c := newFakeStreamingClient(t, map[string]func(map[string]any) map[string]any{
		"OpenSession": func(_ map[string]any) map[string]any {
			// Structured (non-string) in-band error must not be
			// silently dropped just because it isn't a string.
			return map[string]any{"error": map[string]any{
				"code": float64(13), "message": "internal",
			}}
		},
	})
	if _, err := c.OpenSession(context.Background(), OpenSessionRequest{}); err == nil {
		t.Fatal("expected non-string in-band error to surface, got nil")
	}
}

func TestStreamingClient_CloseSession(t *testing.T) {
	c := newFakeStreamingClient(t, map[string]func(map[string]any) map[string]any{
		"CloseSession": func(req map[string]any) map[string]any {
			if req["session_id"] != "s1" {
				t.Errorf("session_id=%v", req["session_id"])
			}
			return map[string]any{"ok": true}
		},
	})
	if err := c.CloseSession(context.Background(), "s1"); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
}

func TestStreamingClient_CloseSession_Error(t *testing.T) {
	c := newFakeStreamingClient(t, map[string]func(map[string]any) map[string]any{
		"CloseSession": func(_ map[string]any) map[string]any {
			return map[string]any{"error": "not-found"}
		},
	})
	if err := c.CloseSession(context.Background(), "x"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestStreamingClient_EvictHashCache(t *testing.T) {
	c := newFakeStreamingClient(t, map[string]func(map[string]any) map[string]any{
		"EvictHashCache": func(req map[string]any) map[string]any {
			if req["hash"] != "h1" {
				t.Errorf("hash=%v", req["hash"])
			}
			return map[string]any{"evicted": 3}
		},
	})
	if err := c.EvictHashCache(context.Background(), "h1"); err != nil {
		t.Fatalf("EvictHashCache: %v", err)
	}
}

func TestStreamingClient_GetCapabilities(t *testing.T) {
	c := newFakeStreamingClient(t, map[string]func(map[string]any) map[string]any{
		"GetCapabilities": func(_ map[string]any) map[string]any {
			return map[string]any{
				"codecs":               []any{"h264", "aac"},
				"hwaccel":              "cuda",
				"max_bitrate_kbps":     40000,
				"supported_containers": []any{"mp4", "mkv"},
				"transcode_used":       2,
				"transcode_capacity":   4,
			}
		},
	})
	caps, err := c.GetCapabilities(context.Background())
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if len(caps.Codecs) != 2 || caps.HWAccel != "cuda" || caps.MaxBitrateKbps != 40000 ||
		caps.TranscodeSlots.Used != 2 || caps.TranscodeSlots.Capacity != 4 {
		t.Fatalf("bad caps: %+v", caps)
	}
}

func TestStreamingClient_HealthCheck(t *testing.T) {
	c := newFakeStreamingClient(t, map[string]func(map[string]any) map[string]any{
		"HealthCheck": func(_ map[string]any) map[string]any {
			return map[string]any{"healthy": true, "detail": "ok"}
		},
	})
	st, err := c.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if !st.Healthy || st.Detail != "ok" {
		t.Fatalf("bad status: %+v", st)
	}
}

func TestStreamingClient_HealthCheck_Unconnected(t *testing.T) {
	c := NewRealClient(Config{}).(*realClient)
	st, err := c.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if st.Healthy {
		t.Fatal("unconfigured client must be unhealthy")
	}
}
