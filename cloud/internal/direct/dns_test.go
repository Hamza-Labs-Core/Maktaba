package direct

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type txts map[string][]string

func (t txts) ChallengeTXT(_ context.Context, name string) ([]string, error) { return t[name], nil }

func query(t *testing.T, r *Responder, name string, typ dnsmessage.Type) (dnsmessage.Header, []dnsmessage.Resource, []dnsmessage.Resource, int) {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, RecursionDesired: true})
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET})
	q, _ := b.Finish()
	out := r.Answer(context.Background(), q, netip.MustParseAddr("198.51.100.7"))
	if out == nil {
		t.Fatalf("%s %v: no answer", name, typ)
	}
	var m dnsmessage.Message
	if err := m.Unpack(out); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m.Header, m.Answers, m.Authorities, len(out)
}

// T02: computed A and AAAA, TXT from the store, NS and CAA at the apex; out of zone refused;
// ANY not answered (no amplification).
func TestTheResponder(t *testing.T) {
	id := HomeID("s1")
	domains := []string{"direct-a.example", "direct-b.example"}
	r := &Responder{
		Domains: func() []string { return domains },
		NS:      [2]netip.Addr{netip.MustParseAddr("203.0.113.11"), netip.MustParseAddr("203.0.113.12")},
		TXT:     txts{"_acme-challenge." + id + ".direct-a.example": {"tok-1", "tok-2"}},
	}
	h, ans, _, _ := query(t, r, "81-2-3-4."+id+".direct-b.example.", dnsmessage.TypeA)
	if h.RCode != dnsmessage.RCodeSuccess || !h.Authoritative || len(ans) != 1 || ans[0].Body.(*dnsmessage.AResource).A != [4]byte{81, 2, 3, 4} {
		t.Fatalf("A: %+v %+v", h, ans)
	}
	_, ans, _, _ = query(t, r, "2001-db8--1."+id+".direct-a.example.", dnsmessage.TypeAAAA)
	if len(ans) != 1 || netip.AddrFrom16(ans[0].Body.(*dnsmessage.AAAAResource).AAAA) != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("AAAA: %+v", ans)
	}
	// an IPv4 name asked for AAAA: it exists, no data
	h, ans, auth, _ := query(t, r, "81-2-3-4."+id+".direct-b.example.", dnsmessage.TypeAAAA)
	if h.RCode != dnsmessage.RCodeSuccess || len(ans) != 0 || len(auth) != 1 {
		t.Fatalf("NODATA: %+v %d %d", h, len(ans), len(auth))
	}
	_, ans, _, _ = query(t, r, "_acme-challenge."+id+".direct-a.example.", dnsmessage.TypeTXT)
	if len(ans) != 2 || ans[0].Body.(*dnsmessage.TXTResource).TXT[0] != "tok-1" {
		t.Fatalf("TXT: %+v", ans)
	}
	_, ans, _, _ = query(t, r, "ns2.direct-a.example.", dnsmessage.TypeA)
	if len(ans) != 1 || ans[0].Body.(*dnsmessage.AResource).A != [4]byte{203, 0, 113, 12} {
		t.Fatalf("ns2: %+v", ans)
	}
	_, ans, _, _ = query(t, r, "direct-a.example.", dnsmessage.TypeNS)
	if len(ans) != 2 {
		t.Fatalf("NS: %+v", ans)
	}
	_, ans, _, _ = query(t, r, "direct-a.example.", TypeCAA)
	if len(ans) != 3 || string(ans[0].Body.(*dnsmessage.UnknownResource).Data) != "\x00\x05issueletsencrypt.org" {
		t.Fatalf("CAA: %+v", ans)
	}
	// the home's own level exists (a CAA lookup climbs through it)
	if h, _, _, _ := query(t, r, id+".direct-a.example.", TypeCAA); h.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("home level: %v", h.RCode)
	}
	if h, _, _, _ := query(t, r, "www.direct-a.example.", dnsmessage.TypeA); h.RCode != dnsmessage.RCodeNameError {
		t.Fatalf("no such name: %v", h.RCode)
	}
	for _, out := range []string{"example.com.", "relay.maktaba.tv.", "81-2-3-4." + id + ".maktaba.tv."} {
		if h, ans, _, _ := query(t, r, out, dnsmessage.TypeA); h.RCode != dnsmessage.RCodeRefused || len(ans) != 0 || h.Authoritative {
			t.Fatalf("%s: %v, not refused", out, h.RCode)
		}
	}
	h, ans, _, n := query(t, r, "direct-a.example.", dnsmessage.TypeALL)
	if h.RCode != dnsmessage.RCodeNotImplemented || len(ans) != 0 || n > 64 {
		t.Fatalf("ANY: %v, %d answers, %d bytes", h.RCode, len(ans), n)
	}
	// a domain appended to the configuration answers at once
	domains = append(domains, "direct-c.example")
	if h, _, _, _ := query(t, r, "81-2-3-4."+id+".direct-c.example.", dnsmessage.TypeA); h.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("a new domain: %v", h.RCode)
	}
}

func TestEachSourceIsRateLimited(t *testing.T) {
	l := &RateLimit{PerSecond: 10, Burst: 20}
	a, b := netip.MustParseAddr("198.51.100.1"), netip.MustParseAddr("198.51.100.2")
	now := time.Unix(0, 0)
	ok := 0
	for range 100 {
		if l.Allow(a, now) {
			ok++
		}
	}
	if ok != 20 || !l.Allow(b, now) || !l.Allow(a, now.Add(time.Second)) {
		t.Fatalf("burst %d", ok)
	}
}

func TestServingOverUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Responder{Domains: func() []string { return []string{"direct-a.example"} }, TXT: txts{}}
	go func() { _ = r.ServeUDP(ctx, pc) }()
	res := &net.Resolver{PreferGo: true, Dial: func(_ context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("udp", pc.LocalAddr().String())
	}}
	ips, err := res.LookupNetIP(ctx, "ip4", "10-0-0-7."+HomeID("s1")+".direct-a.example")
	if err != nil || len(ips) != 1 || ips[0] != netip.MustParseAddr("10.0.0.7") {
		t.Fatalf("%v %v", ips, err)
	}
}
