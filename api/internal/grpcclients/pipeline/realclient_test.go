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
		return func(_ any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
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
			{MethodName: "Transcribe", Handler: mk(handlers["Transcribe"])},
			{MethodName: "STTTest", Handler: mk(handlers["STTTest"])},
			{MethodName: "ListModels", Handler: mk(handlers["ListModels"])},
			{MethodName: "DownloadModel", Handler: mk(handlers["DownloadModel"])},
			{MethodName: "DownloadProgress", Handler: mk(handlers["DownloadProgress"])},
			{MethodName: "DeleteModel", Handler: mk(handlers["DeleteModel"])},
			{MethodName: "ActivateModel", Handler: mk(handlers["ActivateModel"])},
			{MethodName: "TestModel", Handler: mk(handlers["TestModel"])},
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

func TestRealClient_Transcribe(t *testing.T) {
	var gotReq map[string]any
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"Transcribe": func(req map[string]any) map[string]any {
			gotReq = req
			return map[string]any{"segments": []any{
				map[string]any{"seq": 0.0, "start_sec": 0.0, "end_sec": 1.0, "text": "Bismillah", "final": true},
				map[string]any{"seq": 1.0, "start_sec": 1.0, "end_sec": 2.5, "text": "ar-Rahman", "final": true},
			}}
		},
	})
	ch, err := c.Transcribe(context.Background(), "vid-123")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	// The video id is forwarded as the audio-source alias.
	if gotReq["video_id"] != "vid-123" {
		t.Fatalf("expected video_id forwarded, got %v", gotReq)
	}
	var events []TranscribeEvent
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Text != "Bismillah" || events[0].Seq != 0 || !events[0].Final {
		t.Fatalf("first event malformed: %+v", events[0])
	}
	if events[1].Seq != 1 || events[1].EndSec != 2.5 {
		t.Fatalf("second event malformed: %+v", events[1])
	}
}

func TestRealClient_Transcribe_InBandError(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"Transcribe": func(map[string]any) map[string]any {
			return map[string]any{"error": "no STT backend ready"}
		},
	})
	if _, err := c.Transcribe(context.Background(), "v"); err == nil {
		t.Fatal("expected in-band error to surface")
	}
}

func TestRealClient_STTTest(t *testing.T) {
	var gotReq map[string]any
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"STTTest": func(req map[string]any) map[string]any {
			gotReq = req
			return map[string]any{
				"ok":          true,
				"backend":     "whisper",
				"latency_ms":  42.0,
				"sample_text": "Bismillah",
				"segments":    2.0,
			}
		},
	})
	res, err := c.STTTest(context.Background(), "whisper", map[string]any{"model": "tiny"})
	if err != nil {
		t.Fatalf("STTTest: %v", err)
	}
	if gotReq["backend"] != "whisper" {
		t.Fatalf("expected backend forwarded, got %v", gotReq)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", res)
	}
	if m["backend"] != "whisper" || m["sample_text"] != "Bismillah" {
		t.Fatalf("result map malformed: %v", m)
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

func TestRealClient_ListModels(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"ListModels": func(_ map[string]any) map[string]any {
			return map[string]any{"models": []any{
				map[string]any{
					"id": "all-minilm-l6-v2", "type": "embedding",
					"name": "all-MiniLM-L6-v2", "size": "90.0 MB",
					"size_bytes": float64(94371840), "platform": "any",
					"gated": false, "installed": true, "active": true,
					"status": "active", "progress": float64(100),
				},
			}}
		},
	})
	ms, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d models, want 1", len(ms))
	}
	m := ms[0]
	if m.ID != "all-minilm-l6-v2" || m.Type != "embedding" || m.Size != "90.0 MB" ||
		m.SizeBytes != 94371840 || !m.Installed || !m.Active ||
		m.Status != "active" || m.Progress != 100 {
		t.Fatalf("model mismatch: %+v", m)
	}
}

func TestRealClient_DownloadModel(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"DownloadModel": func(req map[string]any) map[string]any {
			if req["id"] != "mlx-whisper-large-v3" {
				t.Errorf("id=%v", req["id"])
			}
			return map[string]any{"job_id": "job-123"}
		},
	})
	jobID, err := c.DownloadModel(context.Background(), "mlx-whisper-large-v3")
	if err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	if jobID != "job-123" {
		t.Fatalf("job_id=%q", jobID)
	}
}

func TestRealClient_DownloadProgress(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"DownloadProgress": func(req map[string]any) map[string]any {
			if req["job_id"] != "job-123" {
				t.Errorf("job_id=%v", req["job_id"])
			}
			return map[string]any{
				"job_id": "job-123", "model_id": "mlx-whisper-large-v3",
				"status": "downloading", "progress": float64(42),
				"downloaded": float64(1000), "total": float64(2380),
				"error": nil,
			}
		},
	})
	st, err := c.DownloadProgress(context.Background(), "job-123")
	if err != nil {
		t.Fatalf("DownloadProgress: %v", err)
	}
	if st.Status != "downloading" || st.Progress != 42 || st.Downloaded != 1000 ||
		st.Total != 2380 || st.ModelID != "mlx-whisper-large-v3" {
		t.Fatalf("status mismatch: %+v", st)
	}
}

func TestRealClient_DeleteModel(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"DeleteModel": func(_ map[string]any) map[string]any {
			return map[string]any{"deleted": true}
		},
	})
	ok, err := c.DeleteModel(context.Background(), "all-minilm-l6-v2")
	if err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if !ok {
		t.Fatal("expected deleted=true")
	}
}

func TestRealClient_ActivateModel(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"ActivateModel": func(req map[string]any) map[string]any {
			if req["id"] != "pyannote-diarization-3.1" {
				t.Errorf("id=%v", req["id"])
			}
			return map[string]any{
				"id": "pyannote-diarization-3.1", "type": "diarization", "active": true,
			}
		},
	})
	act, err := c.ActivateModel(context.Background(), "pyannote-diarization-3.1", "")
	if err != nil {
		t.Fatalf("ActivateModel: %v", err)
	}
	if act.Type != "diarization" || !act.Active || act.ID != "pyannote-diarization-3.1" {
		t.Fatalf("activation mismatch: %+v", act)
	}
}

func TestRealClient_TestModel(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"TestModel": func(_ map[string]any) map[string]any {
			return map[string]any{"ok": true, "latency_ms": float64(42), "detail": "ran sample"}
		},
	})
	res, err := c.TestModel(context.Background(), "all-minilm-l6-v2")
	if err != nil {
		t.Fatalf("TestModel: %v", err)
	}
	if !res.OK || res.LatencyMs != 42 || res.Detail != "ran sample" {
		t.Fatalf("test result mismatch: %+v", res)
	}
}

func TestRealClient_ListModels_ServerError(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"ListModels": func(_ map[string]any) map[string]any {
			return map[string]any{"error": "boom"}
		},
	})
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Fatal("expected in-band error to surface")
	}
}
