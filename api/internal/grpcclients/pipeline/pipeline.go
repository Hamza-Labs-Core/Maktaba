// Package pipeline is the API's typed wrapper around the Pipeline
// gRPC service (Story 7.18 AC-1). The generated protobuf client is
// owned by “shared/proto“ and lives outside this module so the API
// can stub the interface in tests without a protobuf toolchain
// dependency.
//
// The wrapper adds:
//
//   - Per-call deadlines (configurable; defaults to 5 s for Embed and
//     30 s for Transcribe);
//   - Bounded retry with jittered backoff for UNAVAILABLE /
//     DEADLINE_EXCEEDED (default 3 retries);
//   - A simple sliding-window circuit breaker (50 % failure in 30 s
//     opens it for 10 s);
//   - Context propagation: an inbound `X-Request-Id` is carried over
//     to the receiving service via the “maktaba-request-id“ metadata
//     key.
package pipeline

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"
)

// ErrCircuitOpen is returned by Call when the breaker is open.
var ErrCircuitOpen = errors.New("circuit-open")

// Backend describes a registered STT backend (Story 7.15 AC-4).
type Backend struct {
	Name             string
	Available        bool
	Version          string
	Models           []string
	HWAccel          string
	CostPerMinuteUSD float64
}

// TranscribeEvent is one stream event from a Transcribe RPC.
type TranscribeEvent struct {
	SegmentID int64
	Seq       int
	StartSec  float64
	EndSec    float64
	Text      string
	Final     bool
}

// Status mirrors Pipeline.HealthCheck.
type Status struct {
	Healthy bool
	Detail  string
}

// Client is the wrapped interface that handlers consume. Tests inject a
// fake; production injects a concrete *RealClient that wraps the
// generated proto client.
type Client interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Transcribe(ctx context.Context, videoID string) (<-chan TranscribeEvent, error)
	ExtractEmbeddedSubtitle(ctx context.Context, videoID string, streamIndex int) (string, error)
	ListBackends(ctx context.Context) ([]Backend, error)
	STTTest(ctx context.Context, backend string, config map[string]any) (any, error)
	HealthCheck(ctx context.Context) (Status, error)
}

// Config bundles dial + retry knobs.
type Config struct {
	Addr              string
	EmbedTimeout      time.Duration
	TranscribeTimeout time.Duration
	MaxRetries        int
	CircuitWindow     time.Duration
	CircuitOpenTime   time.Duration
	FailureThreshold  float64
}

// DefaultConfig is the canonical knob set.
func DefaultConfig() Config {
	return Config{
		EmbedTimeout:      5 * time.Second,
		TranscribeTimeout: 30 * time.Second,
		MaxRetries:        3,
		CircuitWindow:     30 * time.Second,
		CircuitOpenTime:   10 * time.Second,
		FailureThreshold:  0.5,
	}
}

// Breaker is the sliding-window failure tracker. Exported so tests can
// drive it directly without spinning up a gRPC stack.
type Breaker struct {
	mu        sync.Mutex
	window    time.Duration
	openFor   time.Duration
	threshold float64
	openedAt  time.Time
	history   []breakerEvent
}

type breakerEvent struct {
	ts time.Time
	ok bool
}

// NewBreaker constructs a Breaker with the provided knobs.
func NewBreaker(window, openFor time.Duration, threshold float64) *Breaker {
	return &Breaker{window: window, openFor: openFor, threshold: threshold}
}

// Allow returns true if a new call may proceed. If the breaker is open
// it returns false until openFor has elapsed.
func (b *Breaker) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.openedAt.IsZero() {
		if now.Sub(b.openedAt) < b.openFor {
			return false
		}
		// half-open: clear and let one through
		b.openedAt = time.Time{}
		b.history = nil
	}
	return true
}

// Record updates the failure history and trips the breaker if the
// failure rate over the window exceeds the threshold.
func (b *Breaker) Record(now time.Time, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cutoff := now.Add(-b.window)
	trimmed := b.history[:0]
	for _, e := range b.history {
		if e.ts.After(cutoff) {
			trimmed = append(trimmed, e)
		}
	}
	trimmed = append(trimmed, breakerEvent{ts: now, ok: ok})
	b.history = trimmed
	if len(b.history) < 10 { // need a minimum sample to trip
		return
	}
	bad := 0
	for _, e := range b.history {
		if !e.ok {
			bad++
		}
	}
	rate := float64(bad) / float64(len(b.history))
	if rate >= b.threshold {
		b.openedAt = now
	}
}

// CallWithRetry runs fn with the configured retry budget and the
// breaker. Returns the first non-retryable error or success.
//
// This helper is exported so the streaming client wrapper can reuse
// the same shape.
func CallWithRetry(ctx context.Context, b *Breaker, maxRetries int, fn func(context.Context) error) error {
	now := time.Now()
	if !b.Allow(now) {
		return ErrCircuitOpen
	}
	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		err := fn(ctx)
		b.Record(time.Now(), err == nil)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) {
			return err
		}
		// jittered exponential backoff: 50 ms, 100 ms, 200 ms …
		backoff := time.Duration(1<<i)*50*time.Millisecond + time.Duration(rand.Int63n(int64(50*time.Millisecond)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return lastErr
}

// isRetryable matches the error code vocabulary from the gRPC client:
// UNAVAILABLE and DEADLINE_EXCEEDED retry; INTERNAL / INVALID_ARGUMENT
// surface immediately.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if contains(s, "UNAVAILABLE") || contains(s, "DEADLINE_EXCEEDED") {
		return true
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
