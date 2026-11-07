package direct

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
	"time"
)

// T07: open (this home), closed, another home's server, a slow one.
func TestTheOutsideCheck(t *testing.T) {
	home := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(ServerIDHeader, "s1")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer home.Close()
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(ServerIDHeader, "s2")
	}))
	defer other.Close()
	roots := x509.NewCertPool()
	roots.AddCert(home.Certificate())
	roots.AddCert(other.Certificate())
	port := func(s *httptest.Server) int {
		_, p, _ := net.SplitHostPort(s.Listener.Addr().String())
		n, _ := strconv.Atoi(p)
		return n
	}
	lo := netip.MustParseAddr("127.0.0.1")
	c := Checker{Roots: roots, Timeout: 2 * time.Second}

	// httptest's certificate names example.com: the home's name stands in for it
	if got := c.Check(context.Background(), lo, port(home), "example.com", "s1"); got != CheckOK {
		t.Fatalf("this home: %s", got)
	}
	if got := c.Check(context.Background(), lo, port(other), "example.com", "s1"); got != CheckWrongHome {
		t.Fatalf("another home answers: %s", got)
	}
	if got := c.Check(context.Background(), lo, port(home), "other-name.example", "s1"); got != CheckWrongHome {
		t.Fatalf("a certificate not for this name: %s", got)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if got := c.Check(context.Background(), lo, closedPort, "example.com", "s1"); got != CheckClosed {
		t.Fatalf("closed: %s", got)
	}
	// something that accepts and never speaks
	silent, _ := net.Listen("tcp", "127.0.0.1:0")
	defer func() { _ = silent.Close() }()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()
	quick := Checker{Roots: roots, Timeout: 300 * time.Millisecond}
	if got := quick.Check(context.Background(), lo, silent.Addr().(*net.TCPAddr).Port, "example.com", "s1"); got != CheckTimeout {
		t.Fatalf("silent: %s", got)
	}
}

func TestSharedAddresses(t *testing.T) {
	for _, s := range []string{"100.64.0.1", "100.127.255.254", "10.0.0.1", "192.168.1.1", "172.16.0.1", "127.0.0.1", "fd00::1", "fe80::1"} {
		if !Shared(netip.MustParseAddr(s)) {
			t.Errorf("%s is shared", s)
		}
	}
	for _, s := range []string{"81.2.3.4", "100.128.0.1", "2a01:4f8::1"} {
		if Shared(netip.MustParseAddr(s)) {
			t.Errorf("%s is public", s)
		}
	}
}
