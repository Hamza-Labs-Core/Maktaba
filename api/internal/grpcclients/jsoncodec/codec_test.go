package jsoncodec

import (
	"reflect"
	"testing"

	"google.golang.org/grpc/encoding"
)

func TestCodec_RoundTrip(t *testing.T) {
	c := Codec{}
	in := map[string]any{
		"text":         "hello",
		"stream_index": float64(3),
		"nested":       map[string]any{"a": float64(1)},
	}
	b, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out map[string]any
	if err := c.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch: in=%v out=%v", in, out)
	}
}

func TestCodec_Name(t *testing.T) {
	if got := (Codec{}).Name(); got != "json" {
		t.Fatalf("Name()=%q want json", got)
	}
}

func TestCodec_EmptyUnmarshalIsNoop(t *testing.T) {
	var out map[string]any
	if err := (Codec{}).Unmarshal(nil, &out); err != nil {
		t.Fatalf("Unmarshal(nil): %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil map, got %v", out)
	}
}

// TestCodec_SatisfiesGRPCInterface pins the codec to the gRPC
// encoding.Codec contract so a grpc upgrade that changes the
// interface fails here rather than at a call site.
func TestCodec_SatisfiesGRPCInterface(t *testing.T) {
	var _ encoding.Codec = Codec{}
}
