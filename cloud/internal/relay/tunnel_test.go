package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// chanConn is an in-memory FrameConn: frames the tunnel writes land on
// out; frames pushed on in are what the tunnel reads.
type chanConn struct {
	in     chan Frame
	out    chan Frame
	closed chan struct{}
}

func newChanConn() *chanConn {
	return &chanConn{in: make(chan Frame, 64), out: make(chan Frame, 64), closed: make(chan struct{})}
}

func (c *chanConn) ReadFrame() (Frame, error) {
	select {
	case f := <-c.in:
		return f, nil
	case <-c.closed:
		return Frame{}, errors.New("closed")
	}
}

func (c *chanConn) WriteFrame(f Frame) error {
	select {
	case c.out <- f:
		return nil
	case <-c.closed:
		return errors.New("closed")
	}
}

func (c *chanConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

// awaitRequestEOF drains the tunnel's outbound frames up to the empty
// REQUEST_BODY that terminates the request, returning its stream id.
func awaitRequestEOF(t *testing.T, c *chanConn) uint32 {
	t.Helper()
	for {
		select {
		case f := <-c.out:
			if f.Kind == KindRequestBody && len(f.Payload) == 0 {
				return f.StreamID
			}
		case <-time.After(2 * time.Second):
			t.Fatal("tunnel never finished writing the request")
		}
	}
}

// TestProxy_BodyFramesAfterProxyReturns is the regression test for the
// stream-delete race: the stream used to be unregistered when Proxy
// returned (right after RESPONSE_HEAD), so RESPONSE_BODY frames that
// arrived later were dropped and the body reader hung until ctx expired.
func TestProxy_BodyFramesAfterProxyReturns(t *testing.T) {
	conn := newChanConn()
	tun := NewTunnel("srv", "slug", conn)
	defer tun.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		id := awaitRequestEOF(t, conn)
		conn.in <- Frame{Kind: KindResponseHead, StreamID: id,
			Payload: []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n")}
	}()
	resp, err := tun.Proxy(ctx, httptest.NewRequest(http.MethodGet, "http://slug/x", nil))
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Proxy has returned; only now does the server emit the body.
	conn.in <- Frame{Kind: KindResponseBody, StreamID: 1, Payload: []byte("hello")}
	conn.in <- Frame{Kind: KindResponseBody, StreamID: 1}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("body = %q, want hello", b)
	}
	if _, ok := tun.streams.Load(uint32(1)); ok {
		t.Fatal("stream still registered after body EOF")
	}
}

// TestProxy_BodyCloseUnregistersStream ensures a caller that abandons
// the body releases the stream, and late frames for it don't block the
// read loop.
func TestProxy_BodyCloseUnregistersStream(t *testing.T) {
	conn := newChanConn()
	tun := NewTunnel("srv", "slug", conn)
	defer tun.Close()

	go func() {
		id := awaitRequestEOF(t, conn)
		conn.in <- Frame{Kind: KindResponseHead, StreamID: id,
			Payload: []byte("HTTP/1.1 200 OK\r\n\r\n")}
	}()
	resp, err := tun.Proxy(context.Background(), httptest.NewRequest(http.MethodGet, "http://slug/x", nil))
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if _, ok := tun.streams.Load(uint32(1)); !ok {
		t.Fatal("stream unregistered before body was consumed")
	}
	_ = resp.Body.Close()
	if _, ok := tun.streams.Load(uint32(1)); ok {
		t.Fatal("stream still registered after Body.Close")
	}

	// Late frames for the closed stream are ignored; PING still answers,
	// proving the read loop is not wedged.
	conn.in <- Frame{Kind: KindResponseBody, StreamID: 1, Payload: []byte("late")}
	conn.in <- Frame{Kind: KindPing, StreamID: 7}
	select {
	case f := <-conn.out:
		if f.Kind != KindPong || f.StreamID != 7 {
			t.Fatalf("got kind=%#x stream=%d, want PONG/7", f.Kind, f.StreamID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read loop wedged after body close")
	}
}

// Stub audit #21: the registry gauges are distinct connected servers and
// tunnels carrying a proxied request right now, not the same number.
func TestRegistryCounts(t *testing.T) {
	reg := NewRegistry()
	if s, tu := reg.Counts(); s != 0 || tu != 0 {
		t.Fatalf("empty registry: %d %d", s, tu)
	}
	connA, connB, connC := newChanConn(), newChanConn(), newChanConn()
	a := NewTunnel("srv-a", "a", connA)
	b := NewTunnel("srv-b", "b", connB)
	b2 := NewTunnel("srv-b", "b-alt", connC) // the same server under a second slug
	defer a.Close()
	defer b.Close()
	defer b2.Close()
	reg.Register("a", a)
	reg.Register("b", b)
	reg.Register("b-alt", b2)
	if s, tu := reg.Counts(); s != 2 || tu != 0 {
		t.Fatalf("idle: servers=%d tunnels=%d, want 2/0", s, tu)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		id := awaitRequestEOF(t, connA)
		connA.in <- Frame{Kind: KindResponseHead, StreamID: id, Payload: []byte("HTTP/1.1 200 OK\r\n\r\n")}
	}()
	resp, err := a.Proxy(ctx, httptest.NewRequest(http.MethodGet, "http://a/x", nil))
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if s, tu := reg.Counts(); s != 2 || tu != 1 {
		t.Fatalf("one request in flight: servers=%d tunnels=%d, want 2/1", s, tu)
	}
	_ = resp.Body.Close()
	if s, tu := reg.Counts(); s != 2 || tu != 0 {
		t.Fatalf("after the response: servers=%d tunnels=%d, want 2/0", s, tu)
	}
	if a.Busy() {
		t.Fatal("tunnel still busy after Body.Close")
	}
}
