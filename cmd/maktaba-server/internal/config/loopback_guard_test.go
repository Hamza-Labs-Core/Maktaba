package config

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

// Tests never listen on the LAN: Windows asks to allow every new
// %TEMP%\go-build*\config.test.exe that binds a non-loopback address. TestMain
// installs guardedListen as listenTCP; a violation fails the run.

var (
	guardMu   sync.Mutex
	violation []string
)

func guardViolations() []string {
	guardMu.Lock()
	defer guardMu.Unlock()
	return append([]string(nil), violation...)
}

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
	return fmt.Errorf("test listens on non-loopback address %q: bind 127.0.0.1 or fake the probe", addr)
}

func guardedListen(network, addr string) (net.Listener, error) {
	if err := checkLoopback(addr); err != nil {
		guardMu.Lock()
		violation = append(violation, err.Error())
		guardMu.Unlock()
		return nil, err
	}
	return net.Listen(network, addr)
}

func TestLoopbackGuard(t *testing.T) {
	for _, bad := range []string{":8081", "0.0.0.0:8088", "[::]:8080", "192.168.1.20:8080"} {
		if checkLoopback(bad) == nil {
			t.Errorf("checkLoopback(%q) = nil, want an error", bad)
		}
	}
	for _, ok := range []string{"127.0.0.1:0", "[::1]:0", "localhost:0"} {
		if err := checkLoopback(ok); err != nil {
			t.Errorf("checkLoopback(%q) = %v", ok, err)
		}
	}
}
