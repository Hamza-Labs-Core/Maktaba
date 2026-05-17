// serve.go exposes the in-process Server over a real gRPC server using
// the JSON-on-bytes codec convention (same wire format the pipeline
// gRPC server uses). This is a thin registration/adapter layer: each
// handler JSON-decodes a flat dict into the existing Server method
// args and JSON-encodes the result. The Server struct itself is not
// reshaped.
package grpcsrv

import (
	"context"
	"net"

	"google.golang.org/grpc"
)

// StreamingServiceName is the gRPC service path. The api streaming
// client (api/internal/grpcclients/streaming) Invokes
// /maktaba.streaming.v1.Streaming/<Method> against this.
const StreamingServiceName = "maktaba.streaming.v1.Streaming"

// jsonHandler adapts a (request map) -> (response map, error) function
// into a grpc unary method handler that uses the JSON codec.
func jsonHandler(fn func(context.Context, map[string]any) (map[string]any, error)) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
	return func(_ any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		req := map[string]any{}
		if err := dec(&req); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return fn(ctx, req)
		}
		info := &grpc.UnaryServerInfo{Server: nil}
		return interceptor(ctx, req, info, func(ctx context.Context, _ any) (any, error) {
			return fn(ctx, req)
		})
	}
}

// serviceDesc builds the grpc.ServiceDesc binding the five RPCs to the
// Server's methods via JSON adapters.
func serviceDesc(s *Server) *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: StreamingServiceName,
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "OpenSession", Handler: jsonHandler(s.handleOpenSession)},
			{MethodName: "CloseSession", Handler: jsonHandler(s.handleCloseSession)},
			{MethodName: "EvictHashCache", Handler: jsonHandler(s.handleEvictHashCache)},
			{MethodName: "GetCapabilities", Handler: jsonHandler(s.handleGetCapabilities)},
			{MethodName: "HealthCheck", Handler: jsonHandler(s.handleHealthCheck)},
		},
	}
}

// NewGRPCServer returns a *grpc.Server with the Streaming service
// registered under the JSON codec. Application errors are returned
// in-band as {"error": "..."} so the wire stays gRPC-OK, matching the
// pipeline server's convention; the api client scans for that key.
func NewGRPCServer(s *Server) *grpc.Server {
	srv := grpc.NewServer(grpc.ForceServerCodec(jsonCodec{}))
	desc := serviceDesc(s)
	var impl any = struct{}{}
	srv.RegisterService(desc, impl)
	return srv
}

// Serve registers the service and blocks serving on lis until the
// returned *grpc.Server is stopped. Callers own graceful shutdown via
// the returned server's GracefulStop.
func Serve(s *Server, lis net.Listener) (*grpc.Server, error) {
	srv := NewGRPCServer(s)
	go func() { _ = srv.Serve(lis) }()
	return srv, nil
}

// --- handlers: JSON dict <-> existing Server method args ---

func errResp(err error) (map[string]any, error) {
	return map[string]any{"error": err.Error()}, nil
}

func (s *Server) handleOpenSession(ctx context.Context, m map[string]any) (map[string]any, error) {
	req := OpenSessionRequest{
		VideoID:        jsonString(m["video_id"]),
		UserID:         jsonString(m["user_id"]),
		ClientProfile:  jsonString(m["client_profile"]),
		AudioTrack:     jsonInt(m["audio_track"]),
		SubtitleTrack:  jsonInt(m["subtitle_track"]),
		StartSec:       jsonInt(m["start_sec"]),
		MaxBitrateKbps: jsonInt(m["max_bitrate_kbps"]),
		Format:         jsonString(m["format"]),
		ForceSoftware:  jsonBool(m["force_software"]),
		ForceTranscode: jsonBool(m["force_transcode"]),
		BurnSubs:       jsonBool(m["burn_subs"]),
		AcceptQueue:    jsonBool(m["accept_queue"]),
	}
	resp, err := s.OpenSession(ctx, req)
	if err != nil {
		return errResp(err)
	}
	out := map[string]any{
		"session_id":    resp.SessionID,
		"mode":          resp.Mode,
		"manifest_path": resp.ManifestPath,
		"expires_at":    resp.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		"state":         resp.State,
	}
	ladder := make([]map[string]any, 0, len(resp.Ladder))
	for _, r := range resp.Ladder {
		ladder = append(ladder, map[string]any{
			"name": r.Name, "width": r.Width, "height": r.Height, "bitrate_kbps": r.BitrateKbps,
		})
	}
	out["ladder"] = ladder
	if resp.Queue != nil {
		out["queue"] = map[string]any{"position": resp.Queue.Position, "eta_sec": resp.Queue.ETASec}
	}
	return out, nil
}

func (s *Server) handleCloseSession(ctx context.Context, m map[string]any) (map[string]any, error) {
	if err := s.CloseSession(ctx, jsonString(m["session_id"])); err != nil {
		return errResp(err)
	}
	return map[string]any{"ok": true}, nil
}

func (s *Server) handleEvictHashCache(ctx context.Context, m map[string]any) (map[string]any, error) {
	n, err := s.EvictHashCache(ctx, jsonString(m["hash"]))
	if err != nil {
		return errResp(err)
	}
	return map[string]any{"evicted": n}, nil
}

func (s *Server) handleGetCapabilities(ctx context.Context, _ map[string]any) (map[string]any, error) {
	c, err := s.GetCapabilities(ctx)
	if err != nil {
		return errResp(err)
	}
	return map[string]any{
		"codecs":               c.Codecs,
		"hwaccel":              c.HWAccel,
		"max_bitrate_kbps":     c.MaxBitrateKbps,
		"supported_containers": c.SupportedContainers,
		"transcode_used":       c.TranscodeUsed,
		"transcode_capacity":   c.TranscodeCapacity,
	}, nil
}

func (s *Server) handleHealthCheck(ctx context.Context, _ map[string]any) (map[string]any, error) {
	st, err := s.HealthCheck(ctx)
	if err != nil {
		return errResp(err)
	}
	return map[string]any{"healthy": st.Healthy, "detail": st.Detail}, nil
}

// --- JSON scalar coercion (numbers decode as float64) ---

func jsonString(v any) string {
	s, _ := v.(string)
	return s
}

func jsonBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func jsonInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
