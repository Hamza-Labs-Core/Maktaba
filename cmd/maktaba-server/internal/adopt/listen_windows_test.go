//go:build windows

package adopt

import (
	"net"
	"os"
	"testing"
)

// TestTheListenersAreRead: the listening sockets on a port, each with the
// address it is bound to and its process, from Windows' TCP tables (IPv4 and
// IPv6). Only loopback is bound here: nothing reaches past this PC.
func TestTheListenersAreRead(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	has := func(addr string) bool {
		ls, err := RealSys().Listeners(port)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range ls {
			if x.Addr == addr && x.PID == os.Getpid() {
				return true
			}
		}
		t.Logf("listeners on %s: %+v", port, ls)
		return false
	}
	if !has("127.0.0.1") {
		t.Error("this test's IPv4 listener is not read")
	}
	if l6, err := net.Listen("tcp", net.JoinHostPort("::1", port)); err == nil {
		if !has("::1") {
			t.Error("this test's IPv6 listener is not read")
		}
		_ = l6.Close()
	}
	_ = l.Close()
	if ls, err := RealSys().Listeners(port); err != nil || len(ls) != 0 {
		t.Errorf("a closed port: %+v %v", ls, err)
	}
	if _, err := RealSys().Listeners("http"); err == nil {
		t.Error("a port that is not a number is read")
	}
}
