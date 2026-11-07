package direct

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// TXTSource gives the ACME challenge values for _acme-challenge.<home id>.<domain>.
type TXTSource interface {
	ChallengeTXT(ctx context.Context, name string) ([]string, error)
}

// Responder is the direct-name domains' authoritative name server (story 50.15 §3, --role dns).
// A and AAAA are computed from the name, so a changed address needs no update anywhere; TXT comes
// from TXTSource (the ACME challenges); NS and SOA name ns1 and ns2. It refuses names under no
// configured domain (it is no open resolver), answers ANY with a minimal NOTIMP (no amplification),
// and rate-limits each source.
type Responder struct {
	Domains    func() []string // read on every query: a domain appended to the config answers at once
	NS         [2]netip.Addr   // ns1 and ns2: nodes A and B
	TXT        TXTSource
	Hostmaster string // the SOA's mailbox, as a name: hostmaster.<domain> when empty
	// CAA is who may issue for the domains (the responder is authoritative, so CAs ask it):
	// Let's Encrypt, ZeroSSL (sectigo.com) and Google Trust Services (pki.goog) when empty.
	CAA   []string
	TTL   uint32     // answers (300 s)
	Limit *RateLimit // per source; nil: none
	Log   *slog.Logger
}

func (r *Responder) ttl() uint32 {
	if r.TTL == 0 {
		return 300
	}
	return r.TTL
}

// Answer builds the reply to one query packet (nil: drop it).
func (r *Responder) Answer(ctx context.Context, packet []byte, from netip.Addr) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(packet)
	if err != nil || h.Response {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	resp := dnsmessage.Header{ID: h.ID, Response: true, OpCode: h.OpCode, RecursionDesired: h.RecursionDesired}
	if r.Limit != nil && !r.Limit.Allow(from, time.Now()) {
		resp.RCode = dnsmessage.RCodeRefused
		return build(resp, q, nil)
	}
	if h.OpCode != 0 {
		resp.RCode = dnsmessage.RCodeNotImplemented
		return build(resp, q, nil)
	}
	parsed, inZone, perr := Parse(q.Name.String(), r.Domains())
	if !inZone {
		resp.RCode = dnsmessage.RCodeRefused
		return build(resp, q, nil)
	}
	resp.Authoritative = true
	if q.Type == dnsmessage.TypeALL {
		resp.RCode = dnsmessage.RCodeNotImplemented
		return build(resp, q, nil)
	}
	if perr != nil {
		resp.RCode = dnsmessage.RCodeNameError
		return build(resp, q, []dnsmessage.Resource{r.soa(parsed.Domain)})
	}
	rh := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: r.ttl()}
	var ans []dnsmessage.Resource
	switch {
	case parsed.Addr.IsValid():
		ans = addrAnswer(rh, q.Type, parsed.Addr)
	case parsed.NS > 0:
		ans = addrAnswer(rh, q.Type, r.NS[parsed.NS-1])
	case parsed.ACME && q.Type == dnsmessage.TypeTXT:
		vals, err := r.TXT.ChallengeTXT(ctx, strings.TrimSuffix(strings.ToLower(q.Name.String()), "."))
		if err != nil {
			if r.Log != nil {
				r.Log.Warn("direct dns: challenge lookup failed", "err", err)
			}
			resp.RCode = dnsmessage.RCodeServerFailure
			return build(resp, q, nil)
		}
		rh.TTL = 30
		for _, v := range vals {
			ans = append(ans, dnsmessage.Resource{Header: rh, Body: &dnsmessage.TXTResource{TXT: []string{v}}})
		}
	case parsed.Apex && q.Type == dnsmessage.TypeNS:
		for i := 1; i <= 2; i++ {
			ns, _ := dnsmessage.NewName("ns" + string(rune('0'+i)) + "." + parsed.Domain + ".")
			ans = append(ans, dnsmessage.Resource{Header: rh, Body: &dnsmessage.NSResource{NS: ns}})
		}
	case parsed.Apex && q.Type == dnsmessage.TypeSOA:
		ans = []dnsmessage.Resource{r.soa(parsed.Domain)}
	case parsed.Apex && q.Type == TypeCAA:
		issuers := r.CAA
		if len(issuers) == 0 {
			issuers = DefaultCAA
		}
		for _, iss := range issuers {
			ans = append(ans, dnsmessage.Resource{Header: rh, Body: &dnsmessage.UnknownResource{Type: TypeCAA, Data: caaIssue(iss)}})
		}
	}
	if len(ans) == 0 {
		// the name exists, not this type: NOERROR with the SOA (NODATA)
		return build(resp, q, []dnsmessage.Resource{r.soa(parsed.Domain)}, true)
	}
	return build(resp, q, ans)
}

// TypeCAA is the CAA record's type (RFC 8659; not among dnsmessage's named types).
const TypeCAA dnsmessage.Type = 257

// DefaultCAA lets the three CAs of story 50.15 §4 issue.
var DefaultCAA = []string{"letsencrypt.org", "sectigo.com", "pki.goog"}

// caaIssue is one CAA record's data: flags 0, tag "issue", the issuer's domain.
func caaIssue(issuer string) []byte {
	return append(append([]byte{0, 5}, "issue"...), issuer...)
}

func addrAnswer(rh dnsmessage.ResourceHeader, t dnsmessage.Type, ip netip.Addr) []dnsmessage.Resource {
	switch {
	case t == dnsmessage.TypeA && ip.Is4():
		return []dnsmessage.Resource{{Header: rh, Body: &dnsmessage.AResource{A: ip.As4()}}}
	case t == dnsmessage.TypeAAAA && ip.Is6() && !ip.Is4In6():
		return []dnsmessage.Resource{{Header: rh, Body: &dnsmessage.AAAAResource{AAAA: ip.As16()}}}
	}
	return nil
}

func (r *Responder) soa(domain string) dnsmessage.Resource {
	name, _ := dnsmessage.NewName(domain + ".")
	ns, _ := dnsmessage.NewName("ns1." + domain + ".")
	mbox := r.Hostmaster
	if mbox == "" {
		mbox = "hostmaster." + domain
	}
	mb, _ := dnsmessage.NewName(strings.TrimSuffix(mbox, ".") + ".")
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET, TTL: r.ttl()},
		Body:   &dnsmessage.SOAResource{NS: ns, MBox: mb, Serial: 1, Refresh: 3600, Retry: 600, Expire: 604800, MinTTL: 60},
	}
}

// build writes the reply: answers, or with authority (the SOA) set when nodata is given.
func build(h dnsmessage.Header, q dnsmessage.Question, rrs []dnsmessage.Resource, authority ...bool) []byte {
	b := dnsmessage.NewBuilder(nil, h)
	b.EnableCompression()
	_ = b.StartQuestions()
	_ = b.Question(q)
	isAuth := len(authority) > 0 && authority[0] || h.RCode == dnsmessage.RCodeNameError
	if isAuth {
		_ = b.StartAuthorities()
	} else {
		_ = b.StartAnswers()
	}
	for _, rr := range rrs {
		switch body := rr.Body.(type) {
		case *dnsmessage.AResource:
			_ = b.AResource(rr.Header, *body)
		case *dnsmessage.AAAAResource:
			_ = b.AAAAResource(rr.Header, *body)
		case *dnsmessage.TXTResource:
			_ = b.TXTResource(rr.Header, *body)
		case *dnsmessage.NSResource:
			_ = b.NSResource(rr.Header, *body)
		case *dnsmessage.SOAResource:
			_ = b.SOAResource(rr.Header, *body)
		case *dnsmessage.UnknownResource:
			_ = b.UnknownResource(rr.Header, *body)
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

// RateLimit is a token bucket per source address.
type RateLimit struct {
	PerSecond float64
	Burst     float64
	mu        sync.Mutex
	buckets   map[netip.Addr]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

// Allow takes one token from from's bucket.
func (l *RateLimit) Allow(from netip.Addr, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[netip.Addr]*bucket{}
	}
	b, ok := l.buckets[from]
	if !ok {
		if len(l.buckets) > 100_000 { // bounded: forget everyone rather than grow
			l.buckets = map[netip.Addr]*bucket{}
		}
		b = &bucket{tokens: l.Burst, at: now}
		l.buckets[from] = b
	}
	b.tokens = min(l.Burst, b.tokens+now.Sub(b.at).Seconds()*l.PerSecond)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// ServeUDP answers on pc until ctx ends.
func (r *Responder) ServeUDP(ctx context.Context, pc net.PacketConn) error {
	go func() { <-ctx.Done(); _ = pc.Close() }()
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		from := addrOf(addr)
		if out := r.Answer(ctx, buf[:n], from); out != nil {
			if len(out) > 512 { // no EDNS: a truncated answer sends the client to TCP
				out = truncate(out)
			}
			_, _ = pc.WriteTo(out, addr)
		}
	}
}

// ServeTCP answers on ln until ctx ends (2-byte length framing).
func (r *Responder) ServeTCP(ctx context.Context, ln net.Listener) error {
	go func() { <-ctx.Done(); _ = ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go func() {
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			var l [2]byte
			if _, err := io.ReadFull(c, l[:]); err != nil {
				return
			}
			q := make([]byte, binary.BigEndian.Uint16(l[:]))
			if _, err := io.ReadFull(c, q); err != nil {
				return
			}
			out := r.Answer(ctx, q, addrOf(c.RemoteAddr()))
			if out == nil {
				return
			}
			binary.BigEndian.PutUint16(l[:], uint16(len(out)))
			_, _ = c.Write(append(l[:], out...))
		}()
	}
}

func addrOf(a net.Addr) netip.Addr {
	switch v := a.(type) {
	case *net.UDPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	case *net.TCPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	}
	return netip.Addr{}
}

// truncate keeps the header and question, sets TC.
func truncate(msg []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	h.Truncated = true
	return build(h, q, nil)
}
