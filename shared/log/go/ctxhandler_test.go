package log

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestContextLoggingAddsTheRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(Options{Service: "api", Env: "prod", Version: "v0", Output: &buf})
	ctx := WithJobID(WithRequestID(context.Background(), "req-1"), "job-9")

	logger.WarnContext(ctx, "store write failed", "event", "x")
	got := decodeOne(t, buf.Bytes())
	if got["request_id"] != "req-1" || got["job_id"] != "job-9" {
		t.Fatalf("ids missing from a context line: %v", got)
	}

	// From(ctx) carries the id already: it is not written twice
	buf.Reset()
	prev := Default
	Default = logger
	t.Cleanup(func() { Default = prev })
	From(ctx).WarnContext(ctx, "again")
	if n := strings.Count(buf.String(), `"request_id"`); n != 1 {
		t.Fatalf("request_id written %d times: %s", n, buf.String())
	}

	// no context, no ids
	buf.Reset()
	logger.Warn("plain")
	if strings.Contains(buf.String(), "request_id") {
		t.Fatalf("an id without a context: %s", buf.String())
	}
}
