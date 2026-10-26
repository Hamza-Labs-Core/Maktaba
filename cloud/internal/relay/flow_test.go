package relay

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// answerHead waits for the request and answers its head; returns the id.
func answerHead(t *testing.T, conn *chanConn) uint32 {
	t.Helper()
	id := awaitRequestEOF(t, conn)
	conn.in <- Frame{Kind: KindResponseHead, StreamID: id, Payload: []byte("HTTP/1.1 200 OK\r\n\r\n")}
	return id
}

// nextOut returns the next frame the tunnel wrote of one of kinds.
func nextOut(t *testing.T, conn *chanConn, within time.Duration, kinds ...byte) Frame {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case f := <-conn.out:
			for _, k := range kinds {
				if f.Kind == k {
					return f
				}
			}
		case <-deadline:
			t.Fatalf("no frame of kinds %v within %v", kinds, within)
		}
	}
}

func proxyAsync(tun *Tunnel, ctx context.Context, timeout time.Duration) (chan *http.Response, chan error) {
	rc, ec := make(chan *http.Response, 1), make(chan error, 1)
	go func() {
		resp, err := tun.ProxyWithin(ctx, httptest.NewRequest(http.MethodGet, "http://slug/x", nil), timeout)
		if err != nil {
			ec <- err
			return
		}
		rc <- resp
	}()
	return rc, ec
}

// On a windowed tunnel the cloud grants credit back as its client reads.
func TestFlow_GrantsCreditAsTheClientReads(t *testing.T) {
	conn := newChanConn()
	tun := NewTunnel("srv", "slug", conn, WithFlowControl())
	defer tun.Close()
	rc, ec := proxyAsync(tun, context.Background(), 0)
	id := answerHead(t, conn)
	var resp *http.Response
	select {
	case resp = <-rc:
	case err := <-ec:
		t.Fatal(err)
	}
	defer resp.Body.Close()
	chunk := bytes.Repeat([]byte("x"), 32*1024)
	for i := 0; i < 16; i++ { // 512 KiB, within the window
		conn.in <- Frame{Kind: KindResponseBody, StreamID: id, Payload: chunk}
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, 300*1024)); err != nil {
		t.Fatal(err)
	}
	f := nextOut(t, conn, 2*time.Second, KindWindowUpdate)
	if f.StreamID != id || len(f.Payload) != 4 || binary.BigEndian.Uint32(f.Payload) < grantEvery {
		t.Fatalf("WINDOW_UPDATE stream=%d payload=%x", f.StreamID, f.Payload)
	}
}

// A home that sends past its window has its stream failed (and is told),
// never a frame silently dropped; the read loop keeps serving.
func TestFlow_OverWindowFailsTheStream(t *testing.T) {
	conn := newChanConn()
	tun := NewTunnel("srv", "slug", conn, WithFlowControl())
	defer tun.Close()
	rc, _ := proxyAsync(tun, context.Background(), 0)
	id := answerHead(t, conn)
	resp := <-rc
	defer resp.Body.Close()
	chunk := bytes.Repeat([]byte("x"), 64*1024)
	go func() {
		for i := 0; i < 40; i++ { // 2.5 MiB, nobody reading
			conn.in <- Frame{Kind: KindResponseBody, StreamID: id, Payload: chunk}
		}
	}()
	f := nextOut(t, conn, 2*time.Second, KindCloseStream)
	if f.StreamID != id {
		t.Fatalf("CLOSE_STREAM for %d, want %d", f.StreamID, id)
	}
	_, err := io.ReadAll(resp.Body)
	if !errors.Is(err, errStreamOverflow) {
		t.Fatalf("the client's read ended with %v, want the overflow", err)
	}
	conn.in <- Frame{Kind: KindPing, StreamID: 9}
	if p := nextOut(t, conn, 2*time.Second, KindPong); p.StreamID != 9 {
		t.Fatal("the read loop stopped")
	}
}

// An older agent (no flow control) keeps the blocking behaviour, but a
// stream its client stopped reading is failed after legacyPushWait
// instead of losing a frame.
func TestLegacy_StalledStreamFailsNotDrops(t *testing.T) {
	old := legacyPushWait
	legacyPushWait = 100 * time.Millisecond
	defer func() { legacyPushWait = old }()

	conn := newChanConn()
	tun := NewTunnel("srv", "slug", conn)
	defer tun.Close()
	rc, _ := proxyAsync(tun, context.Background(), 0)
	id := answerHead(t, conn)
	resp := <-rc
	defer resp.Body.Close()
	chunk := bytes.Repeat([]byte("x"), 256*1024)
	go func() {
		for i := 0; i < 8; i++ { // 2 MiB, nobody reading
			conn.in <- Frame{Kind: KindResponseBody, StreamID: id, Payload: chunk}
		}
	}()
	if f := nextOut(t, conn, 3*time.Second, KindCloseStream); f.StreamID != id {
		t.Fatalf("CLOSE_STREAM for %d, want %d", f.StreamID, id)
	}
	b, err := io.ReadAll(resp.Body)
	if !errors.Is(err, errStreamStalled) {
		t.Fatalf("read %d bytes then %v, want the stall error", len(b), err)
	}
	if len(b)%len(chunk) != 0 {
		t.Fatalf("a partial frame reached the client: %d bytes", len(b))
	}
}

// The head timeout bounds only the wait for the head; the home is told
// to drop the stream.
func TestProxyWithin_HeadTimeout(t *testing.T) {
	conn := newChanConn()
	tun := NewTunnel("srv", "slug", conn)
	defer tun.Close()
	_, ec := proxyAsync(tun, context.Background(), 50*time.Millisecond)
	id := awaitRequestEOF(t, conn)
	select {
	case err := <-ec:
		if !errors.Is(err, ErrHeadTimeout) {
			t.Fatalf("err = %v, want ErrHeadTimeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the head wait was not bounded")
	}
	if f := nextOut(t, conn, 2*time.Second, KindCloseStream); f.StreamID != id {
		t.Fatalf("CLOSE_STREAM for %d, want %d", f.StreamID, id)
	}
}

// A body that streams past the head timeout is not cut by it.
func TestProxyWithin_BodyOutlivesHeadTimeout(t *testing.T) {
	conn := newChanConn()
	tun := NewTunnel("srv", "slug", conn)
	defer tun.Close()
	rc, ec := proxyAsync(tun, context.Background(), 50*time.Millisecond)
	id := answerHead(t, conn)
	var resp *http.Response
	select {
	case resp = <-rc:
	case err := <-ec:
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		for i := 0; i < 3; i++ {
			time.Sleep(60 * time.Millisecond)
			conn.in <- Frame{Kind: KindResponseBody, StreamID: id, Payload: []byte("ab")}
		}
		conn.in <- Frame{Kind: KindResponseBody, StreamID: id}
	}()
	b, err := io.ReadAll(resp.Body)
	if err != nil || string(b) != "ababab" {
		t.Fatalf("body %q, %v", b, err)
	}
}

// A client whose context ends mid-body releases the stream and tells the
// home.
func TestProxy_ClientGoneTellsTheHome(t *testing.T) {
	conn := newChanConn()
	tun := NewTunnel("srv", "slug", conn, WithFlowControl())
	defer tun.Close()
	ctx, cancel := context.WithCancel(context.Background())
	rc, _ := proxyAsync(tun, ctx, 0)
	id := answerHead(t, conn)
	resp := <-rc
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := io.ReadAll(resp.Body); !errors.Is(err, context.Canceled) {
		t.Fatalf("read ended with %v", err)
	}
	if f := nextOut(t, conn, 2*time.Second, KindCloseStream); f.StreamID != id {
		t.Fatalf("CLOSE_STREAM for %d, want %d", f.StreamID, id)
	}
	if tun.Busy() {
		t.Fatal("the stream is still registered")
	}
}
