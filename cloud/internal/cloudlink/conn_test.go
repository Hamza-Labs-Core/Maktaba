package cloudlink

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// TestAuthenticate_OK drives the AUTH exchange over a pipe, with the
// peer replying exactly as cloud/internal/relay/ws.go does on success.
func TestAuthenticate_OK(t *testing.T) {
	clientEnd, cloudEnd := newPipe()

	go func() {
		f, err := cloudEnd.ReadFrame()
		if err != nil || f.Kind != KindAuth {
			return
		}
		var ap authPayload
		_ = json.Unmarshal(f.Payload, &ap)
		if ap.ServerID != "srv-1" || ap.Secret != "sek" {
			ok, _ := json.Marshal(map[string]string{"error": "unauthorized"})
			_ = cloudEnd.WriteFrame(Frame{Kind: KindAuthFail, Payload: ok})
			return
		}
		ok, _ := json.Marshal(authOKPayload{Slug: "acme", IssuedAt: time.Now().UTC()})
		_ = cloudEnd.WriteFrame(Frame{Kind: KindAuthOK, Payload: ok})
	}()

	res, err := authenticate(clientEnd, "srv-1", "sek")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if res.Slug != "acme" {
		t.Fatalf("slug = %q, want acme", res.Slug)
	}
}

func TestAuthenticate_Rejected(t *testing.T) {
	clientEnd, cloudEnd := newPipe()
	go func() {
		_, _ = cloudEnd.ReadFrame()
		_ = cloudEnd.WriteFrame(Frame{Kind: KindAuthFail, Payload: []byte(`{"error":"unauthorized"}`)})
	}()
	_, err := authenticate(clientEnd, "x", "y")
	if !errors.Is(err, ErrAuthRejected) {
		t.Fatalf("err = %v, want ErrAuthRejected", err)
	}
}

func TestAuthenticate_ProtocolViolation(t *testing.T) {
	clientEnd, cloudEnd := newPipe()
	go func() {
		_, _ = cloudEnd.ReadFrame()
		// Cloud sends a non-AUTH frame first — protocol error.
		_ = cloudEnd.WriteFrame(Frame{Kind: KindResponseHead})
	}()
	_, err := authenticate(clientEnd, "x", "y")
	if !errors.Is(err, ErrAuthProtocol) {
		t.Fatalf("err = %v, want ErrAuthProtocol", err)
	}
}

// TestDial_RealWebsocket_AgainstRelayCodec stands up a real websocket
// server that frames replies with the SAME relay codec the production
// cloud uses (one frame per binary message, via relay.ReadFrame /
// mustEncode-equivalent), and asserts Dialer.Dial completes the
// handshake over a genuine WSS upgrade.
func TestDial_RealWebsocket_AgainstRelayCodec(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		f, err := relay.ReadFrame(strings.NewReader(string(data)))
		if err != nil || f.Kind != KindAuth {
			return
		}
		var buf strings.Builder
		_ = relay.WriteFrame(&strWriter{&buf}, Frame{
			Kind:    KindAuthOK,
			Payload: []byte(`{"slug":"acme","issued_at":"2026-01-01T00:00:00Z"}`),
		})
		_ = c.WriteMessage(websocket.BinaryMessage, []byte(buf.String()))
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	d := &Dialer{Endpoint: wsURL, ServerID: "srv-1", Secret: "sek", HandshakeTimeout: 3 * time.Second}
	res, err := d.Dial(context.Background())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = res.Conn.Close() }()
	if res.Slug != "acme" {
		t.Fatalf("slug = %q, want acme", res.Slug)
	}
}

type strWriter struct{ b *strings.Builder }

func (s *strWriter) Write(p []byte) (int, error) { return s.b.Write(p) }

func TestHTTPEndpointFromWS(t *testing.T) {
	cases := map[string]string{
		"wss://relay.maktaba.app/v1/relay/ws": "https://relay.maktaba.app/v1/relay/ws",
		"ws://127.0.0.1:9000/v1/relay/ws":     "http://127.0.0.1:9000/v1/relay/ws",
		"https://already":                     "https://already",
	}
	for in, want := range cases {
		if got := httpEndpointFromWS(in); got != want {
			t.Errorf("httpEndpointFromWS(%q) = %q, want %q", in, got, want)
		}
	}
}
