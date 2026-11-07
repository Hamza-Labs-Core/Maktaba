package dbtest

import (
	"io"
	"net"
	neturl "net/url"
	"sync"
	"testing"
)

// Cut is a TCP proxy in front of a test database that a test can cut and
// restore: a database outage (a Patroni failover, story 50.05) without
// stopping the shared test server. While cut it drops every connection and
// refuses new ones.
type Cut struct {
	ln     net.Listener
	target string

	mu    sync.Mutex
	down  bool
	conns map[net.Conn]struct{}
}

// Cuttable returns url rewritten to go through a Cut, and the Cut.
func Cuttable(t *testing.T, url string) (string, *Cut) {
	t.Helper()
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &Cut{ln: ln, target: u.Host, conns: map[net.Conn]struct{}{}}
	go c.serve()
	t.Cleanup(func() { _ = ln.Close(); c.Down() })
	u.Host = ln.Addr().String()
	return u.String(), c
}

func (c *Cut) serve() {
	for {
		in, err := c.ln.Accept()
		if err != nil {
			return
		}
		c.mu.Lock()
		if c.down {
			c.mu.Unlock()
			_ = in.Close()
			continue
		}
		out, err := net.Dial("tcp", c.target)
		if err != nil {
			c.mu.Unlock()
			_ = in.Close()
			continue
		}
		c.conns[in], c.conns[out] = struct{}{}, struct{}{}
		c.mu.Unlock()
		go c.pipe(in, out)
		go c.pipe(out, in)
	}
}

func (c *Cut) pipe(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
	_ = src.Close()
	c.mu.Lock()
	delete(c.conns, dst)
	delete(c.conns, src)
	c.mu.Unlock()
}

// Down cuts the database off: open connections drop, new ones are refused.
func (c *Cut) Down() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.down = true
	for conn := range c.conns {
		_ = conn.Close()
	}
}

// Up lets connections through again.
func (c *Cut) Up() {
	c.mu.Lock()
	c.down = false
	c.mu.Unlock()
}
