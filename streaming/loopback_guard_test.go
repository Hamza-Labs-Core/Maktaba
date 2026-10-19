package main

import (
	"fmt"
	"net"
	"os"
	"reflect"
	"sync"
	"testing"
)

// Tests never listen on the LAN: Windows asks to allow every new
// %TEMP%\go-build*\streaming.test.exe that opens a socket on a non-loopback
// address, and a test has no business on the network anyway. TestMain installs
// guardedListen as listenTCP; a test that binds ":8081", "0.0.0.0:..." or a LAN
// IP gets an error and the run fails (a gRPC bind failure is only a warning in
// runServe, so the violation is recorded too).

var (
	guardMu         sync.Mutex
	guardViolations []string
)

// checkLoopback reports an error unless addr's host is a loopback address.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("test listens on non-loopback address %q: bind 127.0.0.1 instead", addr)
}

// guardedListen is net.Listen restricted to loopback.
func guardedListen(network, addr string) (net.Listener, error) {
	if err := checkLoopback(addr); err != nil {
		guardMu.Lock()
		guardViolations = append(guardViolations, err.Error())
		guardMu.Unlock()
		return nil, err
	}
	return net.Listen(network, addr)
}

// installLoopbackGuard swaps the guard in; the returned func reports the
// violations seen (called by TestMain after the run).
func installLoopbackGuard() func() int {
	listenTCP = guardedListen
	return func() int {
		guardMu.Lock()
		defer guardMu.Unlock()
		for _, v := range guardViolations {
			fmt.Fprintln(os.Stderr, "loopback guard:", v)
		}
		if len(guardViolations) > 0 {
			return 1
		}
		return 0
	}
}

func TestLoopbackGuard(t *testing.T) {
	if reflect.ValueOf(listenTCP).Pointer() != reflect.ValueOf(guardedListen).Pointer() {
		t.Fatal("TestMain must install the loopback guard as listenTCP")
	}
	for _, bad := range []string{":8081", "0.0.0.0:9050", "[::]:9101", "192.168.1.20:8081", ""} {
		if checkLoopback(bad) == nil {
			t.Errorf("checkLoopback(%q) = nil, want an error", bad)
		}
	}
	for _, ok := range []string{"127.0.0.1:0", "[::1]:0", "localhost:0"} {
		if err := checkLoopback(ok); err != nil {
			t.Errorf("checkLoopback(%q) = %v", ok, err)
		}
	}
	ln, err := guardedListen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback listen refused: %v", err)
	}
	_ = ln.Close()
}
