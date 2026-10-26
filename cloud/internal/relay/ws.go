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
	buf := make([]byte, 9, 9+n)
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
type AuthPayload struct {
	ServerID string `json:"server_id"`
	Secret   string `json:"secret"`
	Flow     int    `json:"flow,omitempty"`
}

// AuthOKPayload echoes back the slug so the agent can confirm we
// matched it correctly. Flow is FlowVersion when the tunnel is windowed
// (the agent asked for it), else absent.
type AuthOKPayload struct {
	Slug     string    `json:"slug"`
	IssuedAt time.Time `json:"issued_at"`
	Flow     int       `json:"flow,omitempty"`
}

// TunnelStore is what the tunnel accept handler needs from the servers
// table: authenticate an agent, and record its tunnel coming and going.
type TunnelStore interface {
	Authenticate(ctx context.Context, serverID, secret string) (stores.Server, error)
	SetOnline(ctx context.Context, serverID string, online bool) error
}

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

// ServeTunnels is ServeWS over any TunnelStore, logging to logger.
func ServeTunnels(reg *Registry, store TunnelStore, logger *slog.Logger) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
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
		sv, err := store.Authenticate(r.Context(), ap.ServerID, ap.Secret)
		if err != nil {
			logger.Warn("relay: tunnel auth refused", "server_id", ap.ServerID, "err", err, "event", "tunnel_auth_refused")
			fail("unauthorized")
			return
		}
		flow := ap.Flow >= FlowVersion
		okp := AuthOKPayload{Slug: sv.Slug, IssuedAt: time.Now().UTC()}
		var opts []TunnelOption
		if flow {
			okp.Flow = FlowVersion
			opts = append(opts, WithFlowControl())
		}
		ok, _ := json.Marshal(okp)
		_ = c.SetWriteDeadline(time.Now().Add(writeWait))
		if err := writeControl(c, Frame{Kind: KindAuthOK, Payload: ok}); err != nil {
			_ = c.Close()
			return
		}

		t := NewTunnel(sv.ID, sv.Slug, &wsConn{c: c}, opts...)
		reg.Register(sv.Slug, t)
		logger.Info("relay: tunnel up", "server_id", sv.ID, "slug", sv.Slug, "flow", flow, "event", "tunnel_up")
		markOnline(logger, store, sv.ID, true)
		go func() {
			<-t.Done()
			// a tunnel a reconnect replaced is not the server going offline
			if reg.Unregister(sv.Slug, t) {
				logger.Info("relay: tunnel down", "server_id", sv.ID, "slug", sv.Slug, "event", "tunnel_down")
				markOnline(logger, store, sv.ID, false)
			}
		}()
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
