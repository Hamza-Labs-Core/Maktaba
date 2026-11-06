package reqid

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Epic 53 story 53.08: a gRPC call made for a request carries its id in the metadata.
func TestUnaryClientInterceptorCarriesTheID(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	var got []string
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(ctx)
		got = md.Get(MetadataKey)
		return nil
	}
	_ = UnaryClientInterceptor()(WithID(context.Background(), id), "/m", nil, nil, nil, invoker)
	if len(got) != 1 || got[0] != id.String() {
		t.Fatalf("metadata %v, want %s", got, id)
	}
	got = nil
	_ = UnaryClientInterceptor()(context.Background(), "/m", nil, nil, nil, invoker)
	if len(got) != 0 {
		t.Fatalf("no id, no metadata: %v", got)
	}
}
