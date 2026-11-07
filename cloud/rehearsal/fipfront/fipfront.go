// Package fipfront stands in for the internet's side of the Floating IP in the
// cloud rehearsal (Epic 50 story 50.07): a TCP proxy that resolves the IP's name
// (relay.test, the Docker network alias fakehcloud moves to the assigned node)
// for each new connection, so connections made after a move reach the new
// holder and the ones made before die with the old one, as with the real IP.
package fipfront

import (
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
