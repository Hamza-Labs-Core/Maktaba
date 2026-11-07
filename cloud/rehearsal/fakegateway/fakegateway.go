// Package fakegateway is the rehearsal's home router (Epic 50 story 50.15, scenario
// direct-remote): a NAT-PMP server (RFC 6886) on the LAN side that opens the port a home asks
// for, by a DNAT rule from its WAN address to the home, and closes it when the home asks or
// the lease runs out. The router's NAT itself (masquerading the LAN) is set up once at start.
// It is a stand-in: no PCP, no UPnP, one WAN address.
package fakegateway

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Rules opens and closes a port on the router (iptables in the rehearsal, a recorder in tests).
type Rules interface {
	Open(proto string, external int, to netip.AddrPort) error
	Close(proto string, external int, to netip.AddrPort) error
}

type lease struct {
	proto    string
	to       netip.AddrPort
	external int
	until    time.Time
}

// Gateway answers NAT-PMP requests from the LAN.
type Gateway struct {
	WAN   netip.Addr // the router's external address, as the home is told
	Rules Rules
	Now   func() time.Time

	mu     sync.Mutex
	start  time.Time
	leases map[string]*lease // by proto/external port
	next   int
}

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Serve answers on pc until ctx ends; leases past their lifetime are closed every second.
func (g *Gateway) Serve(ctx context.Context, pc net.PacketConn) error {
	g.mu.Lock()
	g.start = g.now()
	g.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				g.expire()
			}
		}
	}()
	buf := make([]byte, 64)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		src, _ := netip.AddrFromSlice(ua.IP)
		if resp := g.Answer(buf[:n], src.Unmap()); resp != nil {
			_, _ = pc.WriteTo(resp, from)
		}
	}
}

func (g *Gateway) epoch() uint32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.start.IsZero() {
		g.start = g.now()
	}
	return uint32(g.now().Sub(g.start) / time.Second)
}

// Answer is the response to one request from the LAN host src (nil: not NAT-PMP).
func (g *Gateway) Answer(req []byte, src netip.Addr) []byte {
	if len(req) < 2 || req[0] != 0 {
		return nil
	}
	op := req[1]
	switch {
	case op == 0 && len(req) >= 2:
		resp := make([]byte, 12)
		resp[1] = 128
		binary.BigEndian.PutUint32(resp[4:8], g.epoch())
		copy(resp[8:12], g.WAN.AsSlice())
		return resp
	case (op == 1 || op == 2) && len(req) >= 12:
		proto := "udp"
		if op == 2 {
			proto = "tcp"
		}
		internal := int(binary.BigEndian.Uint16(req[4:6]))
		want := int(binary.BigEndian.Uint16(req[6:8]))
		lifetime := binary.BigEndian.Uint32(req[8:12])
		resp := make([]byte, 16)
		resp[1] = 128 + op
		binary.BigEndian.PutUint32(resp[4:8], g.epoch())
		binary.BigEndian.PutUint16(resp[8:10], uint16(internal))
		external, code := g.mapPort(proto, netip.AddrPortFrom(src, uint16(internal)), want, lifetime)
		binary.BigEndian.PutUint16(resp[2:4], code)
		if code == 0 {
			binary.BigEndian.PutUint16(resp[10:12], uint16(external))
			binary.BigEndian.PutUint32(resp[12:16], lifetime)
		}
		return resp
	default:
		resp := make([]byte, 8)
		resp[1] = 128 + op
		binary.BigEndian.PutUint16(resp[2:4], 5) // unsupported opcode
		binary.BigEndian.PutUint32(resp[4:8], g.epoch())
		return resp
	}
}

// mapPort opens (lifetime > 0) or closes (0) a mapping; the result code is NAT-PMP's.
func (g *Gateway) mapPort(proto string, to netip.AddrPort, want int, lifetime uint32) (int, uint16) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.leases == nil {
		g.leases = map[string]*lease{}
	}
	if lifetime == 0 { // delete every mapping of this host's internal port
		for k, l := range g.leases {
			if l.proto == proto && l.to == to {
				if err := g.Rules.Close(proto, l.external, to); err != nil {
					log.Printf("fakegateway: close %s %d: %v", proto, l.external, err)
				}
				delete(g.leases, k)
			}
		}
		return 0, 0
	}
	for _, l := range g.leases { // a renewal keeps its port
		if l.proto == proto && l.to == to {
			l.until = g.now().Add(time.Duration(lifetime) * time.Second)
			return l.external, 0
		}
	}
	external := want
	if external == 0 || g.leases[fmt.Sprintf("%s/%d", proto, external)] != nil {
		if g.next == 0 {
			g.next = 40000
		}
		for g.leases[fmt.Sprintf("%s/%d", proto, g.next)] != nil {
			g.next++
		}
		external = g.next
	}
	if err := g.Rules.Open(proto, external, to); err != nil {
		log.Printf("fakegateway: open %s %d -> %s: %v", proto, external, to, err)
		return 0, 3 // network failure
	}
	g.leases[fmt.Sprintf("%s/%d", proto, external)] = &lease{proto: proto, to: to, external: external,
		until: g.now().Add(time.Duration(lifetime) * time.Second)}
	return external, 0
}

func (g *Gateway) expire() {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for k, l := range g.leases {
		if now.After(l.until) {
			if err := g.Rules.Close(l.proto, l.external, l.to); err != nil {
				log.Printf("fakegateway: expire %s %d: %v", l.proto, l.external, err)
			}
			delete(g.leases, k)
		}
	}
}

// Mappings is how many ports are open now.
func (g *Gateway) Mappings() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.leases)
}
