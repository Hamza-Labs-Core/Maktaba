package grpcsrv

import "encoding/json"

// jsonCodec is a gRPC codec that marshals messages with encoding/json
// instead of protobuf, matching the convention the pipeline gRPC
// server established (JSON-encoded flat dicts over an identity bytes
// serializer). This ~10-line codec is duplicated here rather than
// shared because streaming and api are separate Go modules (see the
// two go.mod files) and must not cross-import.
//
// It implements google.golang.org/grpc/encoding.Codec.
type jsonCodec struct{}

const jsonCodecName = "json"

func (jsonCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (jsonCodec) Unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

func (jsonCodec) Name() string { return jsonCodecName }
