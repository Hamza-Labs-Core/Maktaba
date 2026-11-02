package mdns

// The host name zeroconf announces the service at, answered. zeroconf (grandcat/zeroconf)
// answers only questions for the service: the address records of its target come with
// those answers, but a question for the target itself ("<host>-maktaba.local A", which an
// app asks when it connects to the LocalHost a home names, or once those records left its
// cache) it never answers, so the name resolved to nothing (found by the real run in CI,
// mdns-linux). hostAddrs answers it, with the addresses zeroconf announces, beside zeroconf:
// only where zeroconf answers for the target (announce.go), never beside a system responder
// that holds the name. IPv4 transport (A and AAAA questions both), as a phone asks.
// The same file is in api/internal/discovery and cmd/maktaba-server/internal/mdns (two
// modules); tools/ci/mdns_copies.py checks they stay the same.

import (
	"errors"
	"net"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"
	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

// mdnsCacheFlush is the top bit of an answer's class: the record is the only one of its
// name and type (RFC 6762 §10.2).
const mdnsCacheFlush = 1 << 15

// hostAddrTTL is an address record's TTL (RFC 6762 §10: 120 s for A and AAAA).
const hostAddrTTL = 120

var mdnsIPv4Group = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// hostAnswer is the response to query when it asks for host's addresses ("Studio.local."),
// nil when it asks for nothing of host's. unicast: the asker wants the answer sent to it
// (a legacy resolver asking from a port other than 5353, which also gets the question and
// its id back, or a question with the unicast-response bit).
func hostAnswer(query *dns.Msg, host string, ips []net.IP, legacy bool) (resp *dns.Msg, unicast bool) {
	if query.Response || query.Opcode != dns.OpcodeQuery || len(query.Question) == 0 {
		return nil, false
	}
	resp = new(dns.Msg)
	resp.Response, resp.Authoritative = true, true
	for _, q := range query.Question {
		if !strings.EqualFold(dns.Fqdn(q.Name), dns.Fqdn(host)) {
			continue
		}
		before := len(resp.Answer)
		for _, ip := range ips {
			v4 := ip.To4()
			hdr := dns.RR_Header{Name: dns.Fqdn(host), Class: dns.ClassINET | mdnsCacheFlush, Ttl: hostAddrTTL}
			switch {
			case v4 != nil && (q.Qtype == dns.TypeA || q.Qtype == dns.TypeANY):
				hdr.Rrtype = dns.TypeA
				resp.Answer = append(resp.Answer, &dns.A{Hdr: hdr, A: v4})
			case v4 == nil && ip.To16() != nil && (q.Qtype == dns.TypeAAAA || q.Qtype == dns.TypeANY):
				hdr.Rrtype = dns.TypeAAAA
				resp.Answer = append(resp.Answer, &dns.AAAA{Hdr: hdr, AAAA: ip})
			}
		}
		if len(resp.Answer) > before && q.Qclass&mdnsCacheFlush != 0 {
			unicast = true
		}
	}
	if len(resp.Answer) == 0 {
		return nil, false
	}
	if legacy {
		// RFC 6762 §6.7: the id and the question back, no cache-flush bit
		resp.Id = query.Id
		resp.Question = query.Question
		for _, rr := range resp.Answer {
			rr.Header().Class &^= mdnsCacheFlush
			rr.Header().Ttl = min(rr.Header().Ttl, 10)
		}
		return resp, true
	}
	return resp, unicast
}

// hostAddrs answers the questions for one host name's addresses until closed.
type hostAddrs struct {
	conn   *ipv4.PacketConn
	host   string
	ips    []net.IP
	ifaces []net.Interface
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

func startHostAddrs(host string, ips []string) (*hostAddrs, error) {
	c, err := net.ListenUDP("udp4", mdnsIPv4Group) // a multicast address: SO_REUSEADDR, beside the other responders
	if err != nil {
		return nil, err
	}
	pc := ipv4.NewPacketConn(c)
	h := &hostAddrs{conn: pc, host: dns.Fqdn(host), done: make(chan struct{})}
	for _, s := range ips {
		if ip := net.ParseIP(s); ip != nil {
			h.ips = append(h.ips, ip)
		}
	}
	ifaces, _ := net.Interfaces()
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		if pc.JoinGroup(&ifi, mdnsIPv4Group) == nil {
			h.ifaces = append(h.ifaces, ifi)
		}
	}
	if len(h.ifaces) == 0 {
		_ = c.Close()
		return nil, errors.New("no multicast interface took the mDNS group")
	}
	_ = pc.SetControlMessage(ipv4.FlagInterface, true) // not on Windows: answers go out on every interface
	_ = pc.SetMulticastLoopback(true)
	// RFC 6762 §11: every response leaves with IP TTL 255 (Go's default for multicast is 1)
	_ = pc.SetMulticastTTL(255)
	_ = pc.SetTTL(255)
	h.announce(hostAddrTTL)
	go h.serve()
	return h, nil
}

func (h *hostAddrs) serve() {
	defer close(h.done)
	buf := make([]byte, 9000)
	for {
		n, cm, src, err := h.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		var q dns.Msg
		if q.Unpack(buf[:n]) != nil {
			continue
		}
		from, _ := src.(*net.UDPAddr)
		legacy := from != nil && from.Port != mdnsIPv4Group.Port
		resp, unicast := hostAnswer(&q, h.host, h.ips, legacy)
		if resp == nil {
			continue
		}
		ifIndex := 0
		if cm != nil {
			ifIndex = cm.IfIndex
		}
		if unicast && from != nil {
			h.send(resp, from, ifIndex)
		} else {
			h.send(resp, mdnsIPv4Group, ifIndex)
		}
	}
}

// send writes m to dst on the interface the question came in on (every one when unknown).
func (h *hostAddrs) send(m *dns.Msg, dst *net.UDPAddr, ifIndex int) {
	b, err := m.Pack()
	if err != nil {
		return
	}
	if ifIndex != 0 || !dst.IP.IsMulticast() {
		_, _ = h.conn.WriteTo(b, &ipv4.ControlMessage{IfIndex: ifIndex}, dst)
		return
	}
	for _, ifi := range h.ifaces {
		_, _ = h.conn.WriteTo(b, &ipv4.ControlMessage{IfIndex: ifi.Index}, dst)
	}
}

// announce sends the address records unasked (ttl 0: the goodbye).
func (h *hostAddrs) announce(ttl uint32) {
	m, _ := hostAnswer(&dns.Msg{Question: []dns.Question{{Name: h.host, Qtype: dns.TypeANY, Qclass: dns.ClassINET}}}, h.host, h.ips, false)
	if m == nil {
		return
	}
	for _, rr := range m.Answer {
		rr.Header().Ttl = ttl
	}
	h.send(m, mdnsIPv4Group, 0)
}

func (h *hostAddrs) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	h.announce(0)
	_ = h.conn.Close()
	<-h.done
}

// zeroconfHost is zeroconf's announcement with its target's addresses answered (addrs nil:
// they could not be, zeroconf announces alone).
type zeroconfHost struct {
	*zeroconf.Server
	addrs *hostAddrs
}

// Shutdown implements announcement.
func (z *zeroconfHost) Shutdown() {
	z.Server.Shutdown()
	if z.addrs != nil {
		z.addrs.close()
	}
}
