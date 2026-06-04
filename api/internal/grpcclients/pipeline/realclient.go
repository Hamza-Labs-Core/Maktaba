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

// ErrNotImplemented is retained for parity with the interface's
// fail-soft contract; every method now has a server-side implementation.
var ErrNotImplemented = errors.New("pipeline gRPC client: not implemented")

// Pipeline service / method paths — must match
// pipeline/src/maktaba_pipeline/grpc_server.py
// (PIPELINE_SERVICE_NAME + handler keys).
const (
	pipelineService    = "maktaba.pipeline.v1.Pipeline"
	methodEmbed        = "/" + pipelineService + "/Embed"
	methodListBackends = "/" + pipelineService + "/ListBackends"
	methodExtractSub   = "/" + pipelineService + "/ExtractEmbeddedSubtitle"
	methodTranscribe   = "/" + pipelineService + "/Transcribe"
	methodSTTTest      = "/" + pipelineService + "/STTTest"

	methodListModels       = "/" + pipelineService + "/ListModels"
	methodDownloadModel    = "/" + pipelineService + "/DownloadModel"
	methodDownloadProgress = "/" + pipelineService + "/DownloadProgress"
	methodDeleteModel      = "/" + pipelineService + "/DeleteModel"
	methodActivateModel    = "/" + pipelineService + "/ActivateModel"
	methodTestModel        = "/" + pipelineService + "/TestModel"
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
	// In-band application errors arrive as {"error": ...} with an OK
	// gRPC status. The value is conventionally a string, but a
	// non-string (e.g. a structured {"code":..,"message":..} or a
	// bare true) is still an error and must not be silently dropped.
	if v, present := (*resp)["error"]; present {
		if s, ok := v.(string); ok {
			if s != "" {
				return fmt.Errorf("pipeline: %s", s)
			}
		} else if v != nil {
			return fmt.Errorf("pipeline: %v", v)
		}
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

// Transcribe runs the pipeline's STT backend over videoID's audio and
// fans the returned segments into a channel. The pipeline surface is a
// unary JSON RPC (it returns the whole transcript at once); we adapt
// that to the streaming-shaped Go interface by pushing every segment
// then closing the channel, so callers can range over it exactly as
// they would a true server stream.
func (c *realClient) Transcribe(ctx context.Context, videoID string) (<-chan TranscribeEvent, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("pipeline gRPC client: not connected (%s)", c.detail)
	}
	if c.cfg.TranscribeTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.TranscribeTimeout)
		// The unary call below blocks until the full transcript is back,
		// so it is safe to cancel once invoke returns — the channel we
		// hand back is already fully populated.
		defer cancel()
	}
	var resp map[string]any
	// The pipeline accepts video_id as the audio-source alias (same
	// positional convention ExtractEmbeddedSubtitle uses).
	if err := c.invoke(ctx, methodTranscribe, map[string]any{"video_id": videoID}, &resp); err != nil {
		return nil, err
	}
	raw, ok := resp["segments"].([]any)
	if !ok {
		return nil, fmt.Errorf("pipeline Transcribe: malformed response (missing segments)")
	}
	ch := make(chan TranscribeEvent, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ch <- TranscribeEvent{
			SegmentID: asInt64(m["seq"]),
			Seq:       asInt(m["seq"]),
			StartSec:  asFloat(m["start_sec"]),
			EndSec:    asFloat(m["end_sec"]),
			Text:      asString(m["text"]),
			Final:     asBool(m["final"]),
		}
	}
	close(ch)
	return ch, nil
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

// STTTest runs the pipeline's short backend smoke test and returns the
// decoded result map verbatim (OK / backend / latency_ms / sample_text
// / segments). The settings adapter narrows it to STTTestResult; we
// keep the raw map here so the wire shape can grow without a client
// change.
func (c *realClient) STTTest(ctx context.Context, backend string, config map[string]any) (any, error) {
	req := map[string]any{}
	if backend != "" {
		req["backend"] = backend
	}
	if config != nil {
		req["config"] = config
	}
	var resp map[string]any
	if err := c.invoke(ctx, methodSTTTest, req, &resp); err != nil {
		return nil, err
	}
	return resp, nil
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

// ListModels returns the model catalog with runtime status overlaid.
func (c *realClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var resp map[string]any
	if err := c.invoke(ctx, methodListModels, map[string]any{}, &resp); err != nil {
		return nil, err
	}
	raw, ok := resp["models"].([]any)
	if !ok {
		// A pipeline with an empty catalog still returns {"models": []};
		// a missing key is a malformed reply, treated as empty.
		return []ModelInfo{}, nil
	}
	out := make([]ModelInfo, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, ModelInfo{
			ID:        asString(m["id"]),
			Type:      asString(m["type"]),
			Name:      asString(m["name"]),
			Size:      asString(m["size"]),
			SizeBytes: asInt64(m["size_bytes"]),
			Platform:  asString(m["platform"]),
			Gated:     asBool(m["gated"]),
			Installed: asBool(m["installed"]),
			Active:    asBool(m["active"]),
			Status:    asString(m["status"]),
			Progress:  asInt(m["progress"]),
		})
	}
	return out, nil
}

// DownloadModel starts an async download and returns the job ID.
func (c *realClient) DownloadModel(ctx context.Context, id string) (string, error) {
	var resp map[string]any
	if err := c.invoke(ctx, methodDownloadModel, map[string]any{"id": id}, &resp); err != nil {
		return "", err
	}
	jobID, ok := resp["job_id"].(string)
	if !ok || jobID == "" {
		return "", fmt.Errorf("pipeline DownloadModel: malformed response (missing job_id)")
	}
	return jobID, nil
}

// DownloadProgress polls a download job by ID.
func (c *realClient) DownloadProgress(ctx context.Context, jobID string) (DownloadStatus, error) {
	var resp map[string]any
	if err := c.invoke(ctx, methodDownloadProgress, map[string]any{"job_id": jobID}, &resp); err != nil {
		return DownloadStatus{}, err
	}
	return DownloadStatus{
		JobID:      asString(resp["job_id"]),
		ModelID:    asString(resp["model_id"]),
		Status:     asString(resp["status"]),
		Progress:   asInt(resp["progress"]),
		Downloaded: asInt64(resp["downloaded"]),
		Total:      asInt64(resp["total"]),
		Error:      asString(resp["error"]),
	}, nil
}

// DeleteModel removes a model's files. Returns false if it wasn't present.
func (c *realClient) DeleteModel(ctx context.Context, id string) (bool, error) {
	var resp map[string]any
	if err := c.invoke(ctx, methodDeleteModel, map[string]any{"id": id}, &resp); err != nil {
		return false, err
	}
	return asBool(resp["deleted"]), nil
}

// ActivateModel sets a model active for its type. An empty modelType
// lets the pipeline infer it from the catalog.
func (c *realClient) ActivateModel(ctx context.Context, id, modelType string) (ModelActivation, error) {
	req := map[string]any{"id": id}
	if modelType != "" {
		req["type"] = modelType
	}
	var resp map[string]any
	if err := c.invoke(ctx, methodActivateModel, req, &resp); err != nil {
		return ModelActivation{}, err
	}
	return ModelActivation{
		ID:     asString(resp["id"]),
		Type:   asString(resp["type"]),
		Active: asBool(resp["active"]),
	}, nil
}

// TestModel runs a short sample through the model.
func (c *realClient) TestModel(ctx context.Context, id string) (ModelTestResult, error) {
	var resp map[string]any
	if err := c.invoke(ctx, methodTestModel, map[string]any{"id": id}, &resp); err != nil {
		return ModelTestResult{}, err
	}
	return ModelTestResult{
		OK:        asBool(resp["ok"]),
		LatencyMs: asInt64(resp["latency_ms"]),
		Detail:    asString(resp["detail"]),
		Error:     asString(resp["error"]),
	}, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func asInt64(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
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
