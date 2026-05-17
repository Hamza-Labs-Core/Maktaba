package pipeline

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/jsoncodec"
)

// ErrNotImplemented is returned by the real client for surfaces whose
// server side does not exist yet. Transcribe (server-streaming) and
// STTTest have no pipeline server implementation; they stay stubbed
// until their epic wave.
var ErrNotImplemented = errors.New("pipeline gRPC client: not implemented")

// Pipeline service / method paths — must match
// pipeline/src/maktaba_pipeline/grpc_server.py
// (PIPELINE_SERVICE_NAME + handler keys).
const (
	pipelineService    = "maktaba.pipeline.v1.Pipeline"
	methodEmbed        = "/" + pipelineService + "/Embed"
	methodListBackends = "/" + pipelineService + "/ListBackends"
	methodExtractSub   = "/" + pipelineService + "/ExtractEmbeddedSubtitle"
)

// requestIDKey is the metadata key the pipeline expects inbound
// X-Request-Id correlation on (see package doc, Story 7.18).
const requestIDKey = "maktaba-request-id"

// NewRealClient dials the pipeline gRPC server with the JSON codec and
// returns a Client. The dial is non-blocking (grpc.NewClient lazily
// connects on first RPC); a misconfigured/unreachable address surfaces
// as a per-call error and via HealthCheck rather than a boot failure,
// preserving the previous fail-soft contract.
func NewRealClient(cfg Config) Client {
	addr := cfg.Addr
	if addr == "" {
		return &realClient{
			detail:  "address unset",
			breaker: NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold),
			cfg:     cfg,
		}
	}
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(jsoncodec.Codec{})),
	)
	if err != nil {
		return &realClient{
			addr:    addr,
			detail:  "dial setup failed: " + err.Error(),
			breaker: NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold),
			cfg:     cfg,
		}
	}
	return &realClient{
		addr:    addr,
		conn:    conn,
		detail:  "configured",
		breaker: NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold),
		cfg:     cfg,
	}
}

type realClient struct {
	addr    string
	conn    *grpc.ClientConn
	detail  string
	breaker *Breaker
	cfg     Config
}

// withRequestID copies an inbound X-Request-Id (carried on the context
// as the maktaba-request-id metadata) onto the outbound call so the
// pipeline can correlate logs.
func withRequestID(ctx context.Context) context.Context {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(requestIDKey); len(v) > 0 && v[0] != "" {
			return metadata.AppendToOutgoingContext(ctx, requestIDKey, v[0])
		}
	}
	return ctx
}

// invoke runs a unary RPC through the breaker + retry budget. The
// pipeline server returns application errors in-band as
// {"error": "..."} with an OK gRPC status, so the response is scanned
// for that key.
func (c *realClient) invoke(ctx context.Context, method string, req map[string]any, resp *map[string]any) error {
	if c.conn == nil {
		return fmt.Errorf("pipeline gRPC client: not connected (%s)", c.detail)
	}
	callCtx := withRequestID(ctx)
	err := CallWithRetry(callCtx, c.breaker, c.cfg.MaxRetries, func(ctx context.Context) error {
		out := map[string]any{}
		if err := c.conn.Invoke(ctx, method, req, &out); err != nil {
			return err
		}
		*resp = out
		return nil
	})
	if err != nil {
		return err
	}
	if msg, ok := (*resp)["error"].(string); ok && msg != "" {
		return fmt.Errorf("pipeline: %s", msg)
	}
	return nil
}

func (c *realClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if c.cfg.EmbedTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.EmbedTimeout)
		defer cancel()
	}
	var resp map[string]any
	if err := c.invoke(ctx, methodEmbed, map[string]any{"text": text}, &resp); err != nil {
		return nil, err
	}
	raw, ok := resp["vector"].([]any)
	if !ok {
		return nil, fmt.Errorf("pipeline Embed: malformed response (missing vector)")
	}
	vec := make([]float32, 0, len(raw))
	for i, v := range raw {
		f, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("pipeline Embed: vector[%d] not a number", i)
		}
		vec = append(vec, float32(f))
	}
	return vec, nil
}

// Transcribe is server-streaming and has no pipeline server
// implementation.
// deferred: Epic 03/07 wave
func (c *realClient) Transcribe(_ context.Context, _ string) (<-chan TranscribeEvent, error) {
	return nil, ErrNotImplemented
}

func (c *realClient) ExtractEmbeddedSubtitle(ctx context.Context, videoID string, streamIndex int) (string, error) {
	var resp map[string]any
	req := map[string]any{"path": videoID, "stream_index": streamIndex}
	if err := c.invoke(ctx, methodExtractSub, req, &resp); err != nil {
		return "", err
	}
	body, ok := resp["body"].(string)
	if !ok {
		return "", fmt.Errorf("pipeline ExtractEmbeddedSubtitle: malformed response (missing body)")
	}
	return body, nil
}

func (c *realClient) ListBackends(ctx context.Context) ([]Backend, error) {
	var resp map[string]any
	if err := c.invoke(ctx, methodListBackends, map[string]any{}, &resp); err != nil {
		return nil, err
	}
	raw, ok := resp["backends"].([]any)
	if !ok {
		// Server with no registered backends still returns
		// {"backends": []}; a missing key means a malformed reply.
		return []Backend{}, nil
	}
	out := make([]Backend, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, Backend{
			Name:             asString(m["name"]),
			Available:        asBool(m["available"]),
			Version:          asString(m["version"]),
			Models:           asStringSlice(m["models"]),
			HWAccel:          asString(m["hwaccel"]),
			CostPerMinuteUSD: asFloat(m["cost_per_minute_usd"]),
		})
	}
	return out, nil
}

// STTTest has no pipeline server implementation.
// deferred: Epic 03/07 wave
func (c *realClient) STTTest(_ context.Context, _ string, _ map[string]any) (any, error) {
	return nil, ErrNotImplemented
}

// HealthCheck reports reachability. The pipeline server exposes no
// HealthCheck RPC, so a cheap ListBackends round-trip doubles as a
// liveness probe (it returns {"backends": []} even with no backends).
func (c *realClient) HealthCheck(ctx context.Context) (Status, error) {
	if c.conn == nil {
		return Status{Healthy: false, Detail: c.detail}, nil
	}
	var resp map[string]any
	if err := c.invoke(ctx, methodListBackends, map[string]any{}, &resp); err != nil {
		return Status{Healthy: false, Detail: "list_backends probe failed: " + err.Error()}, nil
	}
	return Status{Healthy: true, Detail: "reachable"}, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

func asStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
