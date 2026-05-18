package cloudlink

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

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

	closeOnce sync.Once
	closed    atomic.Bool
	done      chan struct{}
	err       atomic.Value // error

	// counters surfaced via Stats() for the admin state endpoint.
	bytesIn  atomic.Int64
	bytesOut atomic.Int64
	reqs     atomic.Int64
}

// NewMultiplexer wires the read loop and returns immediately.
func NewMultiplexer(conn FrameConn, proxy *LocalProxy) *Multiplexer {
	m := &Multiplexer{
		conn:    conn,
		proxy:   proxy,
		streams: make(map[uint32]*reqAssembly),
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
		m.mu.Lock()
		delete(m.streams, f.StreamID)
		m.mu.Unlock()
	}
}

// sink adapts the multiplexer's serialized writer to the proxy's
// frameSink interface (exported method name) without exposing the
// internal writeFrame to other packages.
type sink struct{ m *Multiplexer }

func (s sink) WriteFrame(f Frame) error { return s.m.writeFrame(f) }

func (m *Multiplexer) handle(streamID uint32, asm *reqAssembly) {
	if err := m.proxy.serve(context.Background(), streamID, asm, sink{m}); err != nil {
		// A write failure means the tunnel is gone; surface it so the
		// supervisor reconnects.
		m.fatal(err)
	}
}
