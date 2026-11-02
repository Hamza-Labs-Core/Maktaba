package mdns

import (
	"net"
	"testing"

	"github.com/miekg/dns"
)

func hostQuery(name string, qtype uint16, unicastBit bool) *dns.Msg {
	q := dns.Question{Name: name, Qtype: qtype, Qclass: dns.ClassINET}
	if unicastBit {
		q.Qclass |= mdnsCacheFlush
	}
	return &dns.Msg{MsgHdr: dns.MsgHdr{Id: 7}, Question: []dns.Question{q}}
}

var hostIPs = []net.IP{net.ParseIP("10.1.0.168"), net.ParseIP("fd00::1"), net.ParseIP("192.168.1.9")}

// A question for the target zeroconf announces gets its addresses: A for A, AAAA for
// AAAA, both for ANY, whatever the case of the name, with the cache-flush bit and 120 s.
func TestHostAnswerAddresses(t *testing.T) {
	cases := []struct {
		qtype uint16
		want  []string
	}{
		{dns.TypeA, []string{"10.1.0.168", "192.168.1.9"}},
		{dns.TypeAAAA, []string{"fd00::1"}},
		{dns.TypeANY, []string{"10.1.0.168", "fd00::1", "192.168.1.9"}},
	}
	for _, c := range cases {
		resp, unicast := hostAnswer(hostQuery("RunnerVM-Maktaba.local.", c.qtype, false), "runnervm-maktaba.local", hostIPs, false)
		if resp == nil || unicast {
			t.Fatalf("%s: resp %v, unicast %v", dns.TypeToString[c.qtype], resp, unicast)
		}
		if !resp.Response || !resp.Authoritative || resp.Id != 0 || len(resp.Question) != 0 {
			t.Errorf("%s: header %+v, question %v", dns.TypeToString[c.qtype], resp.MsgHdr, resp.Question)
		}
		var got []string
		for _, rr := range resp.Answer {
			h := rr.Header()
			if h.Name != "runnervm-maktaba.local." || h.Class != dns.ClassINET|mdnsCacheFlush || h.Ttl != 120 {
				t.Errorf("%s: header %+v", dns.TypeToString[c.qtype], h)
			}
			switch x := rr.(type) {
			case *dns.A:
				got = append(got, x.A.String())
			case *dns.AAAA:
				got = append(got, x.AAAA.String())
			}
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: %v, want %v", dns.TypeToString[c.qtype], got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %v, want %v", dns.TypeToString[c.qtype], got, c.want)
			}
		}
	}
}

// Another name, a type it has none of, a response, an empty question: no answer.
func TestHostAnswerOnlyItsOwn(t *testing.T) {
	host := "studio-maktaba.local."
	for name, q := range map[string]*dns.Msg{
		"another host":    hostQuery("studio.local.", dns.TypeA, false),
		"the service":     hostQuery("_maktaba._tcp.local.", dns.TypePTR, false),
		"a type it lacks": hostQuery(host, dns.TypeTXT, false),
		"a response":      {MsgHdr: dns.MsgHdr{Response: true}, Question: hostQuery(host, dns.TypeA, false).Question},
		"no question":     {},
	} {
		if resp, _ := hostAnswer(q, host, hostIPs, false); resp != nil {
			t.Errorf("%s: answered %v", name, resp.Answer)
		}
	}
	if resp, _ := hostAnswer(hostQuery(host, dns.TypeAAAA, false), host, []net.IP{net.ParseIP("10.0.0.1")}, false); resp != nil {
		t.Errorf("AAAA with no IPv6: answered %v", resp.Answer)
	}
}

// The unicast-response bit asks for a unicast answer; a legacy resolver (a port other than
// 5353) gets its id and question back, no cache-flush bit and a short TTL (RFC 6762 §6.7).
func TestHostAnswerUnicast(t *testing.T) {
	host := "studio-maktaba.local."
	if _, unicast := hostAnswer(hostQuery(host, dns.TypeA, true), host, hostIPs, false); !unicast {
		t.Error("QU question answered by multicast")
	}
	q := hostQuery(host, dns.TypeA, false)
	resp, unicast := hostAnswer(q, host, hostIPs, true)
	if resp == nil || !unicast || resp.Id != 7 || len(resp.Question) != 1 || resp.Question[0].Name != host {
		t.Fatalf("legacy: %v unicast %v", resp, unicast)
	}
	for _, rr := range resp.Answer {
		if h := rr.Header(); h.Class != dns.ClassINET || h.Ttl > 10 {
			t.Errorf("legacy answer header %+v", h)
		}
	}
	if _, err := resp.Pack(); err != nil {
		t.Fatal(err)
	}
}
