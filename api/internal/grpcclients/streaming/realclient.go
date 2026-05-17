package streaming

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/jsoncodec"
	pipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
)

// ErrNotImplemented is retained for parity with the pipeline client;
// every streaming RPC now has a server implementation so it is no
// longer returned by the real client.
var ErrNotImplemented = errors.New("streaming gRPC client: not implemented")

// Streaming service / method paths — must match
// streaming/internal/grpcsrv/serve.go (StreamingServiceName + methods).
const (
	streamingService      = "maktaba.streaming.v1.Streaming"
	methodOpenSession     = "/" + streamingService + "/OpenSession"
	methodCloseSession    = "/" + streamingService + "/CloseSession"
	methodEvictHashCache  = "/" + streamingService + "/EvictHashCache"
	methodGetCapabilities = "/" + streamingService + "/GetCapabilities"
	methodStreamingHealth = "/" + streamingService + "/HealthCheck"
	streamingRequestIDKey = "maktaba-request-id"
)

// NewRealClient dials the streaming gRPC server with the JSON codec.
// Non-blocking dial (fail-soft): an unreachable address surfaces per
// call and via HealthCheck rather than failing the API boot path.
func NewRealClient(cfg Config) Client {
	addr := cfg.Addr
	if addr == "" {
		return &realClient{
			detail:  "address unset",
			breaker: pipeline.NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold),
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
			breaker: pipeline.NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold),
			cfg:     cfg,
		}
	}
	return &realClient{
		addr:    addr,
		conn:    conn,
		detail:  "configured",
		breaker: pipeline.NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold),
		cfg:     cfg,
	}
}

type realClient struct {
	addr    string
	conn    *grpc.ClientConn
	detail  string
	breaker *pipeline.Breaker
	cfg     Config
}

func withRequestID(ctx context.Context) context.Context {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(streamingRequestIDKey); len(v) > 0 && v[0] != "" {
			return metadata.AppendToOutgoingContext(ctx, streamingRequestIDKey, v[0])
		}
	}
	return ctx
}

// invoke runs a unary RPC through the breaker + retry budget. The
// streaming server returns application errors in-band as
// {"error": "..."} with an OK gRPC status (same convention as the
// pipeline server), so the response is scanned for that key.
func (c *realClient) invoke(ctx context.Context, method string, req map[string]any, resp *map[string]any) error {
	if c.conn == nil {
		return fmt.Errorf("streaming gRPC client: not connected (%s)", c.detail)
	}
	callCtx := withRequestID(ctx)
	err := pipeline.CallWithRetry(callCtx, c.breaker, c.cfg.MaxRetries, func(ctx context.Context) error {
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
		return fmt.Errorf("streaming: %s", msg)
	}
	return nil
}

func (c *realClient) OpenSession(ctx context.Context, req OpenSessionRequest) (OpenSessionResponse, error) {
	if c.cfg.OpenTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.OpenTimeout)
		defer cancel()
	}
	body := map[string]any{
		"user_id":          req.UserID,
		"video_id":         req.VideoID,
		"client_profile":   req.ClientProfile,
		"audio_track":      req.AudioTrack,
		"subtitle_track":   req.SubtitleTrack,
		"start_sec":        req.StartSec,
		"max_bitrate_kbps": req.MaxBitrateKbps,
		"format":           req.Format,
		"burn_subs":        req.BurnSubs,
		"force_transcode":  req.ForceTranscode,
		"force_software":   req.ForceSoftware,
	}
	var resp map[string]any
	if err := c.invoke(ctx, methodOpenSession, body, &resp); err != nil {
		return OpenSessionResponse{}, err
	}
	out := OpenSessionResponse{
		SessionID:   asString(resp["session_id"]),
		Mode:        asString(resp["mode"]),
		ManifestURL: asString(resp["manifest_path"]),
	}
	if ts := asString(resp["expires_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			out.ExpiresAt = t
		}
	}
	return out, nil
}

func (c *realClient) CloseSession(ctx context.Context, sessionID string) error {
	if c.cfg.CloseTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.CloseTimeout)
		defer cancel()
	}
	var resp map[string]any
	return c.invoke(ctx, methodCloseSession, map[string]any{"session_id": sessionID}, &resp)
}

func (c *realClient) EvictHashCache(ctx context.Context, hash string) error {
	var resp map[string]any
	return c.invoke(ctx, methodEvictHashCache, map[string]any{"hash": hash}, &resp)
}

func (c *realClient) GetCapabilities(ctx context.Context) (Capabilities, error) {
	var resp map[string]any
	if err := c.invoke(ctx, methodGetCapabilities, map[string]any{}, &resp); err != nil {
		return Capabilities{}, err
	}
	return Capabilities{
		Codecs:              asStringSlice(resp["codecs"]),
		HWAccel:             asString(resp["hwaccel"]),
		MaxBitrateKbps:      asInt(resp["max_bitrate_kbps"]),
		SupportedContainers: asStringSlice(resp["supported_containers"]),
		TranscodeSlots: Slots{
			Used:     asInt(resp["transcode_used"]),
			Capacity: asInt(resp["transcode_capacity"]),
		},
	}, nil
}

func (c *realClient) HealthCheck(ctx context.Context) (Status, error) {
	if c.conn == nil {
		return Status{Healthy: false, Detail: c.detail}, nil
	}
	var resp map[string]any
	if err := c.invoke(ctx, methodStreamingHealth, map[string]any{}, &resp); err != nil {
		return Status{Healthy: false, Detail: "health probe failed: " + err.Error()}, nil
	}
	healthy, _ := resp["healthy"].(bool)
	return Status{Healthy: healthy, Detail: asString(resp["detail"])}, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
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
