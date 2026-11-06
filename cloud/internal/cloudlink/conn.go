package cloudlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
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

// CloseCode ends the websocket with a close code (4000 normal_closure for a
// drained tunnel, story 50.03), then closes the socket.
func (w *wsFrameConn) CloseCode(code int, text string) error {
	_ = w.c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(time.Second))
	return w.c.Close()
}

// closeCoder is a FrameConn that can send a close code.
type closeCoder interface {
	CloseCode(code int, text string) error
}

// authPayload is the body of the first frame the on-prem server sends.
// Field shape is dictated by relay.AuthPayload (server side):
// {"server_id":"...","secret":"...","flow":1}; flow announces that this
// agent windows its response bodies (WINDOW_UPDATE, relay/protocol.go).
//
// Ticket replaces Secret when the agent holds a resume ticket (story 50.03);
// Features announces drain and ticket support.
type authPayload struct {
	ServerID string   `json:"server_id"`
	Secret   string   `json:"secret,omitempty"`
	Ticket   string   `json:"ticket,omitempty"`
	Flow     int      `json:"flow,omitempty"`
	Features []string `json:"features,omitempty"`
}

// authOKPayload is what the cloud echoes on success (relay.AuthOKPayload).
type authOKPayload struct {
	Slug     string    `json:"slug"`
	IssuedAt time.Time `json:"issued_at"`
	// Flow is FlowVersion when the cloud agreed to window the tunnel; an
	// older cloud omits it and the tunnel stays unwindowed.
	Flow int `json:"flow,omitempty"`
	// Ticket is a first resume ticket (the cloud agreed to tickets).
	Ticket string `json:"ticket,omitempty"`
}

var (
	// ErrAuthRejected is returned when the cloud answers AUTH with
	// AUTH_FAIL (bad creds, protocol error, unknown server).
	ErrAuthRejected = errors.New("cloudlink: cloud rejected AUTH")
	// ErrAuthProtocol is returned when the cloud's first reply is not a
	// well-formed AUTH_OK / AUTH_FAIL frame.
	ErrAuthProtocol = errors.New("cloudlink: unexpected AUTH reply")
	// ErrTicketRefused: the cloud refused the resume ticket (expired, or
	// signed with a rotated secret); the agent retries with its bearer.
	ErrTicketRefused = errors.New("cloudlink: resume ticket refused")
)

// ticketJar holds the latest resume ticket, in memory only: after a restart
// the agent has none and uses its bearer secret.
type ticketJar struct {
	mu sync.Mutex
	v  string
}

func (j *ticketJar) get() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.v
}

func (j *ticketJar) set(v string) {
	j.mu.Lock()
	j.v = v
	j.mu.Unlock()
}

// DialResult carries the live connection and the slug the cloud
// confirmed for this server.
type DialResult struct {
	Conn FrameConn
	Slug string
	// Flow: the cloud agreed to flow control (both ends announced it).
	Flow bool
	// Ticket: a first resume ticket from AUTH_OK, if the cloud sent one.
	Ticket string
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

	tickets ticketJar
	// legacy: announce no features (LegacyAgent)
	legacy bool
}

// Ticket is the resume ticket the agent holds ("" none).
func (d *Dialer) Ticket() string { return d.tickets.get() }

// SetTicket keeps a resume ticket the cloud sent (TICKET, DRAIN).
func (d *Dialer) SetTicket(t string) { d.tickets.set(t) }

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
	once := func(ticket string) (DialResult, error) {
		dctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		c, _, err := wd.DialContext(dctx, d.Endpoint, nil)
		if err != nil {
			return DialResult{}, fmt.Errorf("cloudlink: dial %s: %w", d.Endpoint, err)
		}
		conn := &wsFrameConn{c: c}
		features := agentFeatures
		if d.legacy {
			features = nil
		}
		res, err := authenticateWith(conn, d.ServerID, d.Secret, ticket, features)
		if err != nil {
			_ = conn.Close()
			return DialResult{}, err
		}
		return res, nil
	}
	ticket := d.Ticket()
	res, err := once(ticket)
	if ticket != "" && errors.Is(err, ErrTicketRefused) {
		// the ticket is no good: forget it and retry at once with the bearer
		d.SetTicket("")
		res, err = once("")
	}
	if err == nil && res.Ticket != "" {
		d.SetTicket(res.Ticket)
	}
	return res, err
}

// authenticate performs the AUTH → AUTH_OK exchange over an arbitrary
// FrameConn. Factored out so unit tests can drive it with a pipe and
// no real network.
//
// A non-empty ticket is presented instead of the secret (no argon2id check on
// the cloud); a refused ticket is ErrTicketRefused.
func authenticate(conn FrameConn, serverID, secret, ticket string) (DialResult, error) {
	return authenticateWith(conn, serverID, secret, ticket, agentFeatures)
}

func authenticateWith(conn FrameConn, serverID, secret, ticket string, features []string) (DialResult, error) {
	ap := authPayload{ServerID: serverID, Secret: secret, Flow: FlowVersion, Features: features}
	if ticket != "" {
		ap.Secret, ap.Ticket = "", ticket
	}
	body, _ := json.Marshal(ap)
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
		return DialResult{Conn: conn, Slug: ok.Slug, Flow: ok.Flow >= FlowVersion, Ticket: ok.Ticket}, nil
	case KindAuthFail:
		if ticket != "" && bytes.Contains(reply.Payload, []byte("ticket_invalid")) {
			return DialResult{}, fmt.Errorf("%w: %s", ErrTicketRefused, string(reply.Payload))
		}
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
