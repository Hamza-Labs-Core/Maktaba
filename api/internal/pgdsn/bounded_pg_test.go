package pgdsn

import (
	"context"
	"database/sql"
	"io"
	"net"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

// hangingProxy forwards to a Postgres until hang: then what it is sent is never
// answered (a server switched off), its connections stay open and silent.
type hangingProxy struct {
	ln      net.Listener
	target  string
	mu      sync.Mutex
	hanging bool
	ups     []net.Conn
}

func (p *hangingProxy) serve(c net.Conn) {
	p.mu.Lock()
	hanging := p.hanging
	p.mu.Unlock()
	if hanging {
		_, _ = io.Copy(io.Discard, c) // taken, never answered
		return
	}
	up, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = c.Close()
		return
	}
	p.mu.Lock()
	p.ups = append(p.ups, up)
	p.mu.Unlock()
	go func() { _, _ = io.Copy(up, c) }()
	_, _ = io.Copy(c, up)
	_, _ = io.Copy(io.Discard, c) // the client's side stays open and silent
}

func (p *hangingProxy) hang() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hanging = true
	for _, up := range p.ups {
		_ = up.Close()
	}
}

// #157: on a connection whose server stops answering, a statement ends with its
// context: lib/pq alone waits for as long as the socket lives.
func TestABoundedConnectionGivesUpWithItsContext(t *testing.T) {
	base := os.Getenv("MAKTABA_TEST_PG_URL")
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p := &hangingProxy{ln: ln, target: u.Host}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(c)
		}
	}()
	u.Host = ln.Addr().String()
	conn, err := BoundedConnector(u.String())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(conn)
	defer db.Close()
	db.SetMaxOpenConns(1)
	var one int
	if err := db.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("before: %v", err)
	}
	p.hang()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	err = db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
	if err == nil {
		t.Fatal("a statement answered by a server that is gone")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("waited %s for a statement whose context ended after 1 s", d)
	}
	ectx, ecancel := context.WithTimeout(context.Background(), time.Second)
	defer ecancel()
	start = time.Now()
	if _, err := db.ExecContext(ectx, `SELECT 1`); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("exec on the hanging server: %v after %s", err, time.Since(start))
	}
}
