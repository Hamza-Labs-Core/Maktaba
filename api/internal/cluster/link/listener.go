package link

import (
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// SniffTimeout bounds the wait for a connection's first byte.
const SniffTimeout = 10 * time.Second

// tlsHandshakeByte opens every TLS connection (a handshake record).
const tlsHandshakeByte = 0x16

// SniffListener splits one port between TLS and plain HTTP by the first byte each
// connection sends (design §18.4): a TLS handshake goes to a tls.Server with cfg (the
// http.Server then sees a *tls.Conn and fills r.TLS), anything else is served as it is,
// so browsers and apps keep plain HTTP on the API's port while machines speak mutual TLS
// on it. A connection is classified off the accept loop: a slow client holds up nobody.
type SniffListener struct {
	net.Listener
	cfg   *tls.Config
	conns chan net.Conn
	errs  chan error
	once  sync.Once
	done  chan struct{}
}

// NewSniffListener wraps ln.
func NewSniffListener(ln net.Listener, cfg *tls.Config) *SniffListener {
	s := &SniffListener{Listener: ln, cfg: cfg, conns: make(chan net.Conn), errs: make(chan error, 1),
		done: make(chan struct{})}
	go s.loop()
	return s
}

func (s *SniffListener) loop() {
	var delay time.Duration
	for {
		c, err := s.Listener.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				// a passing shortage (file descriptors): wait like http.Server does
				delay = min(max(delay*2, 5*time.Millisecond), time.Second)
				time.Sleep(delay)
				continue
			}
			select {
			case s.errs <- err:
			case <-s.done:
			}
			return
		}
		delay = 0
		safego.Go(nil, "link_classify", func() { s.classify(c) })
	}
}

func (s *SniffListener) classify(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(SniffTimeout))
	var first [1]byte
	n, err := c.Read(first[:])
	_ = c.SetReadDeadline(time.Time{})
	if err != nil || n == 0 {
		_ = c.Close()
		return
	}
	pc := &peeked{Conn: c, first: first[0], has: true}
	var out net.Conn = pc
	if first[0] == tlsHandshakeByte && s.cfg != nil {
		out = tls.Server(pc, s.cfg)
	}
	select {
	case s.conns <- out:
	case <-s.done:
		_ = c.Close()
	}
}

// Accept implements net.Listener.
func (s *SniffListener) Accept() (net.Conn, error) {
	select {
	case c := <-s.conns:
		return c, nil
	case err := <-s.errs:
		return nil, err
	case <-s.done:
		return nil, net.ErrClosed
	}
}

// Close implements net.Listener.
func (s *SniffListener) Close() error {
	s.once.Do(func() { close(s.done) })
	return s.Listener.Close()
}

// peeked is a connection whose first byte was read already.
type peeked struct {
	net.Conn
	first byte
	has   bool
	mu    sync.Mutex
}

func (p *peeked) Read(b []byte) (int, error) {
	p.mu.Lock()
	if p.has && len(b) > 0 {
		b[0] = p.first
		p.has = false
		p.mu.Unlock()
		return 1, nil
	}
	p.mu.Unlock()
	return p.Conn.Read(b)
}
