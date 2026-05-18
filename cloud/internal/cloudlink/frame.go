// Package cloudlink is the on-prem → cloud tunnel client (Story 25.7).
//
// It is the missing counterpart to cloud/internal/relay: a self-hosted
// Maktaba box dials the cloud edge over WSS, authenticates, then
// frame-multiplexes inbound HTTP requests to its own loopback API and
// streams responses back. The wire format and frame kinds are reused
// VERBATIM from cloud/internal/relay so the two ends are guaranteed
// byte-for-byte compatible — there is no second protocol definition to
// drift.
//
// Scope note (honest deferral): the spec (Story 25.7) also calls for
// frame kinds 0x20 REVOKE and 0x21 ENT_REFRESH. The cloud relay server
// in this repo (cloud/internal/relay/protocol.go) defines no such
// kinds and never emits them, so a client that "handled" them would be
// handling frames the server cannot send. We implement the protocol
// the cloud ACTUALLY speaks (kinds 0x01–0x0b) and document the rest as
// a server-side gap, not a client stub.
package cloudlink

import (
	"io"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// Frame is the relay wire frame. We alias rather than redefine so the
// client and server share one struct and one codec.
type Frame = relay.Frame

// Frame kinds, re-exported from the relay package. Listed explicitly so
// callers in this package read naturally and so a compile error fires
// here if the relay package ever renumbers a kind.
const (
	KindRequestHead  = relay.KindRequestHead
	KindRequestBody  = relay.KindRequestBody
	KindResponseHead = relay.KindResponseHead
	KindResponseBody = relay.KindResponseBody
	KindPing         = relay.KindPing
	KindPong         = relay.KindPong
	KindCloseStream  = relay.KindCloseStream
	KindAuth         = relay.KindAuth
	KindAuthOK       = relay.KindAuthOK
	KindAuthFail     = relay.KindAuthFail
	KindHeartbeat    = relay.KindHeartbeat
)

// MaxFrameBytes mirrors the server's single-frame cap so the client
// never emits a frame the cloud will reject.
const MaxFrameBytes = relay.MaxFrameBytes

// ReadFrame / WriteFrame delegate to the shared relay codec.
func ReadFrame(r io.Reader) (Frame, error)  { return relay.ReadFrame(r) }
func WriteFrame(w io.Writer, f Frame) error { return relay.WriteFrame(w, f) }
