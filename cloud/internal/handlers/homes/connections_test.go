package homes

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// T07 and T12 (story 50.13): the connections array, in the order apps try, beside the old
// fields; a plan without the relay lists no relay.
func TestConnectionsInTheOrderAppsTry(t *testing.T) {
	got := connectionsFor("http://81.2.3.4:32400", "https://skylark.relay.maktaba.tv", true)
	want := []connection{{URL: "http://81.2.3.4:32400", Kind: "direct"}, {URL: "https://skylark.relay.maktaba.tv", Kind: "relay"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("connections %v, want %v", got, want)
	}
	if got := connectionsFor("", "https://skylark.relay.maktaba.tv", true); len(got) != 1 || got[0].Kind != "relay" {
		t.Fatalf("no direct address: %v", got)
	}
}

func TestAPlanWithoutTheRelayListsNoRelay(t *testing.T) {
	got := connectionsFor("http://81.2.3.4:32400", "https://skylark.relay.maktaba.tv", false)
	if len(got) != 1 || got[0].Kind != "direct" {
		t.Fatalf("connections %v, want the direct one only", got)
	}
	if got := connectionsFor("", "https://skylark.relay.maktaba.tv", false); got == nil || len(got) != 0 {
		t.Fatalf("nothing to reach: %v, want an empty list (never null)", got)
	}
}

func TestTheHomeViewKeepsTheOldFields(t *testing.T) {
	v := homeView{ServerID: "s", Slug: "skylark", RelayURL: "https://skylark.relay.maktaba.tv", DirectURL: "http://81.2.3.4:32400",
		Connections: connectionsFor("http://81.2.3.4:32400", "https://skylark.relay.maktaba.tv", true)}
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"relay_url", "direct_url", "connections"} {
		if _, ok := m[k]; !ok {
			t.Errorf("%s missing from %s", k, b)
		}
	}
}

// T08 (story 50.15): a home whose outside check passed lists its HTTPS name first; the plain
// address stays for older apps, then the relay.
func TestASecureDirectNameComesFirst(t *testing.T) {
	got := connectionsFor("http://81.2.3.4:32400", "https://skylark.relay.maktaba.tv", true,
		"https://81-2-3-4.0123456789abcdef0123456789abcdef.direct-a.example:32443")
	if len(got) != 3 || got[0].Kind != "direct" || got[0].URL[:8] != "https://" || got[1].URL != "http://81.2.3.4:32400" || got[2].Kind != "relay" {
		t.Fatalf("%+v", got)
	}
	// a Free home (no relay) still gets its direct name
	if got := connectionsFor("", "https://skylark.relay.maktaba.tv", false, "https://x.direct-a.example:32443"); len(got) != 1 || got[0].Kind != "direct" {
		t.Fatalf("free: %+v", got)
	}
}

// Story 50.15 §3 (DNS rebinding): a home's LAN addresses give its LAN names first, then each
// raw address pinned (tls_name) to that name; IPv6 bracketed; old apps drop local_ip.
func TestLANEntriesNamesThenPinnedAddresses(t *testing.T) {
	const hid = "0123456789abcdef0123456789abcdef"
	got := localConnections(stores.LANHome{HomeID: hid, Domain: "direct-a.example",
		LAN: []netip.AddrPort{netip.MustParseAddrPort("192.168.1.20:8444"), netip.MustParseAddrPort("[fd00::7]:8444")}})
	want := []connection{
		{URL: "https://192-168-1-20." + hid + ".direct-a.example:8444", Kind: "local"},
		{URL: "https://fd00--7." + hid + ".direct-a.example:8444", Kind: "local"},
		{URL: "https://192.168.1.20:8444", Kind: "local_ip", TLSName: "192-168-1-20." + hid + ".direct-a.example"},
		{URL: "https://[fd00::7]:8444", Kind: "local_ip", TLSName: "fd00--7." + hid + ".direct-a.example"},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
	b, _ := json.Marshal(got[0])
	if string(b) != `{"url":"https://192-168-1-20.`+hid+`.direct-a.example:8444","kind":"local"}` {
		t.Errorf("a local entry has no tls_name: %s", b)
	}
}
