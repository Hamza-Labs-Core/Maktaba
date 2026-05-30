package cloudlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// FrameConn is the transport the multiplexer drives. A test fake
// injects an in-memory pipe; production uses *wsFrameConn.
//
// It mirrors relay.FrameConn (server side) exactly so the same stream
// semantics apply to both ends.
type FrameConn interface {
	ReadFrame() (Frame, error)
	WriteFrame(Frame) error
	Close() error
}

// wsFrameConn adapts a *websocket.Conn. The cloud relay (cloud/internal/
// relay/ws.go) puts exactly one wire frame in each binary websocket
// message; we match that framing precisely.
type wsFrameConn struct{ c *websocket.Conn }

func (w *wsFrameConn) ReadFrame() (Frame, error) {
	_, data, err := w.c.ReadMessage()
	if err != nil {
		return Frame{}, err
	}
	return ReadFrame(bytes.NewReader(data))
}

func (w *wsFrameConn) WriteFrame(f Frame) error {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, f); err != nil {
		return err
	}
	return w.c.WriteMessage(websocket.BinaryMessage, buf.Bytes())
}

func (w *wsFrameConn) Close() error { return w.c.Close() }

// authPayload is the body of the first frame the on-prem server sends.
// Field shape is dictated by relay.AuthPayload (server side):
// {"server_id":"...","secret":"..."}.
type authPayload struct {
	ServerID string `json:"server_id"`
	Secret   string `json:"secret"`
}

// authOKPayload is what the cloud echoes on success (relay.AuthOKPayload).
type authOKPayload struct {
	Slug     string    `json:"slug"`
	IssuedAt time.Time `json:"issued_at"`
}

var (
	// ErrAuthRejected is returned when the cloud answers AUTH with
	// AUTH_FAIL (bad creds, protocol error, unknown server).
	ErrAuthRejected = errors.New("cloudlink: cloud rejected AUTH")
	// ErrAuthProtocol is returned when the cloud's first reply is not a
	// well-formed AUTH_OK / AUTH_FAIL frame.
	ErrAuthProtocol = errors.New("cloudlink: unexpected AUTH reply")
)

// DialResult carries the live connection and the slug the cloud
// confirmed for this server.
type DialResult struct {
	Conn FrameConn
	Slug string
}

// Dialer opens a WSS tunnel to the cloud relay and performs the AUTH
// handshake. It is deliberately small and injectable so tests can
// substitute an in-memory FrameConn (see dialFunc on the Client).
type Dialer struct {
	// Endpoint is the full ws:// or wss:// URL of the relay accept
	// handler. The cloud mounts ServeWS at /v1/relay/ws (see
	// cloud/cmd/maktaba-cloud/role_relay.go); the spec's
	// /tunnel/v1/connect path is NOT what the server listens on, so we
	// dial the real path and document the divergence.
	Endpoint string
	// ServerID / Secret are the credentials returned by claim redeem
	// (see claim.go). The cloud verifies secret via argon2id against
	// servers.server_secret_hash (relay/ws.go lookupServerForAuth).
	ServerID string
	Secret   string
	// HandshakeTimeout bounds the dial + AUTH round trip. Story 25.7
	// requires connect within 5s of start; default 5s.
	HandshakeTimeout time.Duration
	// WS is the underlying websocket dialer; nil uses a default.
	WS *websocket.Dialer
}

// Dial connects, sends the AUTH frame, and waits for AUTH_OK. On
// AUTH_FAIL or a malformed reply it closes the socket and returns an
// error so the caller's backoff loop can retry.
func (d *Dialer) Dial(ctx context.Context) (DialResult, error) {
	timeout := d.HandshakeTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	wd := d.WS
	if wd == nil {
		wd = &websocket.Dialer{HandshakeTimeout: timeout}
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c, _, err := wd.DialContext(dctx, d.Endpoint, nil)
	if err != nil {
		return DialResult{}, fmt.Errorf("cloudlink: dial %s: %w", d.Endpoint, err)
	}
	conn := &wsFrameConn{c: c}
	res, err := authenticate(conn, d.ServerID, d.Secret)
	if err != nil {
		_ = conn.Close()
		return DialResult{}, err
	}
	return res, nil
}

// authenticate performs the AUTH → AUTH_OK exchange over an arbitrary
// FrameConn. Factored out so unit tests can drive it with a pipe and
// no real network.
func authenticate(conn FrameConn, serverID, secret string) (DialResult, error) {
	body, _ := json.Marshal(authPayload{ServerID: serverID, Secret: secret})
	if err := conn.WriteFrame(Frame{Kind: KindAuth, Payload: body}); err != nil {
		return DialResult{}, fmt.Errorf("cloudlink: write AUTH: %w", err)
	}
	reply, err := conn.ReadFrame()
	if err != nil {
		return DialResult{}, fmt.Errorf("cloudlink: read AUTH reply: %w", err)
	}
	switch reply.Kind {
	case KindAuthOK:
		var ok authOKPayload
		if err := json.Unmarshal(reply.Payload, &ok); err != nil {
			return DialResult{}, fmt.Errorf("%w: bad AUTH_OK json: %v", ErrAuthProtocol, err)
		}
		return DialResult{Conn: conn, Slug: ok.Slug}, nil
	case KindAuthFail:
		return DialResult{}, fmt.Errorf("%w: %s", ErrAuthRejected, string(reply.Payload))
	default:
		return DialResult{}, fmt.Errorf("%w: kind %#x", ErrAuthProtocol, reply.Kind)
	}
}

// httpEndpointFromWS converts the relay ws:// or wss:// endpoint into
// the http(s):// base used for the (separate) claim REST call. Exposed
// for the cmd wiring.
func httpEndpointFromWS(ws string) string {
	switch {
	case len(ws) >= 6 && ws[:6] == "wss://":
		return "https://" + ws[6:]
	case len(ws) >= 5 && ws[:5] == "ws://":
		return "http://" + ws[5:]
	default:
		return ws
	}
}
