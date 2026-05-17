// Package jsoncodec is a gRPC codec that marshals messages with
// encoding/json instead of protobuf.
//
// The pipeline service (pipeline/src/maktaba_pipeline/grpc_server.py)
// runs a generic gRPC server with an identity (raw-bytes) serializer
// where each request/response is a JSON-encoded flat dictionary. To
// talk to it without a protobuf toolchain we register a codec named
// "json" that simply runs encoding/json over the message value. The
// streaming gRPC server (streaming/internal/grpcsrv) uses the same
// convention from the server side; the api module duplicates this
// ~10-line codec into both client packages because api and streaming
// are separate Go modules (see go.mod) and must not cross-import.
//
// Force this codec per-call with
// grpc.WithDefaultCallOptions(grpc.ForceCodec(jsoncodec.Codec{})) so
// the dial does not require the global codec registry.
package jsoncodec

import "encoding/json"

// Name is the codec's wire name. gRPC sends it in the
// grpc-encoding/content-subtype; the pipeline/streaming generic
// servers ignore it (identity bytes), so any stable value works.
const Name = "json"

// Codec marshals/unmarshals gRPC message values with encoding/json.
// It implements google.golang.org/grpc/encoding.Codec.
type Codec struct{}

// Marshal returns the JSON encoding of v.
func (Codec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal parses the JSON-encoded data into v.
func (Codec) Unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

// Name returns the codec name ("json").
func (Codec) Name() string { return Name }
