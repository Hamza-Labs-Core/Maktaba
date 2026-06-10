package relay

import (
	"encoding/json"
	"errors"
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

// wsConn adapts a *websocket.Conn to FrameConn. The tunnel sees a
// stream-of-frames; we wrap each frame in a single binary message.
type wsConn struct{ c *websocket.Conn }

func (w *wsConn) ReadFrame() (Frame, error) {
	_, data, err := w.c.ReadMessage()
	if err != nil {
		return Frame{}, err
	}
	return ReadFrame(&byteReader{b: data})
}
func (w *wsConn) WriteFrame(f Frame) error {
	return w.c.WriteMessage(websocket.BinaryMessage, mustEncode(f))
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

func mustEncode(f Frame) []byte {
	buf := make([]byte, 0, 9+len(f.Payload))
	buf = append(buf, f.Kind)
	buf = append(buf, byte(f.StreamID>>24), byte(f.StreamID>>16), byte(f.StreamID>>8), byte(f.StreamID))
	n := uint32(len(f.Payload))
	buf = append(buf, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	buf = append(buf, f.Payload...)
	return buf
}

// AuthPayload is the body of the first frame the on-prem server sends.
type AuthPayload struct {
	ServerID string `json:"server_id"`
	Secret   string `json:"secret"`
}

// AuthOKPayload echoes back the slug so the agent can confirm we
// matched it correctly.
type AuthOKPayload struct {
	Slug     string    `json:"slug"`
	IssuedAt time.Time `json:"issued_at"`
}

// ServeWS upgrades an HTTP request to a websocket and hands the
// resulting tunnel to the registry. The agent must send an AUTH frame
// within 5 seconds or we drop the connection.
func ServeWS(reg *Registry, servers *stores.Servers) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := c.ReadMessage()
		if err != nil {
			c.Close()
			return
		}
		f, err := ReadFrame(&byteReader{b: data})
		if err != nil || f.Kind != KindAuth {
			_ = c.WriteMessage(websocket.BinaryMessage, mustEncode(Frame{Kind: KindAuthFail, Payload: []byte(`{"error":"protocol"}`)}))
			c.Close()
			return
		}
		var ap AuthPayload
		if err := json.Unmarshal(f.Payload, &ap); err != nil {
			_ = c.WriteMessage(websocket.BinaryMessage, mustEncode(Frame{Kind: KindAuthFail, Payload: []byte(`{"error":"bad_json"}`)}))
			c.Close()
			return
		}
		sv, err := lookupServerForAuth(r, servers, ap)
		if err != nil {
			_ = c.WriteMessage(websocket.BinaryMessage, mustEncode(Frame{Kind: KindAuthFail, Payload: []byte(`{"error":"unauthorized"}`)}))
			c.Close()
			return
		}
		_ = c.SetReadDeadline(time.Time{})
		ok, _ := json.Marshal(AuthOKPayload{Slug: sv.Slug, IssuedAt: time.Now().UTC()})
		_ = c.WriteMessage(websocket.BinaryMessage, mustEncode(Frame{Kind: KindAuthOK, Payload: ok}))

		t := NewTunnel(sv.ID, sv.Slug, &wsConn{c: c})
		reg.Register(sv.Slug, t)
		go func() {
			<-t.Done()
			reg.Unregister(sv.Slug, t)
		}()
	}
}

func lookupServerForAuth(r *http.Request, ss *stores.Servers, ap AuthPayload) (stores.Server, error) {
	if ap.ServerID == "" || ap.Secret == "" {
		return stores.Server{}, errors.New("missing creds")
	}
	// We re-query by id; the server_id+secret pair is what authenticates.
	var hash string
	row := ss.DB.QueryRowContext(r.Context(), `SELECT server_secret_hash FROM servers WHERE id = $1`, ap.ServerID)
	if err := row.Scan(&hash); err != nil {
		return stores.Server{}, err
	}
	if err := argon2id.Verify(ap.Secret, hash); err != nil {
		return stores.Server{}, err
	}
	sv, err := ss.BySlugOrID(r.Context(), ap.ServerID)
	if err != nil {
		return stores.Server{}, err
	}
	return sv, nil
}
