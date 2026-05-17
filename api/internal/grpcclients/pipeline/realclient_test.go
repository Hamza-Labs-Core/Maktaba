package pipeline

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

// fakePipeline registers the same maktaba.pipeline.v1.Pipeline service
// the Python server exposes, using the JSON codec, so the real client
// is exercised end-to-end over an in-memory connection.
func newFakePipelineClient(t *testing.T, handlers map[string]func(map[string]any) map[string]any) *realClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)

	codec := jsoncodec.Codec{}
	srv := grpc.NewServer(grpc.ForceServerCodec(codec))

	mk := func(h func(map[string]any) map[string]any) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
		return func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			req := map[string]any{}
			if err := dec(&req); err != nil {
				return nil, err
			}
			return h(req), nil
		}
	}
	sd := grpc.ServiceDesc{
		ServiceName: pipelineService,
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "Embed", Handler: mk(handlers["Embed"])},
			{MethodName: "ListBackends", Handler: mk(handlers["ListBackends"])},
			{MethodName: "ExtractEmbeddedSubtitle", Handler: mk(handlers["ExtractEmbeddedSubtitle"])},
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
		breaker: NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold),
		cfg:     cfg,
	}
}

func TestRealClient_Embed(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"Embed": func(req map[string]any) map[string]any {
			if req["text"] != "hello" {
				t.Errorf("text=%v want hello", req["text"])
			}
			return map[string]any{"vector": []any{0.1, 0.2, 0.3}}
		},
	})
	vec, err := c.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 {
		t.Fatalf("vector=%v", vec)
	}
}

func TestRealClient_Embed_ServerError(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"Embed": func(_ map[string]any) map[string]any {
			return map[string]any{"error": "embedder backend not configured"}
		},
	})
	if _, err := c.Embed(context.Background(), "x"); err == nil {
		t.Fatal("expected in-band error to surface")
	}
}

func TestRealClient_Embed_NonStringServerError(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"Embed": func(_ map[string]any) map[string]any {
			// Structured (non-string) in-band error must not be
			// silently dropped just because it isn't a string.
			return map[string]any{"error": map[string]any{
				"code": float64(13), "message": "internal",
			}}
		},
	})
	if _, err := c.Embed(context.Background(), "x"); err == nil {
		t.Fatal("expected non-string in-band error to surface, got nil")
	}
}

func TestRealClient_ListBackends(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"ListBackends": func(_ map[string]any) map[string]any {
			return map[string]any{"backends": []any{
				map[string]any{
					"name": "whisper", "available": true, "version": "1.0",
					"models": []any{"base", "small"}, "hwaccel": "cuda",
					"cost_per_minute_usd": 0.02,
				},
			}}
		},
	})
	bs, err := c.ListBackends(context.Background())
	if err != nil {
		t.Fatalf("ListBackends: %v", err)
	}
	if len(bs) != 1 || bs[0].Name != "whisper" || !bs[0].Available ||
		bs[0].Version != "1.0" || len(bs[0].Models) != 2 ||
		bs[0].HWAccel != "cuda" || bs[0].CostPerMinuteUSD != 0.02 {
		t.Fatalf("backend mismatch: %+v", bs[0])
	}
}

func TestRealClient_ListBackends_Empty(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"ListBackends": func(_ map[string]any) map[string]any {
			return map[string]any{"backends": []any{}}
		},
	})
	bs, err := c.ListBackends(context.Background())
	if err != nil {
		t.Fatalf("ListBackends: %v", err)
	}
	if len(bs) != 0 {
		t.Fatalf("want empty, got %v", bs)
	}
}

func TestRealClient_ExtractEmbeddedSubtitle(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"ExtractEmbeddedSubtitle": func(req map[string]any) map[string]any {
			if req["path"] != "/v/x.mkv" {
				t.Errorf("path=%v", req["path"])
			}
			if int(req["stream_index"].(float64)) != 2 {
				t.Errorf("stream_index=%v", req["stream_index"])
			}
			return map[string]any{"body": "WEBVTT\n\n00:00.000 --> 00:01.000\nhi"}
		},
	})
	body, err := c.ExtractEmbeddedSubtitle(context.Background(), "/v/x.mkv", 2)
	if err != nil {
		t.Fatalf("ExtractEmbeddedSubtitle: %v", err)
	}
	if body == "" {
		t.Fatal("empty body")
	}
}

func TestRealClient_HealthCheck(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"ListBackends": func(_ map[string]any) map[string]any {
			return map[string]any{"backends": []any{}}
		},
	})
	st, err := c.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if !st.Healthy {
		t.Fatalf("expected healthy, got %+v", st)
	}
}

func TestRealClient_HealthCheck_Unconnected(t *testing.T) {
	c := NewRealClient(Config{}).(*realClient)
	st, err := c.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if st.Healthy {
		t.Fatal("unconfigured client must be unhealthy")
	}
}

func TestRealClient_DeferredRPCs(t *testing.T) {
	c := NewRealClient(Config{Addr: "127.0.0.1:1"}).(*realClient)
	if _, err := c.Transcribe(context.Background(), "v"); err != ErrNotImplemented {
		t.Errorf("Transcribe err=%v want ErrNotImplemented", err)
	}
	if _, err := c.STTTest(context.Background(), "b", nil); err != ErrNotImplemented {
		t.Errorf("STTTest err=%v want ErrNotImplemented", err)
	}
}

func TestRealClient_EmbedTimeoutWired(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"Embed": func(_ map[string]any) map[string]any {
			time.Sleep(50 * time.Millisecond)
			return map[string]any{"vector": []any{1.0}}
		},
	})
	c.cfg.EmbedTimeout = 1 * time.Millisecond
	if _, err := c.Embed(context.Background(), "x"); err == nil {
		t.Fatal("expected deadline to fire with 1ms EmbedTimeout")
	}
}
