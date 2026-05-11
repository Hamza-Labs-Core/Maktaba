package pipeline

import (
	"context"
	"errors"
	"net"
	"time"
)

// ErrNotImplemented is returned by the stub real-client for surfaces that
// require generated protobuf stubs. The dial wrapper is wired so the API
// can carry the configured address through to readiness checks and audit
// logs; the actual RPC layer ships with the Story 7.18 plan §6 proto file.
var ErrNotImplemented = errors.New("pipeline gRPC client: not implemented (awaiting proto stubs)")

// NewRealClient returns a Client that holds the dial address and reports
// configured-but-stub status. It performs a TCP probe at construction so
// a misconfigured address fails fast — failures are tolerated and the
// returned client surfaces them via HealthCheck.
//
// The protobuf-typed client lands with Story 7.18 plan §6; until then
// this implementation lets main.go wire the dependency without nil
// pointers in the boot path.
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
		breaker: NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold),
	}
}

type realClient struct {
	addr    string
	healthy bool
	detail  string
	breaker *Breaker
}

func (c *realClient) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, ErrNotImplemented
}

func (c *realClient) Transcribe(_ context.Context, _ string) (<-chan TranscribeEvent, error) {
	return nil, ErrNotImplemented
}

func (c *realClient) ExtractEmbeddedSubtitle(_ context.Context, _ string, _ int) (string, error) {
	return "", ErrNotImplemented
}

func (c *realClient) ListBackends(_ context.Context) ([]Backend, error) {
	// Empty list keeps GET /settings/stt-backends from returning an
	// error while the registry server-side enumeration ships.
	return []Backend{}, nil
}

func (c *realClient) STTTest(_ context.Context, _ string, _ map[string]any) (any, error) {
	return nil, ErrNotImplemented
}

func (c *realClient) HealthCheck(_ context.Context) (Status, error) {
	return Status{Healthy: c.healthy, Detail: c.detail}, nil
}
