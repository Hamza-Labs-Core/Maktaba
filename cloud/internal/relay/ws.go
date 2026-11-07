package relay

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// upgrader is configured generously — server agents may have small
// MTU mid-flight and large payloads; the framing layer above already
// enforces MaxFrameBytes.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	CheckOrigin: func(_ *http.Request) bool {
		return true // server-to-cloud auth is via the AUTH frame; no Origin gate.
	},
}

// Websocket bounds. The agent PINGs every 25s, so a tunnel silent for
// tunnelIdle is dead (half-open TCP) and is closed; a write that cannot
// leave in writeWait means the same.
const (
	tunnelIdle   = 90 * time.Second
	writeWait    = 30 * time.Second
	wsReadLimit  = MaxFrameBytes + 9 + 1024
	authDeadline = 5 * time.Second
)

// wsConn adapts a *websocket.Conn to FrameConn. The tunnel sees a
// stream-of-frames; we wrap each frame in a single binary message.
type wsConn struct{ c *websocket.Conn }

func (w *wsConn) ReadFrame() (Frame, error) {
	_ = w.c.SetReadDeadline(time.Now().Add(tunnelIdle))
	_, data, err := w.c.ReadMessage()
	if err != nil {
		return Frame{}, err
	}
	return ReadFrame(&byteReader{b: data})
}
func (w *wsConn) WriteFrame(f Frame) error {
	_ = w.c.SetWriteDeadline(time.Now().Add(writeWait))
	b, err := encodeFrame(f)
	if err != nil {
		return err
	}
	return w.c.WriteMessage(websocket.BinaryMessage, b)
}
func (w *wsConn) Close() error { return w.c.Close() }

type byteReader struct{ b []byte }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, errEOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

var errEOF = errors.New("EOF")

// encodeFrame puts one frame in one websocket message. A payload over
// MaxFrameBytes is refused before anything is allocated (its length would
// not fit the frame header's bound either).
func encodeFrame(f Frame) ([]byte, error) {
	n := len(f.Payload)
	if n > MaxFrameBytes {
		return nil, ErrFrameTooLarge
	}
	buf := make([]byte, 9) // append grows it: no size computed here
	buf[0] = f.Kind
	binary.BigEndian.PutUint32(buf[1:5], f.StreamID)
	binary.BigEndian.PutUint32(buf[5:9], uint32(n))
	return append(buf, f.Payload...), nil
}

// writeControl writes a small control frame (AUTH_OK / AUTH_FAIL).
func writeControl(c *websocket.Conn, f Frame) error {
	b, err := encodeFrame(f)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.BinaryMessage, b)
}

// AuthPayload is the body of the first frame the on-prem server sends.
// Flow is the agent's flow-control version (FlowVersion; 0 = none).
//
// Ticket (story 50.03) replaces Secret on a reconnect: a resume ticket the
// relay issued, checked by HMAC instead of argon2id. Features lists what the
// home understands (FeatureDrain, FeatureTicket); an older home sends none.
type AuthPayload struct {
	ServerID string   `json:"server_id"`
	Secret   string   `json:"secret,omitempty"`
	Ticket   string   `json:"ticket,omitempty"`
	Flow     int      `json:"flow,omitempty"`
	Features []string `json:"features,omitempty"`
}

func (a AuthPayload) has(feature string) bool {
	for _, f := range a.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// AuthOKPayload echoes back the slug so the agent can confirm we
// matched it correctly. Flow is FlowVersion when the tunnel is windowed
// (the agent asked for it), else absent.
//
// Ticket is a first resume ticket when the home announced FeatureTicket and
// tickets are on; Features what the relay will use on this tunnel.
type AuthOKPayload struct {
	Slug     string    `json:"slug"`
	IssuedAt time.Time `json:"issued_at"`
	Flow     int       `json:"flow,omitempty"`
	Ticket   string    `json:"ticket,omitempty"`
	Features []string  `json:"features,omitempty"`
}

// TunnelStore is what the tunnel accept handler needs from the servers
// table: authenticate an agent, and record its tunnel coming and going.
type TunnelStore interface {
	Authenticate(ctx context.Context, serverID, secret string) (stores.Server, error)
	SetOnline(ctx context.Context, serverID string, online bool) error
}

// ResumeStore is the check a resume ticket needs beyond its HMAC (story
// 50.03): the server still exists and its owner is not suspended. No
// argon2id: that is the point of the ticket.
type ResumeStore interface {
	Resume(ctx context.Context, serverID string) (stores.Server, error)
}

// ErrServerRevoked: the server is gone or its owner suspended.
var ErrServerRevoked = errors.New("relay: server revoked or suspended")

// NewTunnelStore backs TunnelStore with the servers tables.
func NewTunnelStore(servers *stores.Servers) TunnelStore { return dbTunnelStore{s: servers} }

type dbTunnelStore struct{ s *stores.Servers }

func (d dbTunnelStore) Authenticate(ctx context.Context, serverID, secret string) (stores.Server, error) {
	if serverID == "" || secret == "" {
		return stores.Server{}, errors.New("missing creds")
	}
	// We re-query by id; the server_id+secret pair is what authenticates.
	var hash string
	row := d.s.DB.QueryRowContext(ctx, `SELECT server_secret_hash FROM servers WHERE id = $1`, serverID)
	if err := row.Scan(&hash); err != nil {
		return stores.Server{}, err
	}
	if err := argon2id.Verify(secret, hash); err != nil {
		return stores.Server{}, err
	}
	return d.s.BySlugOrID(ctx, serverID)
}

// Resume checks a ticket's server without its secret.
func (d dbTunnelStore) Resume(ctx context.Context, serverID string) (stores.Server, error) {
	var active bool
	err := d.s.DB.QueryRowContext(ctx, `
        SELECT u.status = 'active' FROM servers s JOIN users u ON u.id = s.owner_user_id
         WHERE s.id::text = $1`, serverID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !active) {
		return stores.Server{}, ErrServerRevoked
	}
	if err != nil {
		return stores.Server{}, err
	}
	return d.s.BySlugOrID(ctx, serverID)
}

// SetOnline marks the server's tunnel up or down: servers.last_seen_at and
// server_health.online (the dashboard's state).
func (d dbTunnelStore) SetOnline(ctx context.Context, serverID string, online bool) error {
	return setOnline(ctx, d.s.DB, serverID, online)
}

func setOnline(ctx context.Context, db *sql.DB, serverID string, online bool) error {
	if _, err := db.ExecContext(ctx, `UPDATE servers SET last_seen_at = now() WHERE id = $1`, serverID); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `
        INSERT INTO server_health (server_id, online, last_heartbeat, updated_at)
        VALUES ($1, $2, now(), now())
        ON CONFLICT (server_id) DO UPDATE SET online = EXCLUDED.online, updated_at = now()
    `, serverID, online)
	return err
}

// ServeWS upgrades an HTTP request to a websocket and hands the
// resulting tunnel to the registry. The agent must send an AUTH frame
// within 5 seconds or we drop the connection.
func ServeWS(reg *Registry, servers *stores.Servers) http.HandlerFunc {
	return ServeTunnels(reg, NewTunnelStore(servers), slog.Default())
}

// ServeTunnels is ServeWS over any TunnelStore, logging to logger, with no
// resume tickets and no drain.
func ServeTunnels(reg *Registry, store TunnelStore, logger *slog.Logger) http.HandlerFunc {
	return (&TunnelServer{Registry: reg, Store: store, Logger: logger}).ServeHTTP
}

// TunnelServer accepts home tunnels at /v1/relay/ws.
type TunnelServer struct {
	Registry *Registry
	Store    TunnelStore
	Logger   *slog.Logger
	// Tickets mints and checks resume tickets; nil turns them off (every
	// handshake checks the bearer).
	Tickets *Tickets
	// Drain: tell homes that announce it that this relay sends DRAIN.
	Drain bool
	// TicketEvery is how often a fresh ticket is sent (default TicketRefresh).
	TicketEvery time.Duration
	// Directory records which process holds each tunnel (story 50.04); nil:
	// none (one process, no forwarding).
	Directory Directory
}

// ServeHTTP upgrades and authenticates one home's tunnel. The agent must
// send an AUTH frame within 5 seconds or we drop the connection.
func (ts *TunnelServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := ts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	reg, store := ts.Registry, ts.Store
	if reg.Closing() {
		// this process is going away: the home tries the other process or node
		w.Header().Set("Retry-After", "1")
		http.Error(w, "relay shutting down", http.StatusServiceUnavailable)
		return
	}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(wsReadLimit)
	fail := func(reason string) {
		_ = c.SetWriteDeadline(time.Now().Add(writeWait))
		_ = writeControl(c, Frame{Kind: KindAuthFail, Payload: []byte(`{"error":"` + reason + `"}`)})
		_ = c.Close()
	}
	_ = c.SetReadDeadline(time.Now().Add(authDeadline))
	_, data, err := c.ReadMessage()
	if err != nil {
		_ = c.Close()
		return
	}
	f, err := ReadFrame(&byteReader{b: data})
	if err != nil || f.Kind != KindAuth {
		fail("protocol")
		return
	}
	var ap AuthPayload
	if err := json.Unmarshal(f.Payload, &ap); err != nil {
		fail("bad_json")
		return
	}
	var sv stores.Server
	method := "bearer"
	if ap.Ticket != "" {
		method = "ticket"
		sv, err = ts.resume(r.Context(), ap)
		if err != nil {
			stats.ticketRefused.Add(1)
			// the reason stays in the log, never the ticket
			logger.Info("relay: resume ticket refused", "server_id", ap.ServerID, "err", err, "event", "tunnel_ticket_refused")
			fail("ticket_invalid")
			return
		}
	} else {
		sv, err = store.Authenticate(r.Context(), ap.ServerID, ap.Secret)
		if err != nil {
			logger.Warn("relay: tunnel auth refused", "server_id", ap.ServerID, "err", err, "event", "tunnel_auth_refused")
			fail("unauthorized")
			return
		}
	}
	if reg.Closing() {
		fail("shutting_down")
		return
	}
	flow := ap.Flow >= FlowVersion
	canTicket := ts.Tickets != nil && ap.has(FeatureTicket)
	canDrain := ts.Drain && ap.has(FeatureDrain)
	okp := AuthOKPayload{Slug: sv.Slug, IssuedAt: time.Now().UTC()}
	opts := []TunnelOption{WithFeatures(canDrain, canTicket)}
	if flow {
		okp.Flow = FlowVersion
		opts = append(opts, WithFlowControl())
	}
	if canDrain {
		okp.Features = append(okp.Features, FeatureDrain)
	}
	if canTicket {
		okp.Features = append(okp.Features, FeatureTicket)
		okp.Ticket = ts.Tickets.Mint(sv.ID)
	}
	ok, _ := json.Marshal(okp)
	_ = c.SetWriteDeadline(time.Now().Add(writeWait))
	if err := writeControl(c, Frame{Kind: KindAuthOK, Payload: ok}); err != nil {
		_ = c.Close()
		return
	}
	if method == "ticket" {
		stats.handsTicket.Add(1)
	} else {
		stats.handsBearer.Add(1)
	}

	t := NewTunnel(sv.ID, sv.Slug, &wsConn{c: c}, opts...)
	// online first, then routable: whatever finds the tunnel finds the server online
	if !reg.Up(sv.Slug, t, func() {
		markOnline(logger, store, sv.ID, true)
		ts.directory(logger, func(ctx context.Context, d Directory) error { return d.Up(ctx, sv.Slug, sv.ID, t.Seq()) })
	}) {
		// gone while it waited its turn, or a newer connection is in: nothing to record
		t.Close()
		logger.Info("relay: a stale tunnel attempt dropped", "server_id", sv.ID, "slug", sv.Slug, "event", "tunnel_stale")
		return
	}
	logger.Info("relay: tunnel up", "server_id", sv.ID, "slug", sv.Slug, "flow", flow, "method", method, "event", "tunnel_up")
	if canTicket {
		go ts.refreshTickets(t)
	}
	go func() {
		<-t.Done()
		// a tunnel a reconnect replaced is not the server going offline
		// the directory row goes whether or not t was the live tunnel here:
		// Down deletes it only when it is still this tunnel's
		ts.directory(logger, func(ctx context.Context, d Directory) error { return d.Down(ctx, sv.Slug, t.Seq()) })
		if reg.Down(sv.Slug, t, func() { markOnline(logger, store, sv.ID, false) }) {
			logger.Info("relay: tunnel down", "server_id", sv.ID, "slug", sv.Slug, "event", "tunnel_down")
		}
	}()
}

// directory writes to the tunnel directory, best-effort: a failed write is
// logged (the row is fixed by the home's next tunnel, or swept with its node).
func (ts *TunnelServer) directory(logger *slog.Logger, write func(context.Context, Directory) error) {
	if ts.Directory == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := write(ctx, ts.Directory); err != nil {
		logger.Warn("relay: writing the tunnel directory failed", "err", err, "event", "relay_directory_write_failed")
	}
}

// resume authenticates a home by its resume ticket: the HMAC and expiry
// (microseconds), then the server's standing (one indexed query, no argon2id).
func (ts *TunnelServer) resume(ctx context.Context, ap AuthPayload) (stores.Server, error) {
	if ts.Tickets == nil {
		return stores.Server{}, ErrTicketMalformed
	}
	if err := ts.Tickets.Verify(ap.Ticket, ap.ServerID); err != nil {
		return stores.Server{}, err
	}
	rs, ok := ts.Store.(ResumeStore)
	if !ok {
		return stores.Server{}, ErrServerRevoked
	}
	return rs.Resume(ctx, ap.ServerID)
}

// refreshTickets sends the home a fresh resume ticket every TicketEvery
// until the tunnel closes.
func (ts *TunnelServer) refreshTickets(t *Tunnel) {
	every := ts.TicketEvery
	if every <= 0 {
		every = TicketRefresh
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-t.Done():
			return
		case <-tk.C:
			p, _ := json.Marshal(TicketPayload{Ticket: ts.Tickets.Mint(t.ServerID)})
			_ = t.writeFrame(Frame{Kind: KindTicket, Payload: p})
		}
	}
}

// markOnline records the tunnel state, best-effort: a failed write is
// logged, never fatal to the tunnel.
func markOnline(logger *slog.Logger, store TunnelStore, serverID string, online bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.SetOnline(ctx, serverID, online); err != nil {
		logger.Warn("relay: recording the tunnel state failed", "server_id", serverID, "online", online, "err", err)
	}
}
