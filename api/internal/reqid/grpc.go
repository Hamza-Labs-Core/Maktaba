package reqid

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// UnaryClientInterceptor puts the request id of the call's context in its metadata
// (MetadataKey), so the pipeline and streaming log their part of the request under the
// same id (Epic 53 story 53.08). A call with no request id goes as it was.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption) error {
		return invoker(Outgoing(ctx), method, req, reply, cc, opts...)
	}
}

// Outgoing is ctx with its request id in the outgoing gRPC metadata.
func Outgoing(ctx context.Context) context.Context {
	if id := FromContext(ctx); id != uuid.Nil {
		return metadata.AppendToOutgoingContext(ctx, MetadataKey, id.String())
	}
	return ctx
}
