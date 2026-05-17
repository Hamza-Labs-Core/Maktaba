package grpcsrv

import (
	"context"
	"net"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// dialServer stands up the gRPC server over an in-memory bufconn and
// returns a *grpc.ClientConn forced onto the JSON codec — the same
// wire setup the api streaming client uses.
func dialServer(t *testing.T, s *Server) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := NewGRPCServer(s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(jsonCodec{})),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func invoke(t *testing.T, conn *grpc.ClientConn, method string, req map[string]any) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := conn.Invoke(context.Background(), "/"+StreamingServiceName+"/"+method, req, &out); err != nil {
		t.Fatalf("invoke %s: %v", method, err)
	}
	return out
}

func TestServe_OpenSession(t *testing.T) {
	srv, _, row := setupServer(t)
	conn := dialServer(t, srv)
	out := invoke(t, conn, "OpenSession", map[string]any{
		"video_id":       row.VideoID.String(),
		"user_id":        uuid.New().String(),
		"client_profile": "ios-native",
	})
	if _, isErr := out["error"]; isErr {
		t.Fatalf("unexpected error: %v", out["error"])
	}
	if out["session_id"] == "" || out["mode"] == "" {
		t.Fatalf("bad response: %v", out)
	}
}

func TestServe_OpenSession_BadVideoID(t *testing.T) {
	srv, _, _ := setupServer(t)
	conn := dialServer(t, srv)
	out := invoke(t, conn, "OpenSession", map[string]any{
		"video_id": "not-a-uuid", "user_id": uuid.New().String(),
	})
	if _, isErr := out["error"]; !isErr {
		t.Fatalf("expected in-band error, got %v", out)
	}
}

func TestServe_CloseSession_NotFound(t *testing.T) {
	srv, _, _ := setupServer(t)
	conn := dialServer(t, srv)
	out := invoke(t, conn, "CloseSession", map[string]any{"session_id": uuid.New().String()})
	if _, isErr := out["error"]; !isErr {
		t.Fatalf("expected not-found error, got %v", out)
	}
}

func TestServe_EvictHashCache(t *testing.T) {
	srv, _, _ := setupServer(t)
	conn := dialServer(t, srv)
	out := invoke(t, conn, "EvictHashCache", map[string]any{"hash": "h1"})
	if _, isErr := out["error"]; isErr {
		t.Fatalf("unexpected error: %v", out["error"])
	}
	if _, ok := out["evicted"]; !ok {
		t.Fatalf("missing evicted count: %v", out)
	}
}

func TestServe_EvictHashCache_Empty(t *testing.T) {
	srv, _, _ := setupServer(t)
	conn := dialServer(t, srv)
	out := invoke(t, conn, "EvictHashCache", map[string]any{"hash": ""})
	if _, isErr := out["error"]; !isErr {
		t.Fatalf("expected error for empty hash, got %v", out)
	}
}

func TestServe_GetCapabilities(t *testing.T) {
	srv, _, _ := setupServer(t)
	conn := dialServer(t, srv)
	out := invoke(t, conn, "GetCapabilities", map[string]any{})
	if out["hwaccel"] == nil || out["codecs"] == nil {
		t.Fatalf("bad capabilities: %v", out)
	}
}

func TestServe_HealthCheck(t *testing.T) {
	srv, _, _ := setupServer(t)
	conn := dialServer(t, srv)
	out := invoke(t, conn, "HealthCheck", map[string]any{})
	if out["healthy"] != true {
		t.Fatalf("expected healthy, got %v", out)
	}
}
