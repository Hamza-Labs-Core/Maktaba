package fakegateway

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu   sync.Mutex
	open map[string]bool
	log  []string
}

func (r *recorder) Open(proto string, external int, to netip.AddrPort) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.open == nil {
		r.open = map[string]bool{}
	}
	k := fmt.Sprintf("%s %d %s", proto, external, to)
	r.open[k] = true
	r.log = append(r.log, "open "+k)
	return nil
}

func (r *recorder) Close(proto string, external int, to netip.AddrPort) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := fmt.Sprintf("%s %d %s", proto, external, to)
	delete(r.open, k)
	r.log = append(r.log, "close "+k)
	return nil
}

func mapReq(op byte, internal, want int, lifetime uint32) []byte {
	b := make([]byte, 12)
	b[1] = op
	binary.BigEndian.PutUint16(b[4:6], uint16(internal))
	binary.BigEndian.PutUint16(b[6:8], uint16(want))
	binary.BigEndian.PutUint32(b[8:12], lifetime)
	return b
}

// The rehearsal's router answers NAT-PMP as a home's client expects (RFC 6886): its external
// address; a TCP mapping opened by a DNAT rule to the asking host, kept on renewal, removed
// on a zero lifetime and when its lease runs out.
func TestTheGatewayMapsAsNATPMP(t *testing.T) {
	now := time.Unix(1000, 0)
	rules := &recorder{}
	g := &Gateway{WAN: netip.MustParseAddr("198.51.100.2"), Rules: rules, Now: func() time.Time { return now }}
	home := netip.MustParseAddr("192.168.77.20")

	resp := g.Answer([]byte{0, 0}, home)
	if len(resp) != 12 || resp[1] != 128 || netip.AddrFrom4([4]byte(resp[8:12])).String() != "198.51.100.2" {
		t.Fatalf("external address: %v", resp)
	}
	resp = g.Answer(mapReq(2, 8444, 41234, 3600), home)
	if len(resp) != 16 || resp[1] != 130 || binary.BigEndian.Uint16(resp[2:4]) != 0 ||
		binary.BigEndian.Uint16(resp[10:12]) != 41234 || binary.BigEndian.Uint32(resp[12:16]) != 3600 {
		t.Fatalf("map: %v", resp)
	}
	if !rules.open["tcp 41234 192.168.77.20:8444"] {
		t.Fatalf("no rule: %v", rules.log)
	}
	// a renewal asking another port keeps the one it has
	resp = g.Answer(mapReq(2, 8444, 45000, 3600), home)
	if binary.BigEndian.Uint16(resp[10:12]) != 41234 || len(rules.log) != 1 {
		t.Fatalf("renewal: %v %v", resp, rules.log)
	}
	// another host asking the same port gets another
	resp = g.Answer(mapReq(2, 8444, 41234, 3600), netip.MustParseAddr("192.168.77.21"))
	if p := binary.BigEndian.Uint16(resp[10:12]); p == 41234 || p == 0 {
		t.Fatalf("a second host got port %d", p)
	}
	// lifetime 0 removes the first host's mapping
	g.Answer(mapReq(2, 8444, 0, 0), home)
	if rules.open["tcp 41234 192.168.77.20:8444"] || g.Mappings() != 1 {
		t.Fatalf("delete: %v", rules.log)
	}
	// a lease past its lifetime closes
	now = now.Add(2 * time.Hour)
	g.expire()
	if g.Mappings() != 0 || len(rules.open) != 0 {
		t.Fatalf("expiry: %v", rules.log)
	}
	if resp := g.Answer([]byte{0, 9}, home); binary.BigEndian.Uint16(resp[2:4]) != 5 {
		t.Fatalf("an unknown opcode: %v", resp)
	}
	if g.Answer([]byte{2, 0}, home) != nil {
		t.Fatal("answered another version")
	}
}

// Over UDP, the way a home reaches it.
func TestTheGatewayServesUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := &Gateway{WAN: netip.MustParseAddr("198.51.100.2"), Rules: &recorder{}}
	go func() { _ = g.Serve(ctx, pc) }()
	c, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write(mapReq(2, 8444, 0, 60))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil || n != 16 || binary.BigEndian.Uint16(buf[10:12]) != 40000 {
		t.Fatalf("%d %v %v", n, err, buf[:n])
	}
}
