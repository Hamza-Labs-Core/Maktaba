package pipeline

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/jsoncodec"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
)

// Epic 53 story 53.08: every call carries the request's id in its metadata, for the other
// side's lines.
func TestRealClientSendsTheRequestID(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	srv := grpc.NewServer(grpc.ForceServerCodec(jsoncodec.Codec{}),
		grpc.UnknownServiceHandler(func(_ any, ss grpc.ServerStream) error {
			md, _ := metadata.FromIncomingContext(ss.Context())
			v := md.Get(reqid.MetadataKey)
			if len(v) == 0 {
				v = []string{""}
			}
			got <- v[0]
			var req map[string]any
			_ = ss.RecvMsg(&req)
			return ss.SendMsg(map[string]any{})
		}))
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	conn := NewRealClient(Config{Addr: lis.Addr().String()}).(*realClient).conn
	defer conn.Close()
	id := uuid.Must(uuid.NewV7())
	ctx, cancel := context.WithTimeout(reqid.WithID(context.Background(), id), 5*time.Second)
	defer cancel()
	var out map[string]any
	_ = conn.Invoke(ctx, "/any.Service/Call", map[string]any{}, &out)
	select {
	case v := <-got:
		if v != id.String() {
			t.Fatalf("metadata %q, want %q", v, id)
		}
	case <-ctx.Done():
		t.Fatal("no call arrived")
	}
}
