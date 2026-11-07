package directaccess

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"time"
)

// NATPMP asks the gateway by NAT-PMP (RFC 6886) on UDP 5351.
type NATPMP struct {
	Gateway netip.AddrPort // the router, port 5351
	// Tries is how many times a request is sent (250 ms, doubling); default 4.
	Tries int
}

func (n *NATPMP) Name() string { return "natpmp" }

// request sends req and waits for a reply of opcode 128+op.
func (n *NATPMP) request(ctx context.Context, req []byte, size int) ([]byte, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", n.Gateway.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	tries := n.Tries
	if tries <= 0 {
		tries = 4
	}
	wait := 250 * time.Millisecond
	buf := make([]byte, 64)
	for i := 0; i < tries; i++ {
		if _, err := conn.Write(req); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(wait)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetReadDeadline(deadline)
		for {
			k, err := conn.Read(buf)
			if err != nil {
				break // timeout: send again
			}
			if k >= size && buf[0] == 0 && buf[1] == 128+req[1] {
				if rc := binary.BigEndian.Uint16(buf[2:4]); rc != 0 {
					return nil, fmt.Errorf("natpmp: result code %d", rc)
				}
				return append([]byte(nil), buf[:k]...), nil
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		wait *= 2
	}
	return nil, errors.New("natpmp: no answer")
}

// ExternalAddress asks the router's external IPv4 address.
func (n *NATPMP) ExternalAddress(ctx context.Context) (netip.Addr, error) {
	resp, err := n.request(ctx, []byte{0, 0}, 12)
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.AddrFrom4([4]byte(resp[8:12])), nil
}

// Map asks a TCP mapping (opcode 2).
func (n *NATPMP) Map(ctx context.Context, internal, external int, lifetime time.Duration) (Mapping, error) {
	ip, err := n.ExternalAddress(ctx)
	if err != nil {
		return Mapping{}, err
	}
	resp, err := n.request(ctx, natpmpMap(internal, external, lifetime), 16)
	if err != nil {
		return Mapping{}, err
	}
	return Mapping{By: n.Name(), Internal: int(binary.BigEndian.Uint16(resp[8:10])),
		External: int(binary.BigEndian.Uint16(resp[10:12])), ExternalIP: ip,
		Lifetime: time.Duration(binary.BigEndian.Uint32(resp[12:16])) * time.Second}, nil
}

// Unmap asks the mapping with lifetime 0 (RFC 6886 §3.4).
func (n *NATPMP) Unmap(ctx context.Context, m Mapping) error {
	_, err := n.request(ctx, natpmpMap(m.Internal, 0, 0), 16)
	return err
}

func natpmpMap(internal, external int, lifetime time.Duration) []byte {
	b := make([]byte, 12)
	b[1] = 2 // TCP
	binary.BigEndian.PutUint16(b[4:6], uint16(internal))
	binary.BigEndian.PutUint16(b[6:8], uint16(external))
	binary.BigEndian.PutUint32(b[8:12], uint32(lifetime/time.Second))
	return b
}

// PCP asks the gateway by the Port Control Protocol (RFC 6887) on UDP 5351: a MAP request for
// TCP, from this machine's address on the way to the router.
type PCP struct {
	Gateway netip.AddrPort
	Tries   int

	nonce [12]byte
}

func (p *PCP) Name() string { return "pcp" }

func (p *PCP) Map(ctx context.Context, internal, external int, lifetime time.Duration) (Mapping, error) {
	if p.nonce == ([12]byte{}) {
		for i := range p.nonce {
			p.nonce[i] = byte(rand.IntN(256))
		}
	}
	return p.do(ctx, internal, external, lifetime)
}

func (p *PCP) Unmap(ctx context.Context, m Mapping) error {
	_, err := p.do(ctx, m.Internal, m.External, 0)
	return err
}

func (p *PCP) do(ctx context.Context, internal, external int, lifetime time.Duration) (Mapping, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", p.Gateway.String())
	if err != nil {
		return Mapping{}, err
	}
	defer func() { _ = conn.Close() }()
	local, _ := netip.ParseAddrPort(conn.LocalAddr().String())
	req := make([]byte, 60)
	req[0], req[1] = 2, 1 // version 2, MAP
	binary.BigEndian.PutUint32(req[4:8], uint32(lifetime/time.Second))
	client := local.Addr().As16()
	copy(req[8:24], client[:])
	copy(req[24:36], p.nonce[:])
	req[36] = 6 // TCP
	binary.BigEndian.PutUint16(req[40:42], uint16(internal))
	binary.BigEndian.PutUint16(req[42:44], uint16(external))
	// suggested external address: none (::ffff:0.0.0.0)
	req[54], req[55] = 0xff, 0xff
	tries := p.Tries
	if tries <= 0 {
		tries = 4
	}
	wait := 250 * time.Millisecond
	buf := make([]byte, 1100)
	for i := 0; i < tries; i++ {
		if _, err := conn.Write(req); err != nil {
			return Mapping{}, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(wait))
		for {
			k, err := conn.Read(buf)
			if err != nil {
				break
			}
			if k < 60 || buf[0] != 2 || buf[1] != 0x81 || [12]byte(buf[24:36]) != p.nonce {
				continue
			}
			if rc := buf[3]; rc != 0 {
				return Mapping{}, fmt.Errorf("pcp: result code %d", rc)
			}
			ip := netip.AddrFrom16([16]byte(buf[44:60])).Unmap()
			return Mapping{By: p.Name(), Internal: int(binary.BigEndian.Uint16(buf[40:42])),
				External: int(binary.BigEndian.Uint16(buf[42:44])), ExternalIP: ip,
				Lifetime: time.Duration(binary.BigEndian.Uint32(buf[4:8])) * time.Second}, nil
		}
		if ctx.Err() != nil {
			return Mapping{}, ctx.Err()
		}
		wait *= 2
	}
	return Mapping{}, errors.New("pcp: no answer")
}
