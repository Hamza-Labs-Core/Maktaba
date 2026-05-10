// Package streaming is the API's typed wrapper around the Streaming
// gRPC service (Story 7.18 AC-2). Same retry / breaker / context
// propagation pattern as the pipeline client wrapper.
//
// The generated protobuf surface lives in “shared/proto/streaming“
// outside this module; we expose a Go-native interface so tests can
// stub it.
package streaming

import (
	"context"
	"time"

	pipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
)

// OpenSessionRequest mirrors the same proto on the Streaming side.
type OpenSessionRequest struct {
	UserID         string
	VideoID        string
	ClientProfile  string
	AudioTrack     int
	SubtitleTrack  string
	StartSec       float64
	MaxBitrateKbps int
	Format         string
	BurnSubs       bool
	ForceTranscode bool
	ForceSoftware  bool
}

// OpenSessionResponse mirrors the Streaming proto reply.
type OpenSessionResponse struct {
	SessionID   string
	Mode        string
	ManifestURL string
	DirectURL   string
	ExpiresAt   time.Time
}

// Capabilities is the AC-2 “GetCapabilities“ return.
type Capabilities struct {
	Codecs              []string
	HWAccel             string
	MaxBitrateKbps      int
	SupportedContainers []string
	TranscodeSlots      Slots
}

// Slots reports transcoder capacity.
type Slots struct {
	Used     int
	Capacity int
}

// Status mirrors HealthCheck.
type Status struct {
	Healthy bool
	Detail  string
}

// Client is the Go-native surface.
type Client interface {
	OpenSession(ctx context.Context, req OpenSessionRequest) (OpenSessionResponse, error)
	CloseSession(ctx context.Context, sessionID string) error
	EvictHashCache(ctx context.Context, hash string) error
	GetCapabilities(ctx context.Context) (Capabilities, error)
	HealthCheck(ctx context.Context) (Status, error)
}

// Config bundles the retry knobs. Shares the breaker primitive with the
// pipeline package so the API has one consistent failure-mode model.
type Config struct {
	Addr             string
	OpenTimeout      time.Duration
	CloseTimeout     time.Duration
	MaxRetries       int
	CircuitWindow    time.Duration
	CircuitOpenTime  time.Duration
	FailureThreshold float64
}

// DefaultConfig is the canonical knob set.
func DefaultConfig() Config {
	return Config{
		OpenTimeout:      5 * time.Second,
		CloseTimeout:     2 * time.Second,
		MaxRetries:       3,
		CircuitWindow:    30 * time.Second,
		CircuitOpenTime:  10 * time.Second,
		FailureThreshold: 0.5,
	}
}

// NewBreaker is a passthrough so callers don't need to import the
// pipeline package just for the breaker primitive.
func NewBreaker(cfg Config) *pipeline.Breaker {
	return pipeline.NewBreaker(cfg.CircuitWindow, cfg.CircuitOpenTime, cfg.FailureThreshold)
}
