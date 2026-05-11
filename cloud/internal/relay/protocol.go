// Package relay implements the WebSocket tunnel between an on-prem
// Maktaba server and the cloud edge.
//
// The wire protocol is a multiplexed binary frame format that carries
// HTTP request/response pairs and (later) gRPC bidirectional streams.
// Each frame has the shape:
//
//	┌─────────┬─────────────┬──────────────┬─────────┐
//	│ kind(1) │ stream_id(4)│ length(4 be) │ payload │
//	└─────────┴─────────────┴──────────────┴─────────┘
//
// Frame kinds:
//
//	0x01  REQUEST_HEAD    payload = HTTP/1.1-shaped request preamble
//	0x02  REQUEST_BODY    payload = raw bytes (length 0 = EOF)
//	0x03  RESPONSE_HEAD   payload = HTTP/1.1-shaped response preamble
//	0x04  RESPONSE_BODY   payload = raw bytes (length 0 = EOF)
//	0x05  PING            payload = empty
//	0x06  PONG            payload = empty
//	0x07  CLOSE_STREAM    payload = error message (utf-8) or empty
//	0x08  AUTH            payload = JSON `{"server_id":"...","secret":"..."}`
//	0x09  AUTH_OK         payload = JSON `{"slug":"...", "issued_at":"..."}`
//	0x0a  AUTH_FAIL       payload = JSON `{"error":"..."}`
//	0x0b  HEARTBEAT       payload = JSON server stats; mirrors POST /heartbeat
//
// We size length as uint32 (≤4 GiB per frame); single-frame upper bound
// is enforced at 16 MiB to keep memory bounded under hostile streams.
package relay

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	KindRequestHead  byte = 0x01
	KindRequestBody  byte = 0x02
	KindResponseHead byte = 0x03
	KindResponseBody byte = 0x04
	KindPing         byte = 0x05
	KindPong         byte = 0x06
	KindCloseStream  byte = 0x07
	KindAuth         byte = 0x08
	KindAuthOK       byte = 0x09
	KindAuthFail     byte = 0x0a
	KindHeartbeat    byte = 0x0b
)

// MaxFrameBytes caps a single payload. 16 MiB lets normal HTML/JSON
// responses through while preventing a misbehaving (or hostile) server
// from forcing the cloud to allocate 4 GiB chunks. Larger payloads use
// fragmentation via BODY frames.
const MaxFrameBytes = 16 * 1024 * 1024

// Frame is the in-memory representation. We do not pool payload slices
// here — the read path returns a fresh slice each call to match the
// expectations of the chi response writer.
type Frame struct {
	Kind     byte
	StreamID uint32
	Payload  []byte
}

var (
	ErrFrameTooLarge = errors.New("relay: frame exceeds MaxFrameBytes")
	ErrShortRead     = errors.New("relay: short read")
)

// ReadFrame parses a single frame from r. Returns io.EOF when the
// underlying reader is at end-of-stream cleanly.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [9]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(hdr[5:9])
	if int64(length) > int64(MaxFrameBytes) {
		return Frame{}, ErrFrameTooLarge
	}
	f := Frame{
		Kind:     hdr[0],
		StreamID: binary.BigEndian.Uint32(hdr[1:5]),
		Payload:  make([]byte, length),
	}
	if length > 0 {
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			return Frame{}, err
		}
	}
	return f, nil
}

// WriteFrame serializes a frame to w. Returns ErrFrameTooLarge if the
// payload would not fit in the 32-bit length field bound.
func WriteFrame(w io.Writer, f Frame) error {
	if len(f.Payload) > MaxFrameBytes {
		return ErrFrameTooLarge
	}
	var hdr [9]byte
	hdr[0] = f.Kind
	binary.BigEndian.PutUint32(hdr[1:5], f.StreamID)
	binary.BigEndian.PutUint32(hdr[5:9], uint32(len(f.Payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(f.Payload) > 0 {
		if _, err := w.Write(f.Payload); err != nil {
			return err
		}
	}
	return nil
}
