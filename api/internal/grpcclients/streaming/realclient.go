package streaming

import (
	"context"
	"errors"
	"net"
	"time"

	pipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
)

// ErrNotImplemented is returned by the stub real-client for surfaces that
// require generated protobuf stubs. The dial wrapper is wired so the API
// can carry the configured address through to readiness checks and audit
// logs; the actual RPC layer ships with the Story 7.18 plan §6 proto file.
var ErrNotImplemented = errors.New("streaming gRPC client: not implemented (awaiting proto stubs)")

// NewRealClient mirrors pipeline.NewRealClient: it probes the configured
// address via a short TCP dial so main.go can fail-soft when the
// streaming service is unreachable, and stubs the protobuf-typed
// surface until Story 7.18 plan §6 lands.
func NewRealClient(cfg Config) Client {
	addr := cfg.Addr
	healthy := false
	detail := "address unset"
	if addr != "" {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			detail = "tcp dial failed: " + err.Error()
		} else {
			_ = conn.Close()
			healthy = true
			detail = "tcp reachable; proto client stubbed"
		}
	}
	return &realClient{
		addr:    addr,
		healthy: healthy,
		detail:  detail,
		breaker: pipeline.NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold),
	}
}

type realClient struct {
	addr    string
	healthy bool
	detail  string
	breaker *pipeline.Breaker
}

func (c *realClient) OpenSession(_ context.Context, _ OpenSessionRequest) (OpenSessionResponse, error) {
	return OpenSessionResponse{}, ErrNotImplemented
}

func (c *realClient) CloseSession(_ context.Context, _ string) error {
	return ErrNotImplemented
}

func (c *realClient) EvictHashCache(_ context.Context, _ string) error {
	return ErrNotImplemented
}

func (c *realClient) GetCapabilities(_ context.Context) (Capabilities, error) {
	return Capabilities{}, nil
}

func (c *realClient) HealthCheck(_ context.Context) (Status, error) {
	return Status{Healthy: c.healthy, Detail: c.detail}, nil
}
