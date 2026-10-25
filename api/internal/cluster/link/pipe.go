package link

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Timings of design §18.4–18.6.
var (
	writeTimeout   = 10 * time.Second
	pingEvery      = 15 * time.Second
	pingTimeout    = 15 * time.Second
	missedPings    = 3
	ticketLife     = 30 * time.Second
	offerWait      = 10 * time.Second
	backoffFloor   = time.Second
	backoffCeiling = 30 * time.Second
)

// streamConn is a data stream as a byte pipe: one binary message per write of at most
// MaxChunk bytes. One goroutine reads the messages into a bounded buffer (so TCP still
// pushes back on a fast sender), and a read deadline only interrupts a read (http.Server
// sets one in the past to stop its background read when a handler hijacks the
// connection; the stream must go on after it). remote is the peer's address as the HTTP
// request carried it.
type streamConn struct {
	ws     *websocket.Conn
	remote net.Addr
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	cond     *sync.Cond
	buf      []byte
	rerr     error
	deadline time.Time
	timer    *time.Timer

	wmu       sync.Mutex
	wdeadline time.Time

	once   sync.Once
	closed chan struct{}
}

// maxBuffered is how much of a stream is read ahead of its reader.
const maxBuffered = 4 * MaxChunk

func newStreamConn(c *websocket.Conn, remote net.Addr) *streamConn {
	ctx, cancel := context.WithCancel(context.Background())
	c.SetReadLimit(MaxChunk + 1024)
	s := &streamConn{ws: c, remote: remote, ctx: ctx, cancel: cancel, closed: make(chan struct{})}
	s.cond = sync.NewCond(&s.mu)
	go s.pump()
	return s
}

func (s *streamConn) pump() {
	for {
		typ, data, err := s.ws.Read(s.ctx)
		s.mu.Lock()
		if err != nil {
			switch {
			case s.rerr != nil:
			case websocket.CloseStatus(err) == websocket.StatusNormalClosure, websocket.CloseStatus(err) == websocket.StatusGoingAway,
				errors.Is(err, io.EOF), errors.Is(err, context.Canceled):
				s.rerr = io.EOF
			default:
				s.rerr = err
			}
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
		if typ != websocket.MessageBinary {
			s.rerr = errors.New("link: a text message on a data stream")
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
		for len(s.buf) >= maxBuffered && s.rerr == nil {
			s.cond.Wait()
		}
		s.buf = append(s.buf, data...)
		s.cond.Broadcast()
		s.mu.Unlock()
		runtime.Gosched() // as Write: a bulk stream read leaves the CPU between messages
	}
}

// timeoutError is a passed deadline (a net.Error that times out).
type timeoutError struct{}

func (timeoutError) Error() string   { return "link: i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func (s *streamConn) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.buf) == 0 && s.rerr == nil {
		if !s.deadline.IsZero() && !time.Now().Before(s.deadline) {
			return 0, timeoutError{}
		}
		s.cond.Wait()
	}
	if len(s.buf) == 0 {
		return 0, s.rerr
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	if len(s.buf) == 0 {
		s.buf = nil
	}
	s.cond.Broadcast()
	return n, nil
}

// Write splits b into messages of at most MaxChunk bytes.
func (s *streamConn) Write(b []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	n := 0
	for len(b) > 0 {
		k := min(len(b), MaxChunk)
		ctx, cancel := context.WithTimeout(s.ctx, time.Minute)
		if !s.wdeadline.IsZero() {
			cancel()
			ctx, cancel = context.WithDeadline(s.ctx, s.wdeadline)
		}
		err := s.ws.Write(ctx, websocket.MessageBinary, b[:k])
		cancel()
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return n, timeoutError{}
			}
			return n, err
		}
		n += k
		b = b[k:]
		// a bulk stream (a release download) yields the CPU between messages: the control
		// channel's goroutines get it within one message's time, not after a whole slice
		runtime.Gosched()
	}
	return n, nil
}

func (s *streamConn) LocalAddr() net.Addr { return websocketAddr{} }

func (s *streamConn) RemoteAddr() net.Addr {
	if s.remote != nil {
		return s.remote
	}
	return websocketAddr{}
}

func (s *streamConn) SetDeadline(t time.Time) error {
	_ = s.SetWriteDeadline(t)
	return s.SetReadDeadline(t)
}

func (s *streamConn) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deadline = t
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if !t.IsZero() {
		s.timer = time.AfterFunc(max(time.Until(t), 0), func() {
			s.mu.Lock()
			s.cond.Broadcast()
			s.mu.Unlock()
		})
	}
	s.cond.Broadcast()
	return nil
}

func (s *streamConn) SetWriteDeadline(t time.Time) error {
	s.wmu.Lock()
	s.wdeadline = t
	s.wmu.Unlock()
	return nil
}

// Close closes the stream (once, the close handshake in the background) and tells Done.
func (s *streamConn) Close() error {
	s.shut(websocket.StatusNormalClosure, "")
	return nil
}

func (s *streamConn) shut(code websocket.StatusCode, reason string) {
	s.once.Do(func() {
		s.mu.Lock()
		if s.rerr == nil {
			s.rerr = net.ErrClosed
		}
		s.cond.Broadcast()
		s.mu.Unlock()
		close(s.closed)
		go func() {
			_ = s.ws.Close(code, reason)
			s.cancel()
		}()
	})
}

// Done is closed once the stream is.
func (s *streamConn) Done() <-chan struct{} { return s.closed }

// closeNow drops the stream at once (revocation, grace over).
func (s *streamConn) closeNow(code websocket.StatusCode, reason string) { s.shut(code, reason) }

type websocketAddr struct{}

func (websocketAddr) Network() string { return "websocket" }
func (websocketAddr) String() string  { return "link-stream" }

// oneConn is a listener that hands out one connection, then waits for it to close.
type oneConn struct {
	c    *streamConn
	once sync.Once
	ch   chan net.Conn
}

func newOneConn(c *streamConn) *oneConn {
	l := &oneConn{c: c, ch: make(chan net.Conn, 1)}
	l.ch <- c
	return l
}

func (l *oneConn) Accept() (net.Conn, error) {
	select {
	case c, ok := <-l.ch:
		if ok {
			return c, nil
		}
	default:
	}
	<-l.c.Done()
	return nil, net.ErrClosed
}

func (l *oneConn) Close() error   { l.once.Do(func() { close(l.ch) }); return nil }
func (l *oneConn) Addr() net.Addr { return l.c.LocalAddr() }

// serveOne serves the HTTP requests a stream carries with h (one request: no keep-alive)
// and returns once the stream is closed. ctxFn marks each request (the machine it came
// from).
func serveOne(c *streamConn, h http.Handler, ctxFn func(context.Context) context.Context) {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second,
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context { return ctxFn(ctx) }}
	srv.SetKeepAlivesEnabled(false)
	_ = srv.Serve(newOneConn(c))
	<-c.Done()
}
