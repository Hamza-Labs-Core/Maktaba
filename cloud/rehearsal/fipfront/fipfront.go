// Package fipfront stands in for the internet's side of the Floating IP in the
// cloud rehearsal (Epic 50 story 50.07): a TCP proxy that resolves the IP's name
// (relay.test, the Docker network alias fakehcloud moves to the assigned node)
// for each new connection, so connections made after a move reach the new
// holder and the ones made before die with the old one, as with the real IP.
//
// Follow is the same for an address whose old holder may still run (story 50.18's database
// Floating IP, db.internal): when the name moves, the connections to the old holder are
// closed, as the new holder of a real Floating IP answers their next packet with a reset.
package fipfront

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

// Serve accepts on ln and pipes each connection to target (host:port, looked
// up for every connection) until ln is closed.
func Serve(ln net.Listener, target string) error {
	for {
		in, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			out, err := net.DialTimeout("tcp", target, 3*time.Second)
			if err != nil {
				_ = in.Close()
				return
			}
			pipe(in, out)
		}()
	}
}

func pipe(a, b net.Conn) {
	var once sync.Once
	closeBoth := func() { _ = a.Close(); _ = b.Close() }
	go func() { _, _ = io.Copy(a, b); once.Do(closeBoth) }()
	_, _ = io.Copy(b, a)
	once.Do(closeBoth)
}

// Resolver looks a host up (net.DefaultResolver; a stub in tests).
type Resolver func(ctx context.Context, host string) ([]string, error)

// Follow is Serve, and every `every` it looks target's host up again: once the name answers
// another address, each connection piped to an address it no longer answers is closed.
func Follow(ln net.Listener, target string, every time.Duration, resolve Resolver) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	if resolve == nil {
		resolve = net.DefaultResolver.LookupHost
	}
	var mu sync.Mutex
	conns := map[net.Conn]string{} // the client's side -> the address it was piped to
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tk := time.NewTicker(every)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
			}
			ctx, cancel := context.WithTimeout(context.Background(), every)
			addrs, err := resolve(ctx, host)
			cancel()
			if err != nil || len(addrs) == 0 {
				continue
			}
			now := map[string]bool{}
			for _, a := range addrs {
				now[a] = true
			}
			mu.Lock()
			for c, a := range conns {
				if !now[a] {
					_ = c.Close()
					delete(conns, c)
				}
			}
			mu.Unlock()
		}
	}()
	for {
		in, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			out, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 3*time.Second)
			if err != nil {
				_ = in.Close()
				return
			}
			ip, _, _ := net.SplitHostPort(out.RemoteAddr().String())
			mu.Lock()
			conns[in] = ip
			mu.Unlock()
			pipe(in, out)
			mu.Lock()
			delete(conns, in)
			mu.Unlock()
		}()
	}
}
