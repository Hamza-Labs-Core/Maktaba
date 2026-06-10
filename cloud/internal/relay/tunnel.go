package relay

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Tunnel is the server-facing side of one live WebSocket connection.
// It owns the I/O loop reading frames off the wire and routing
// REQUEST_HEAD/BODY back to whichever stream the HTTP proxy opened.
type Tunnel struct {
	ServerID  string
	Slug      string
	conn      FrameConn
	streams   sync.Map // streamID -> *stream
	nextID    uint32
	closed    atomic.Bool
	closeOnce sync.Once
	done      chan struct{}
	writeMu   sync.Mutex
	// Counters consumed by the bandwidth meter.
	BytesIn  atomic.Int64
	BytesOut atomic.Int64
}

// FrameConn abstracts the websocket. A test fake injects a pipe.
type FrameConn interface {
	ReadFrame() (Frame, error)
	WriteFrame(Frame) error
	Close() error
}

// NewTunnel wires the read loop and returns immediately. Callers must
// then call Register on the *Registry so the proxy can find it.
func NewTunnel(serverID, slug string, conn FrameConn) *Tunnel {
	t := &Tunnel{ServerID: serverID, Slug: slug, conn: conn, done: make(chan struct{})}
	go t.readLoop()
	return t
}

// Close shuts down the tunnel and unblocks all pending streams.
func (t *Tunnel) Close() {
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		_ = t.conn.Close()
		close(t.done)
		t.streams.Range(func(_, v any) bool {
			v.(*stream).fail(errors.New("relay: tunnel closed"))
			return true
		})
	})
}

// Done returns a channel closed when the tunnel is closed. Useful for
// the server-handler goroutine that wants to keep the HTTP upgrade
// alive until the websocket terminates.
func (t *Tunnel) Done() <-chan struct{} { return t.done }

func (t *Tunnel) readLoop() {
	for {
		f, err := t.conn.ReadFrame()
		if err != nil {
			t.Close()
			return
		}
		t.BytesIn.Add(int64(9 + len(f.Payload)))
		t.dispatch(f)
	}
}

func (t *Tunnel) dispatch(f Frame) {
	switch f.Kind {
	case KindPing:
		_ = t.writeFrame(Frame{Kind: KindPong, StreamID: f.StreamID})
	case KindResponseHead, KindResponseBody, KindCloseStream:
		if v, ok := t.streams.Load(f.StreamID); ok {
			v.(*stream).push(f)
		}
	case KindHeartbeat:
		// Heartbeat data is consumed by the supervisor goroutine via a
		// callback wired at construction time in a future story.
	}
}

func (t *Tunnel) writeFrame(f Frame) error {
	if t.closed.Load() {
		return errors.New("relay: tunnel closed")
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if err := t.conn.WriteFrame(f); err != nil {
		return err
	}
	t.BytesOut.Add(int64(9 + len(f.Payload)))
	return nil
}

// Proxy serializes an HTTP request onto the tunnel and waits for the
// response. This is the function the http relay handler calls.
func (t *Tunnel) Proxy(ctx context.Context, r *http.Request) (*http.Response, error) {
	if t.closed.Load() {
		return nil, errors.New("relay: tunnel closed")
	}
	streamID := atomic.AddUint32(&t.nextID, 1)
	s := newStream()
	t.streams.Store(streamID, s)
	defer t.streams.Delete(streamID)

	// Serialize request preamble.
	var head bytes.Buffer
	fmt.Fprintf(&head, "%s %s HTTP/1.1\r\n", r.Method, r.URL.RequestURI())
	fmt.Fprintf(&head, "Host: %s\r\n", r.Host)
	for k, vs := range r.Header {
		for _, v := range vs {
			fmt.Fprintf(&head, "%s: %s\r\n", k, v)
		}
	}
	head.WriteString("\r\n")
	if err := t.writeFrame(Frame{Kind: KindRequestHead, StreamID: streamID, Payload: head.Bytes()}); err != nil {
		return nil, err
	}

	// Stream request body in 32 KiB chunks; an empty BODY frame is EOF.
	if r.Body != nil {
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				if werr := t.writeFrame(Frame{Kind: KindRequestBody, StreamID: streamID, Payload: append([]byte(nil), buf[:n]...)}); werr != nil {
					return nil, werr
				}
			}
			if errors.Is(err, io.EOF) || n == 0 {
				_ = t.writeFrame(Frame{Kind: KindRequestBody, StreamID: streamID})
				break
			}
			if err != nil {
				return nil, err
			}
		}
	} else {
		_ = t.writeFrame(Frame{Kind: KindRequestBody, StreamID: streamID})
	}

	return s.collectResponse(ctx, r)
}

// stream is the per-request inbound buffer. Frames from the read loop
// land here; the proxy goroutine consumes them.
type stream struct {
	frames chan Frame
	closed atomic.Bool
}

func newStream() *stream { return &stream{frames: make(chan Frame, 16)} }

func (s *stream) push(f Frame) {
	if s.closed.Load() {
		return
	}
	select {
	case s.frames <- f:
	case <-time.After(30 * time.Second):
		// Drop on the floor; we can't block the read loop. The
		// underlying request will time out on its own.
	}
}

// fail closes the stream. The cause is currently advisory only — the
// reader surfaces its own "stream closed before response" error — so the
// argument is accepted for call-site clarity but not propagated.
func (s *stream) fail(_ error) {
	if s.closed.CompareAndSwap(false, true) {
		close(s.frames)
	}
}

// collectResponse blocks until RESPONSE_HEAD + body EOF arrive.
func (s *stream) collectResponse(ctx context.Context, req *http.Request) (*http.Response, error) {
	var head Frame
	select {
	case f, ok := <-s.frames:
		if !ok {
			return nil, errors.New("relay: stream closed before response")
		}
		head = f
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if head.Kind != KindResponseHead {
		return nil, fmt.Errorf("relay: expected RESPONSE_HEAD, got %#x", head.Kind)
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(head.Payload)), req)
	if err != nil {
		return nil, err
	}
	// Replace body with a reader that pulls subsequent RESPONSE_BODY frames.
	resp.Body = &streamBody{src: s.frames, ctx: ctx}
	return resp, nil
}

type streamBody struct {
	src  chan Frame
	ctx  context.Context
	buf  []byte
	done bool
}

func (b *streamBody) Read(p []byte) (int, error) {
	for {
		if len(b.buf) > 0 {
			n := copy(p, b.buf)
			b.buf = b.buf[n:]
			return n, nil
		}
		if b.done {
			return 0, io.EOF
		}
		select {
		case f, ok := <-b.src:
			if !ok {
				b.done = true
				return 0, io.EOF
			}
			if f.Kind == KindCloseStream {
				b.done = true
				if len(f.Payload) > 0 {
					return 0, fmt.Errorf("relay: %s", string(f.Payload))
				}
				return 0, io.EOF
			}
			if len(f.Payload) == 0 {
				b.done = true
				return 0, io.EOF
			}
			b.buf = f.Payload
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
}
func (b *streamBody) Close() error { return nil }
