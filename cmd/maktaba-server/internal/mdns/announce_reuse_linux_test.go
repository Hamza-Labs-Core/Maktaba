//go:build linux

package mdns

// Epic 59 story 59.08, T01: Maktaba's responder beside the NAS's own. On a NAS the host's
// avahi-daemon owns 5353 (bound with SO_REUSEADDR) and the container shares the host's
// network; zeroconf and the host-name answerer bind 5353 at a multicast address, which Go
// binds with SO_REUSEADDR, so both sockets bind and both get each query.
// The same file is in api/internal/discovery and cmd/maktaba-server/internal/mdns (two
// modules); tools/ci/mdns_copies.py checks they stay the same.

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/ipv4"
)

// listenLikeAvahi binds 0.0.0.0:5353 the way avahi-daemon does: SO_REUSEADDR first.
func listenLikeAvahi(t *testing.T) net.PacketConn {
	t.Helper()
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		}); err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:5353")
	if err != nil {
		t.Skipf("5353 cannot be bound here: %v", err)
	}
	return pc
}

func reuseAddrOn(t *testing.T, c *net.UDPConn) int {
	t.Helper()
	raw, err := c.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var v int
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		v, gerr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR)
	}); err != nil {
		t.Fatal(err)
	}
	if gerr != nil {
		t.Fatal(gerr)
	}
	return v
}

func TestResponderBindsBesideTheHostsResponder(t *testing.T) {
	host := listenLikeAvahi(t)
	defer func() { _ = host.Close() }()

	// the bind every socket of ours on 5353 makes (announce_hostaddr.go; zeroconf binds
	// 224.0.0.0:5353 the same way)
	ours, err := net.ListenUDP("udp4", mdnsIPv4Group)
	if err != nil {
		t.Fatalf("our responder cannot bind beside the host's: %v", err)
	}
	defer func() { _ = ours.Close() }()
	if reuseAddrOn(t, ours) == 0 {
		t.Fatal("our socket on 5353 is not bound with SO_REUSEADDR")
	}

	// a query to the group reaches both sockets
	ifi := firstMulticastInterface()
	if ifi == nil {
		t.Skip("no multicast interface to send a query on")
	}
	for _, pc := range []net.PacketConn{host, ours} {
		if err := ipv4.NewPacketConn(pc).JoinGroup(ifi, mdnsIPv4Group); err != nil {
			t.Skipf("cannot join the mDNS group on %s: %v", ifi.Name, err)
		}
	}
	send, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = send.Close() }()
	sp := ipv4.NewPacketConn(send)
	_ = sp.SetMulticastInterface(ifi)
	_ = sp.SetMulticastLoopback(true)
	query := []byte("maktaba-59.08-reuse")
	if _, err := send.WriteTo(query, mdnsIPv4Group); err != nil {
		t.Skipf("cannot send to the group: %v", err)
	}
	for name, pc := range map[string]net.PacketConn{"the host's responder": host, "ours": ours} {
		if !receives(pc, query) {
			t.Errorf("%s did not get the query", name)
		}
	}
}

func firstMulticastInterface() *net.Interface {
	for _, ifi := range announceInterfaces() {
		if ifi.Flags&net.FlagLoopback == 0 {
			return &ifi
		}
	}
	return nil
}

func receives(pc net.PacketConn, want []byte) bool {
	buf := make([]byte, 9000)
	deadline := time.Now().Add(3 * time.Second)
	_ = pc.SetReadDeadline(deadline)
	for time.Now().Before(deadline) {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return false
		}
		if string(buf[:n]) == string(want) {
			return true
		}
	}
	return false
}
