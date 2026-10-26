package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
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
	// flow: the home windows its RESPONSE_BODY frames (negotiated at AUTH,
	// see protocol.go); the cloud grants credit as the client reads.
	flow bool
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

// TunnelOption configures a Tunnel at construction.
type TunnelOption func(*Tunnel)

// WithFlowControl marks the tunnel as windowed: the agent announced flow
// control and the cloud agreed (AUTH / AUTH_OK "flow").
func WithFlowControl() TunnelOption { return func(t *Tunnel) { t.flow = true } }

// NewTunnel wires the read loop and returns immediately. Callers must
// then call Register on the *Registry so the proxy can find it.
func NewTunnel(serverID, slug string, conn FrameConn, opts ...TunnelOption) *Tunnel {
	t := &Tunnel{ServerID: serverID, Slug: slug, conn: conn, done: make(chan struct{})}
	for _, o := range opts {
		o(t)
	}
	go t.readLoop()
	return t
}

// FlowControl reports whether the tunnel is windowed.
func (t *Tunnel) FlowControl() bool { return t.flow }

// Busy reports whether the tunnel is carrying at least one proxied
// request (a stream registered and not yet released).
func (t *Tunnel) Busy() bool {
	busy := false
	t.streams.Range(func(_, _ any) bool {
		busy = true
		return false
	})
	return busy
}

// Close shuts down the tunnel and unblocks all pending streams.
func (t *Tunnel) Close() {
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		_ = t.conn.Close()
		close(t.done)
		t.streams.Range(func(_, v any) bool {
			v.(*stream).fail(errTunnelClosed)
			return true
		})
	})
}

var errTunnelClosed = errors.New("relay: tunnel closed")

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
		v, ok := t.streams.Load(f.StreamID)
		if !ok {
			return
		}
		s := v.(*stream)
		if err := s.push(f, t.flow); err != nil {
			// The stream cannot take the frame (a home over its window, or
			// a client that stopped reading): fail it and tell the home,
			// never drop a frame and carry on with a corrupt body.
			t.streams.Delete(f.StreamID)
			s.fail(err)
			go t.closeStream(f.StreamID, err.Error())
		}
	case KindHeartbeat:
		// Heartbeat data is consumed by the supervisor goroutine via a
		// callback wired at construction time in a future story.
	}
}

// closeStream tells the home to stop a stream (best-effort).
func (t *Tunnel) closeStream(id uint32, reason string) {
	_ = t.writeFrame(Frame{Kind: KindCloseStream, StreamID: id, Payload: []byte(reason)})
}

func (t *Tunnel) writeFrame(f Frame) error {
	if t.closed.Load() {
		return errTunnelClosed
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if err := t.conn.WriteFrame(f); err != nil {
		return err
	}
	t.BytesOut.Add(int64(9 + len(f.Payload)))
	return nil
}

// ErrHeadTimeout is returned when the home does not answer a request's
// head within the proxy's head timeout.
var ErrHeadTimeout = errors.New("relay: the home did not answer in time")

// Proxy serializes an HTTP request onto the tunnel and waits for the
// response. ctx bounds the whole exchange, body included.
func (t *Tunnel) Proxy(ctx context.Context, r *http.Request) (*http.Response, error) {
	return t.ProxyWithin(ctx, r, 0)
}

// ProxyWithin is Proxy with headTimeout (>0) bounding only the wait for
// RESPONSE_HEAD: the response body then lives as long as ctx, so a long
// video keeps playing. A timeout returns ErrHeadTimeout.
func (t *Tunnel) ProxyWithin(ctx context.Context, r *http.Request, headTimeout time.Duration) (*http.Response, error) {
	if t.closed.Load() {
		return nil, errTunnelClosed
	}
	streamID := atomic.AddUint32(&t.nextID, 1)
	s := newStream()
	t.streams.Store(streamID, s)
	// The stream must stay registered until the response BODY is fully
	// consumed, not merely until Proxy returns: Proxy returns as soon as
	// RESPONSE_HEAD arrives, and RESPONSE_BODY frames that dispatch sees
	// after an early delete would be dropped, hanging the body reader
	// until ctx expires. On success, ownership of the registration moves
	// to the returned body (released on EOF, error, or Close).
	var once sync.Once
	release := func(early bool) {
		once.Do(func() {
			t.streams.Delete(streamID)
			s.abandon()
			if early {
				// the client left (or the body was closed) before the home
				// finished: stop the home reading its file
				go t.closeStream(streamID, "client gone")
			}
		})
	}
	resp, err := t.proxy(ctx, r, streamID, s, release, headTimeout)
	if err != nil {
		release(true)
		return nil, err
	}
	return resp, nil
}

func (t *Tunnel) proxy(ctx context.Context, r *http.Request, streamID uint32, s *stream, release func(bool), headTimeout time.Duration) (*http.Response, error) {
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
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
		}
	}
	if err := t.writeFrame(Frame{Kind: KindRequestBody, StreamID: streamID}); err != nil {
		return nil, err
	}

	return t.collectResponse(ctx, r, streamID, s, release, headTimeout)
}

// collectResponse waits for RESPONSE_HEAD (bounded by headTimeout when
// set) and returns a response whose body pulls the stream's body frames.
func (t *Tunnel) collectResponse(ctx context.Context, req *http.Request, streamID uint32, s *stream, release func(bool), headTimeout time.Duration) (*http.Response, error) {
	hctx := ctx
	if headTimeout > 0 {
		var cancel context.CancelFunc
		hctx, cancel = context.WithTimeout(ctx, headTimeout)
		defer cancel()
	}
	head, err := s.pop(hctx)
	if err != nil {
		if ctx.Err() == nil && hctx.Err() != nil {
			return nil, ErrHeadTimeout
		}
		if errors.Is(err, errStreamDone) {
			return nil, errors.New("relay: stream closed before response")
		}
		return nil, err
	}
	if head.Kind == KindCloseStream {
		return nil, fmt.Errorf("relay: the home closed the stream: %s", string(head.Payload))
	}
	if head.Kind != KindResponseHead {
		return nil, fmt.Errorf("relay: expected RESPONSE_HEAD, got %#x", head.Kind)
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(head.Payload)), req)
	if err != nil {
		return nil, err
	}
	// Replace body with a reader that pulls subsequent RESPONSE_BODY frames.
	resp.Body = &streamBody{s: s, ctx: ctx, release: release, t: t, id: streamID}
	return resp, nil
}

// Stream buffer bounds. On a windowed tunnel the home never has more than
// InitialWindow bytes outstanding, so more buffered is a broken (or
// hostile) home. On an unwindowed one the read loop waits up to
// legacyPushWait for the client to make room, as before, and then fails
// the stream instead of dropping the frame.
const (
	flowBufferMax   = InitialWindow + 64*1024
	legacyBufferMax = 1 << 20
	// grantEvery is how many consumed bytes the cloud batches into one
	// WINDOW_UPDATE.
	grantEvery = InitialWindow / 4
)

// legacyPushWait is how long an unwindowed stream may stay full (a var
// for tests).
var legacyPushWait = 30 * time.Second

var (
	errStreamDone     = errors.New("relay: stream finished")
	errStreamOverflow = errors.New("relay: the home sent past its window")
	errStreamStalled  = errors.New("relay: the client stopped reading")
)

// stream is the per-request inbound buffer: the read loop pushes frames,
// the proxy goroutine pops them. The read loop never blocks on it on a
// windowed tunnel.
type stream struct {
	mu     sync.Mutex
	q      []Frame
	qBytes int
	err    error // terminal: set by fail; pop returns it once q drains
	gone   bool  // the consumer stopped reading
	notify chan struct{}
	space  chan struct{}
}

func newStream() *stream {
	return &stream{notify: make(chan struct{}, 1), space: make(chan struct{}, 1)}
}

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// abandon tells push that nobody will read further frames.
func (s *stream) abandon() {
	s.mu.Lock()
	s.gone = true
	s.q, s.qBytes = nil, 0
	s.mu.Unlock()
	signal(s.space)
	signal(s.notify)
}

// push queues f. It returns an error when the stream must be failed (the
// caller fails it and tells the home).
func (s *stream) push(f Frame, flow bool) error {
	n := len(f.Payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone || s.err != nil {
		return nil
	}
	if flow {
		if f.Kind == KindResponseBody && s.qBytes+n > flowBufferMax {
			return errStreamOverflow
		}
	} else {
		var deadline <-chan time.Time
		for s.qBytes > 0 && s.qBytes+n > legacyBufferMax {
			if deadline == nil {
				tm := time.NewTimer(legacyPushWait)
				defer tm.Stop()
				deadline = tm.C
			}
			s.mu.Unlock()
			select {
			case <-s.space:
			case <-deadline:
				s.mu.Lock()
				return errStreamStalled
			}
			s.mu.Lock()
			if s.gone || s.err != nil {
				return nil
			}
		}
	}
	s.q = append(s.q, f)
	s.qBytes += n
	signal(s.notify)
	return nil
}

// fail ends the stream with err: pop returns the frames already queued,
// then err.
func (s *stream) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	signal(s.notify)
	signal(s.space)
}

// pop returns the next frame, blocking until one arrives, the stream
// fails (its error) or ctx ends.
func (s *stream) pop(ctx context.Context) (Frame, error) {
	for {
		s.mu.Lock()
		if s.gone {
			s.mu.Unlock()
			return Frame{}, errStreamDone
		}
		if len(s.q) > 0 {
			f := s.q[0]
			s.q[0] = Frame{}
			s.q = s.q[1:]
			s.qBytes -= len(f.Payload)
			s.mu.Unlock()
			signal(s.space)
			return f, nil
		}
		if s.err != nil {
			err := s.err
			s.mu.Unlock()
			return Frame{}, err
		}
		s.mu.Unlock()
		select {
		case <-s.notify:
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		}
	}
}

type streamBody struct {
	s       *stream
	t       *Tunnel
	id      uint32
	ctx     context.Context
	buf     []byte
	done    bool
	release func(early bool) // unregisters the stream; idempotent
	unacked int              // consumed bytes not yet granted back
}

// finish marks the body terminal and unregisters its stream.
func (b *streamBody) finish(early bool) {
	b.done = true
	b.release(early)
}

// grant credits the home with n consumed bytes, batched.
func (b *streamBody) grant(n int) {
	if !b.t.flow || n == 0 {
		return
	}
	b.unacked += n
	if b.unacked < grantEvery {
		return
	}
	var p [4]byte
	binary.BigEndian.PutUint32(p[:], uint32(b.unacked))
	b.unacked = 0
	_ = b.t.writeFrame(Frame{Kind: KindWindowUpdate, StreamID: b.id, Payload: p[:]})
}

func (b *streamBody) Read(p []byte) (int, error) {
	for {
		if len(b.buf) > 0 {
			n := copy(p, b.buf)
			b.buf = b.buf[n:]
			b.grant(n)
			return n, nil
		}
		if b.done {
			return 0, io.EOF
		}
		f, err := b.s.pop(b.ctx)
		if err != nil {
			if errors.Is(err, errStreamDone) {
				b.done = true
				return 0, io.EOF
			}
			// the client's context ended, the tunnel closed or the stream
			// failed: the home must stop
			b.finish(true)
			return 0, err
		}
		if f.Kind == KindCloseStream {
			b.finish(false)
			if len(f.Payload) > 0 {
				return 0, fmt.Errorf("relay: %s", string(f.Payload))
			}
			return 0, io.EOF
		}
		if len(f.Payload) == 0 {
			b.finish(false)
			return 0, io.EOF
		}
		b.buf = f.Payload
	}
}

// Close releases the stream; before the home's EOF that tells the home
// to stop.
func (b *streamBody) Close() error {
	b.finish(!b.done)
	return nil
}
