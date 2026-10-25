// Package link is the cluster link (Epic 35 story 35.16, design §18): machine-to-home
// traffic over WebSockets on mutual TLS, on the home API's existing port.
//
// Each machine keeps one control channel (GET /api/cluster/link) that authenticates
// first — the TLS client certificate the home's cluster CA gave it, bound to its node id —
// and then carries small requests (the node's home routes, session checks), commands,
// certificate renewals and stream tickets. Every data stream (a release or wheelhouse
// file, a backup, a proxied WebSocket, a TV pull the home asks for) opens its own wss://
// connection (GET /api/cluster/link/stream) with a single-use ticket issued on the
// control channel, so a busy stream never delays another one or the control channel.
//
// A data stream is a byte pipe (one binary WebSocket message per chunk, at most 64 KiB)
// that carries plain HTTP/1.1: the side that asked for the bytes writes a request on it,
// the other serves it with an ordinary http.Handler (the home's API, or the node's
// streaming port), so every HTTP behaviour — status, headers, cookies, streamed bodies,
// upgrades — is kept unchanged.
//
// A full node's API process holds the machine's control channel (Client); the node's
// supervisor and pipeline processes reach the home through its loopback endpoints
// (local.go). The home's API serves the channel and the streams (Server).
package link

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Frame kinds on the control channel (design §18.4).
const (
	KindReq    byte = 1
	KindResp   byte = 2
	KindChunk  byte = 3
	KindEnd    byte = 4
	KindCancel byte = 5
	KindEvent  byte = 6
)

// Classes: control frames (commands, cert.*, tickets, offers) are always sent before rpc
// frames (proxied requests and their answers).
const (
	ClassControl byte = 0
	ClassRPC     byte = 1
)

// Sizes.
const (
	// MaxPayload is a control frame's largest payload.
	MaxPayload = 16 << 10
	// HeadSize is a frame's header: id u32, kind u8, class u8, len u32.
	HeadSize = 10
	// MaxInline is the largest answer (or request body) carried on the control channel;
	// beyond it the rest moves to a data stream (svc_body).
	MaxInline = 1 << 20
	// MaxChunk is a data stream's largest message.
	MaxChunk = 64 << 10
)

// Frame is one control-channel message.
type Frame struct {
	ID      uint32
	Kind    byte
	Class   byte
	Payload []byte
}

// Errors of the framing.
var (
	ErrFrameTooLarge = errors.New("link: frame payload over 16 KiB")
	ErrBadFrame      = errors.New("link: malformed frame")
)

// Encode is the frame's wire form.
func (f Frame) Encode() ([]byte, error) {
	if len(f.Payload) > MaxPayload {
		return nil, ErrFrameTooLarge
	}
	if f.Class != ClassControl && f.Class != ClassRPC {
		return nil, fmt.Errorf("%w: class %d", ErrBadFrame, f.Class)
	}
	b := make([]byte, HeadSize+len(f.Payload))
	binary.BigEndian.PutUint32(b[0:4], f.ID)
	b[4], b[5] = f.Kind, f.Class
	binary.BigEndian.PutUint32(b[6:10], uint32(len(f.Payload))) //nolint:gosec // at most 16 KiB
	copy(b[HeadSize:], f.Payload)
	return b, nil
}

// Decode reads one frame from a binary message.
func Decode(b []byte) (Frame, error) {
	if len(b) < HeadSize {
		return Frame{}, ErrBadFrame
	}
	n := binary.BigEndian.Uint32(b[6:10])
	if n > MaxPayload {
		return Frame{}, ErrFrameTooLarge
	}
	if int(n) != len(b)-HeadSize {
		return Frame{}, ErrBadFrame
	}
	f := Frame{ID: binary.BigEndian.Uint32(b[0:4]), Kind: b[4], Class: b[5]}
	if f.Kind < KindReq || f.Kind > KindEvent || (f.Class != ClassControl && f.Class != ClassRPC) {
		return Frame{}, ErrBadFrame
	}
	f.Payload = append([]byte(nil), b[HeadSize:]...)
	return f, nil
}

// split cuts b into payloads of at most MaxPayload bytes.
func split(b []byte) [][]byte {
	var out [][]byte
	for len(b) > MaxPayload {
		out = append(out, b[:MaxPayload])
		b = b[MaxPayload:]
	}
	if len(b) > 0 {
		out = append(out, b)
	}
	return out
}
