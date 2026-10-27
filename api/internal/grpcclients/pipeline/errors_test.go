package pipeline

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
)

func TestBreakerLogsWhenItOpensAndHalfOpens(t *testing.T) {
	var buf bytes.Buffer
	b := NewBreaker(30*time.Second, 10*time.Second, 0.5)
	b.Name = "pipeline 127.0.0.1:50051"
	b.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	now := time.Now()
	for i := 0; i < 15; i++ {
		b.Record(now, false)
	}
	out := buf.String()
	if strings.Count(out, "event=grpc_circuit_open") != 1 {
		t.Fatalf("want one grpc_circuit_open line, got %q", out)
	}
	if !strings.Contains(out, `service="pipeline 127.0.0.1:50051"`) {
		t.Fatalf("the line does not say which service: %q", out)
	}
	if !b.Allow(now.Add(11 * time.Second)) {
		t.Fatal("breaker must let a probe through after openFor")
	}
	if !strings.Contains(buf.String(), "event=grpc_circuit_half_open") {
		t.Fatalf("no half-open line: %q", buf.String())
	}
}

func TestIsRetryableReadsTheGRPCStatus(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{status.Error(codes.Unavailable, "connection refused"), true},
		{status.Error(codes.DeadlineExceeded, "slow"), true},
		{status.Error(codes.Internal, "boom"), false},
		{status.Error(codes.InvalidArgument, "bad"), false},
		{errors.New("rpc error: code = UNAVAILABLE"), true},
		{errors.New("plain"), false},
	}
	for _, c := range cases {
		if got := isRetryable(c.err); got != c.want {
			t.Errorf("isRetryable(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestCallErrorNamesMethodAndTarget(t *testing.T) {
	err := error(&CallError{Method: methodEmbed, Target: "127.0.0.1:50051", Err: ErrCircuitOpen})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatal("CallError must unwrap to its cause")
	}
	if s := err.Error(); !strings.Contains(s, methodEmbed) || !strings.Contains(s, "127.0.0.1:50051") {
		t.Fatalf("error text lacks method or target: %q", s)
	}
}

func TestRealClientTransportErrorIsACallError(t *testing.T) {
	c := NewRealClient(Config{Addr: "127.0.0.1:1", EmbedTimeout: 200 * time.Millisecond,
		CircuitWindow: time.Second, CircuitOpenTime: time.Second, FailureThreshold: 0.99})
	_, err := c.Embed(context.Background(), "hi")
	var ce *CallError
	if !errors.As(err, &ce) || ce.Method != methodEmbed || ce.Target != "127.0.0.1:1" {
		t.Fatalf("want a CallError for Embed at 127.0.0.1:1, got %v", err)
	}
}

func TestOutgoingRequestIDFallsBackToTheHTTPRequest(t *testing.T) {
	id := uuid.New()
	ctx := reqid.WithID(context.Background(), id)
	if got := OutgoingRequestID(ctx, requestIDKey); got != id.String() {
		t.Fatalf("got %q, want the HTTP request id", got)
	}
	in := metadata.NewIncomingContext(ctx, metadata.Pairs(requestIDKey, "from-grpc"))
	if got := OutgoingRequestID(in, requestIDKey); got != "from-grpc" {
		t.Fatalf("an inbound gRPC id wins: got %q", got)
	}
	if got := OutgoingRequestID(context.Background(), requestIDKey); got != "" {
		t.Fatalf("no id: got %q", got)
	}
}
