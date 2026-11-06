package grpcsrv

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Epic 53 story 53.08: the api's request id in the call's metadata is on the handler's
// context, so its lines carry it.
func TestRequestIDInterceptor(t *testing.T) {
	var got context.Context
	h := func(ctx context.Context, _ any) (any, error) { got = ctx; return nil, nil }
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(RequestIDMetadata, "01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60"))
	if _, err := RequestIDInterceptor(ctx, nil, &grpc.UnaryServerInfo{}, h); err != nil {
		t.Fatal(err)
	}
	var buf captured
	l := mlog.NewLogger(mlog.Options{Service: "streaming", Env: "prod", Output: &buf})
	l.InfoContext(got, "stream_session_opened")
	if !contains(buf.s, `"request_id":"01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60"`) {
		t.Fatalf("line: %s", buf.s)
	}
}

type captured struct{ s string }

func (c *captured) Write(p []byte) (int, error) { c.s += string(p); return len(p), nil }

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
