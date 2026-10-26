package cloudlink

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// serveTimeout bounds one inbound request end-to-end: parse → loopback
// call → full response body relay. It exists because the loopback
// http.Client's ResponseHeaderTimeout only bounds time-to-first-byte,
// NOT body reads — a local handler that emits headers then trickles or
// wedges the body would otherwise pin the per-stream goroutine (and the
// cloud's matching stream) forever. The bound is deliberately generous:
// a legitimate large/slow loopback response (e.g. a multi-hundred-MiB
// export streamed off slow disk) must complete, while a genuinely stuck
// handler is abandoned in bounded time. 10 minutes is well above any
// healthy loopback response yet finite, so a stall always yields a
// CLOSE_STREAM rather than a leaked goroutine.
//
// On a flow-controlled tunnel a stream waiting for credit (its client
// paused) is not stuck: its own client ends it (the cloud sends
// CLOSE_STREAM), so the bound there is creditTimeout per wait instead.
const serveTimeout = 10 * time.Minute

// creditTimeout bounds one wait for WINDOW_UPDATE on a flow-controlled
// tunnel: a cloud that never grants more is not reading the stream.
const creditTimeout = 10 * time.Minute

// MaxRequestBody bounds a request body the agent buffers before calling
// the local API (uploads through the relay); a larger one is answered
// 413 and dropped.
var MaxRequestBody = 64 << 20

// Multiplexer owns the read loop for one live tunnel. It demuxes
// inbound frames by stream id, reassembles each REQUEST_HEAD+BODY into
// a request, and hands it to the LocalProxy. It also answers cloud
// PINGs with PONG (the cloud's relay/tunnel.go expects PONG to a PING
// it never actually sends today — see health.go for the client-driven
// keepalive that the cloud DOES answer).
//
// All frame writes funnel through writeFrame under one mutex, matching
// the server's single-writer discipline (relay/tunnel.go writeMu).
type Multiplexer struct {
	conn  FrameConn
	proxy *LocalProxy
	// flow: RESPONSE_BODY bytes are windowed per stream (negotiated at
	// AUTH); WINDOW_UPDATE frames from the cloud grant more.
	flow bool

	writeMu sync.Mutex

	mu      sync.Mutex
	streams map[uint32]*reqAssembly
	// cancels holds the per-stream cancel handle for in-flight handle()
	// goroutines. A cloud KindCloseStream (or tunnel teardown) cancels
	// the matching context so a wedged loopback body relay is abandoned
	// promptly and the goroutine exits instead of leaking. Values are
	// pointers so a goroutine only ever removes the exact handle it
	// registered (id reuse / racing CloseStream safe).
	cancels map[uint32]*streamCancel
	// credits is each in-flight stream's send window (flow only).
	credits map[uint32]*credit

	closeOnce sync.Once
	closed    atomic.Bool
	done      chan struct{}
	err       atomic.Value // error
	// lastRead is when the last frame arrived from the cloud (unix nanos):
	// the supervisor closes a tunnel silent for PongWait.
	lastRead atomic.Int64

	// counters surfaced via Stats() for the admin state endpoint.
	bytesIn  atomic.Int64
	bytesOut atomic.Int64
	reqs     atomic.Int64
}

// streamCancel is a heap-identity wrapper around a context.CancelFunc so
// the registering goroutine can prove ownership before removing its own
// entry (a later stream reusing the same id, or a CloseStream that
// already cancelled+deleted it, must not be clobbered).
type streamCancel struct {
	cancel context.CancelFunc
}

// MuxOption configures a Multiplexer.
type MuxOption func(*Multiplexer)

// WithFlowControl windows response bodies (the cloud agreed at AUTH).
func WithFlowControl() MuxOption { return func(m *Multiplexer) { m.flow = true } }

// NewMultiplexer wires the read loop and returns immediately.
func NewMultiplexer(conn FrameConn, proxy *LocalProxy, opts ...MuxOption) *Multiplexer {
	m := &Multiplexer{
		conn:    conn,
		proxy:   proxy,
		streams: make(map[uint32]*reqAssembly),
		cancels: make(map[uint32]*streamCancel),
		credits: make(map[uint32]*credit),
		done:    make(chan struct{}),
	}
	for _, o := range opts {
		o(m)
	}
	m.lastRead.Store(time.Now().UnixNano())
	go m.readLoop()
	return m
}

// Done is closed when the tunnel terminates (read error or Close).
func (m *Multiplexer) Done() <-chan struct{} { return m.done }

// Err returns the terminal error, if any (nil after a clean Close).
func (m *Multiplexer) Err() error {
	if v := m.err.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// LastRead is when the last frame arrived from the cloud.
func (m *Multiplexer) LastRead() time.Time { return time.Unix(0, m.lastRead.Load()) }

// Close tears down the tunnel idempotently.
func (m *Multiplexer) Close() {
	m.closeOnce.Do(func() {
		m.closed.Store(true)
		_ = m.conn.Close()
		// Cancel every in-flight per-stream context so a goroutine
		// blocked on a wedged loopback body relay unblocks and exits
		// instead of leaking past tunnel teardown.
		m.mu.Lock()
		for id, sc := range m.cancels {
			sc.cancel()
			delete(m.cancels, id)
		}
		m.mu.Unlock()
		close(m.done)
	})
}

// Stats is a point-in-time snapshot for GET /admin/cloud-link.
type Stats struct {
	BytesIn  int64
	BytesOut int64
	Requests int64
}

func (m *Multiplexer) Stats() Stats {
	return Stats{
		BytesIn:  m.bytesIn.Load(),
		BytesOut: m.bytesOut.Load(),
		Requests: m.reqs.Load(),
	}
}

// writeFrame is the single serialized writer for this tunnel.
func (m *Multiplexer) writeFrame(f Frame) error {
	if m.closed.Load() {
		return errors.New("cloudlink: tunnel closed")
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	if err := m.conn.WriteFrame(f); err != nil {
		return err
	}
	m.bytesOut.Add(int64(9 + len(f.Payload)))
	return nil
}

func (m *Multiplexer) fatal(err error) {
	if !m.closed.Load() {
		m.err.Store(err)
	}
	m.Close()
}

func (m *Multiplexer) readLoop() {
	for {
		f, err := m.conn.ReadFrame()
		if err != nil {
			m.fatal(err)
			return
		}
		m.lastRead.Store(time.Now().UnixNano())
		m.bytesIn.Add(int64(9 + len(f.Payload)))
		m.dispatch(f)
	}
}

func (m *Multiplexer) dispatch(f Frame) {
	switch f.Kind {
	case KindPing:
		// The cloud may PING us; echo PONG on the same stream id.
		_ = m.writeFrame(Frame{Kind: KindPong, StreamID: f.StreamID})
	case KindPong:
		// Reply to our own keepalive PING (health.go); readLoop already
		// noted the frame's arrival (lastRead).
	case KindRequestHead:
		m.mu.Lock()
		m.streams[f.StreamID] = &reqAssembly{head: append([]byte(nil), f.Payload...)}
		m.mu.Unlock()
	case KindRequestBody:
		m.mu.Lock()
		asm, ok := m.streams[f.StreamID]
		if !ok {
			m.mu.Unlock()
			return
		}
		if len(f.Payload) == 0 {
			// EOF: request complete. Hand off and clear the slot.
			delete(m.streams, f.StreamID)
			var cr *credit
			if m.flow {
				cr = newCredit(InitialWindow)
				m.credits[f.StreamID] = cr
			}
			m.mu.Unlock()
			m.reqs.Add(1)
			go m.handle(f.StreamID, asm, cr)
			return
		}
		if asm.body.Len()+len(f.Payload) > MaxRequestBody {
			// Too large to buffer: answer 413 and forget the stream; its
			// remaining body frames find no assembly and are ignored.
			delete(m.streams, f.StreamID)
			m.mu.Unlock()
			go m.reject(f.StreamID, http.StatusRequestEntityTooLarge)
			return
		}
		asm.body.Write(f.Payload)
		m.mu.Unlock()
	case KindWindowUpdate:
		if len(f.Payload) != 4 {
			return
		}
		m.mu.Lock()
		cr := m.credits[f.StreamID]
		m.mu.Unlock()
		if cr != nil {
			cr.add(int64(binary.BigEndian.Uint32(f.Payload)))
		}
	case KindCloseStream:
		// The cloud aborted this stream (its client left). Drop any
		// partial assembly AND cancel a running handle()/serve()
		// goroutine so the loopback body relay (a file being read) stops
		// at once.
		m.mu.Lock()
		delete(m.streams, f.StreamID)
		if sc, ok := m.cancels[f.StreamID]; ok {
			sc.cancel()
			delete(m.cancels, f.StreamID)
		}
		m.mu.Unlock()
	}
}

// reject answers a stream with an empty-bodied status (no loopback call).
func (m *Multiplexer) reject(streamID uint32, code int) {
	var head bytes.Buffer
	fmt.Fprintf(&head, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\n\r\n", code, http.StatusText(code))
	if err := m.writeFrame(Frame{Kind: KindResponseHead, StreamID: streamID, Payload: head.Bytes()}); err != nil {
		return
	}
	_ = m.writeFrame(Frame{Kind: KindResponseBody, StreamID: streamID})
}

// credit is one stream's send window: RESPONSE_BODY bytes the cloud
// still accepts.
type credit struct {
	mu    sync.Mutex
	avail int64
	more  chan struct{}
}

func newCredit(n int64) *credit { return &credit{avail: n, more: make(chan struct{}, 1)} }

func (c *credit) add(n int64) {
	c.mu.Lock()
	c.avail += n
	c.mu.Unlock()
	select {
	case c.more <- struct{}{}:
	default:
	}
}

// take waits until n bytes of credit are available and spends them.
func (c *credit) take(ctx context.Context, n int64) error {
	timer := time.NewTimer(creditTimeout)
	defer timer.Stop()
	for {
		c.mu.Lock()
		if c.avail >= n {
			c.avail -= n
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		select {
		case <-c.more:
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("cloudlink: the cloud granted no window")
		}
	}
}

// sink adapts the multiplexer's serialized writer to the proxy's
// frameSink interface for one stream, spending its window (flow) before
// each body chunk.
type sink struct {
	m   *Multiplexer
	ctx context.Context
	cr  *credit // nil: unwindowed
}

func (s sink) WriteFrame(f Frame) error {
	if s.cr != nil && f.Kind == KindResponseBody && len(f.Payload) > 0 {
		if err := s.cr.take(s.ctx, int64(len(f.Payload))); err != nil {
			return errStreamAbandoned{err}
		}
	}
	return s.m.writeFrame(f)
}

// errStreamAbandoned is a stream ended on this side (its client left or
// the cloud stopped granting): the stream's end, not the tunnel's.
type errStreamAbandoned struct{ err error }

func (e errStreamAbandoned) Error() string { return e.err.Error() }

func (m *Multiplexer) handle(streamID uint32, asm *reqAssembly, cr *credit) {
	// Bound the whole request+body relay and make it cancelable so a
	// stalled loopback body cannot pin this goroutine forever (I-2) and
	// a cloud KindCloseStream / tunnel teardown unblocks it at once
	// (M-4). On timeout/cancel resp.Body.Read fails, so serve() still
	// emits a CLOSE_STREAM and the cloud's matching stream is freed.
	var ctx context.Context
	var cancel context.CancelFunc
	if cr != nil {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), serveTimeout)
	}
	defer cancel()
	sc := &streamCancel{cancel: cancel}

	m.mu.Lock()
	if m.closed.Load() {
		// Tunnel already torn down between dispatch and here; abandon.
		delete(m.credits, streamID)
		m.mu.Unlock()
		return
	}
	m.cancels[streamID] = sc
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		// Only delete our own handle: a later stream reusing this id (or
		// a CloseStream that already cancelled+deleted it) must not have
		// its cancel clobbered.
		if cur, ok := m.cancels[streamID]; ok && cur == sc {
			delete(m.cancels, streamID)
		}
		if cur, ok := m.credits[streamID]; ok && cur == cr {
			delete(m.credits, streamID)
		}
		m.mu.Unlock()
	}()

	err := m.proxy.serve(ctx, streamID, asm, sink{m: m, ctx: ctx, cr: cr})
	var abandoned errStreamAbandoned
	if errors.As(err, &abandoned) {
		if ctx.Err() == nil {
			// no credit for creditTimeout: free the cloud's stream too
			_ = m.writeFrame(Frame{Kind: KindCloseStream, StreamID: streamID, Payload: []byte(err.Error())})
		}
		return
	}
	if err != nil && ctx.Err() == nil {
		// A write failure means the tunnel is gone; surface it so the
		// supervisor reconnects.
		m.fatal(err)
	}
}
