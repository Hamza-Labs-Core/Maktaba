package direct

import (
	"net/netip"
	"testing"
)

// T01: the name codec round-trips IPv4 and IPv6; bad labels are refused.
func TestTheNameCodec(t *testing.T) {
	for _, s := range []string{"81.2.3.4", "192.168.1.20", "2001:db8::1", "2a01:4f8:c17:1::", "::1", "fe80::1"} {
		ip := netip.MustParseAddr(s)
		got, err := DecodeLabel(EncodeIP(ip))
		if err != nil || got != ip {
			t.Errorf("%s -> %q -> %v %v", s, EncodeIP(ip), got, err)
		}
	}
	if EncodeIP(netip.MustParseAddr("81.2.3.4")) != "81-2-3-4" || EncodeIP(netip.MustParseAddr("2001:db8::1")) != "2001-db8--1" {
		t.Error("the spec's examples")
	}
	if EncodeIP(netip.MustParseAddr("::ffff:81.2.3.4")) != "81-2-3-4" {
		t.Error("a mapped IPv4 is the IPv4")
	}
	for _, bad := range []string{"", "81-2-3", "81-2-3-4-5", "256-1-1-1", "081-2-3-4", "a.b", "81_2_3_4", "zz--1", "2001-0db8--1", "x"} {
		if ip, err := DecodeLabel(bad); err == nil {
			t.Errorf("%q decoded to %v", bad, ip)
		}
	}
}

// T03: the same server_id gives the same home ID; another server another.
func TestTheHomeID(t *testing.T) {
	a, b := HomeID("0a1b2c3d-0000-4000-8000-000000000000"), HomeID("0a1b2c3d-0000-4000-8000-000000000001")
	if a != HomeID("0a1b2c3d-0000-4000-8000-000000000000") || a == b || len(a) != 32 || !IsHomeID(a) {
		t.Fatalf("%s %s", a, b)
	}
}

func TestParsingNames(t *testing.T) {
	domains := []string{"direct-a.example", "direct-b.example"}
	id := HomeID("s1")
	p, in, err := Parse("81-2-3-4."+id+".direct-b.example.", domains)
	if !in || err != nil || p.Addr != netip.MustParseAddr("81.2.3.4") || p.HomeID != id || p.Domain != "direct-b.example" {
		t.Fatalf("%+v %v %v", p, in, err)
	}
	if p, in, err := Parse("_acme-challenge."+id+".direct-a.example", domains); !in || err != nil || !p.ACME {
		t.Fatalf("acme: %+v %v", p, err)
	}
	if p, _, _ := Parse("NS2.Direct-A.Example", domains); p.NS != 2 {
		t.Fatalf("ns2: %+v", p)
	}
	if _, in, _ := Parse("81-2-3-4."+id+".relay.maktaba.tv", domains); in {
		t.Fatal("a name under maktaba.tv is in no direct zone")
	}
	if _, in, err := Parse("www.direct-a.example", domains); !in || err == nil {
		t.Fatal("a name in the zone that isn't ours: in the zone, no such name")
	}
}

// The apps derive a home's id from its server id to check a local_ip pin names that home
// (PinnedNames on Android and Apple): this value is in their tests too, so a change here must
// change theirs.
func TestHomeIDIsWhatTheAppsDerive(t *testing.T) {
	if got := HomeID("s"); got != "18c78ba238ff428e2ddbdff20477e7eb" {
		t.Fatalf("HomeID(%q) = %s", "s", got)
	}
}
