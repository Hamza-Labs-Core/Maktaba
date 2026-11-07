package homes

import (
	"encoding/json"
	"testing"
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
