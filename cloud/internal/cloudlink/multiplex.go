package cloudlink

import (
	"context"
	"errors"
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
const serveTimeout = 10 * time.Minute

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

	closeOnce sync.Once
	closed    atomic.Bool
	done      chan struct{}
	err       atomic.Value // error

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

// NewMultiplexer wires the read loop and returns immediately.
func NewMultiplexer(conn FrameConn, proxy *LocalProxy) *Multiplexer {
	m := &Multiplexer{
		conn:    conn,
		proxy:   proxy,
		streams: make(map[uint32]*reqAssembly),
		cancels: make(map[uint32]*streamCancel),
		done:    make(chan struct{}),
	}
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
		// Reply to our own keepalive PING (health.go). Liveness is
		// tracked there via the read deadline; nothing to do here.
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
			m.mu.Unlock()
			m.reqs.Add(1)
			go m.handle(f.StreamID, asm)
			return
		}
		asm.body.Write(f.Payload)
		m.mu.Unlock()
	case KindCloseStream:
		// The cloud aborted this stream. Drop any partial assembly AND
		// cancel a running handle()/serve() goroutine so a wedged
		// loopback body relay is abandoned promptly (M-4): previously
		// this only deleted the map entry and left the goroutine pinned.
		m.mu.Lock()
		delete(m.streams, f.StreamID)
		if sc, ok := m.cancels[f.StreamID]; ok {
			sc.cancel()
			delete(m.cancels, f.StreamID)
		}
		m.mu.Unlock()
	}
}

// sink adapts the multiplexer's serialized writer to the proxy's
// frameSink interface (exported method name) without exposing the
// internal writeFrame to other packages.
type sink struct{ m *Multiplexer }

func (s sink) WriteFrame(f Frame) error { return s.m.writeFrame(f) }

func (m *Multiplexer) handle(streamID uint32, asm *reqAssembly) {
	// Bound the whole request+body relay and make it cancelable so a
	// stalled loopback body cannot pin this goroutine forever (I-2) and
	// a cloud KindCloseStream / tunnel teardown unblocks it at once
	// (M-4). On timeout/cancel resp.Body.Read fails, so serve() still
	// emits a CLOSE_STREAM and the cloud's matching stream is freed.
	ctx, cancel := context.WithTimeout(context.Background(), serveTimeout)
	defer cancel()
	sc := &streamCancel{cancel: cancel}

	m.mu.Lock()
	if m.closed.Load() {
		// Tunnel already torn down between dispatch and here; abandon.
		m.mu.Unlock()
		cancel()
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
		m.mu.Unlock()
	}()

	if err := m.proxy.serve(ctx, streamID, asm, sink{m}); err != nil {
		// A write failure means the tunnel is gone; surface it so the
		// supervisor reconnects.
		m.fatal(err)
	}
}
