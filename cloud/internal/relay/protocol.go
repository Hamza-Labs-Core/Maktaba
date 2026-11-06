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
//	0x0c  WINDOW_UPDATE   payload = uint32 BE: RESPONSE_BODY bytes the cloud grants
//	0x22  DRAIN           payload = JSON DrainPayload: open a new tunnel, then let this one
//	                      finish its streams (story 50.03)
//	0x23  TICKET          payload = JSON TicketPayload: a fresh resume ticket
//
// DRAIN and TICKET are sent only to a home that announced the features
// "drain" and "ticket" in AUTH; an older home never sees them (a frame kind
// it does not know is ignored by its dispatch anyway).
//
// Flow control (WINDOW_UPDATE) is negotiated: the agent sends "flow":1 in
// AUTH and the cloud answers "flow":1 in AUTH_OK when it agrees. On such a
// tunnel the home sends at most InitialWindow bytes of RESPONSE_BODY
// payload per stream beyond what the cloud has granted back; the cloud
// grants as its client reads. Without it (an older agent or cloud) the
// stream is unwindowed, as before.
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
	KindWindowUpdate byte = 0x0c
	KindDrain        byte = 0x22
	KindTicket       byte = 0x23
)

// Tunnel features a home announces in AUTH ("features"), story 50.03.
const (
	// FeatureDrain: the home understands DRAIN (make before break).
	FeatureDrain = "drain"
	// FeatureTicket: the home keeps resume tickets and presents them.
	FeatureTicket = "ticket"
)

// Drain reasons (DrainPayload.Reason).
const (
	DrainDeploy    = "deploy"
	DrainRebalance = "rebalance"
	DrainShutdown  = "shutdown"
)

// DrainPayload asks the home to move: wait a random time in [0, WithinMS),
// dial a new tunnel (presenting Ticket), then close this one with 4000 once
// it has no streams, or at DeadlineMS, whichever is first.
type DrainPayload struct {
	Reason     string `json:"reason"`
	WithinMS   int64  `json:"within_ms"`
	DeadlineMS int64  `json:"deadline_ms"`
	Ticket     string `json:"ticket,omitempty"`
}

// TicketPayload carries a fresh resume ticket (sent every TicketRefresh).
type TicketPayload struct {
	Ticket string `json:"ticket"`
}

// CloseNormal is the websocket close code a home ends a drained tunnel with.
const CloseNormal = 4000

// InitialWindow is the RESPONSE_BODY credit (bytes) each stream starts
// with on a flow-controlled tunnel: what the home may send before the
// cloud grants more.
const InitialWindow = 1 << 20

// FlowVersion is the flow-control version both ends announce ("flow" in
// AUTH and AUTH_OK).
const FlowVersion = 1

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
