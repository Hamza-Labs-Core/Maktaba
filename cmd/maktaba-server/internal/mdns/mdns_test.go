package mdns

import (
	"strings"
	"testing"

	"github.com/grandcat/zeroconf"
)

func TestPortFromListen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int
	}{
		{"0.0.0.0:8080", 8080},
		{":8081", 8081},
		{"127.0.0.1:9100", 9100},
		{"garbage", 7777},
		{"", 7777},
		{"host:0", 7777},
	}
	for _, c := range cases {
		if got := PortFromListen(c.in, 7777); got != c.want {
			t.Errorf("PortFromListen(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TC1 of story 35.11: a node's TXT has role=node, home=<8 hex>, web, no join.
func TestAdvertiseTXTNodeMode(t *testing.T) {
	m := Machine{Role: "node", ID: "6F1C2A3B-4d5e-4f60-8a71-92b3c4d5e6f7", HomeID: "0a1b2c3d-0000-4000-8000-000000000000",
		Name: "studio-linux", WebPort: 8088, Version: "1.5.0"}
	txt := map[string]string{}
	for _, kv := range m.TXT() {
		k, v, _ := strings.Cut(kv, "=")
		txt[k] = v
	}
	if txt["role"] != "node" || txt["id"] != "6f1c2a3b" || txt["home"] != "0a1b2c3d" || txt["web"] != "8088" ||
		txt["protocol"] != "1" || txt["version"] != "1.5.0" || txt["name"] != "studio-linux" {
		t.Fatalf("txt = %v", txt)
	}
	if _, ok := txt["join"]; ok {
		t.Fatal("a node never offers joins")
	}
	if _, ok := txt["schema_rev"]; ok {
		t.Fatal("schema_rev is the home's")
	}
}

// TC2 of story 35.11: the home publishes once, with schema_rev and join.
func TestAdvertiseHomeSinglePublisher(t *testing.T) {
	calls := 0
	p := &Publisher{register: func(_, _, _ string, _ int, _ []string) (*zeroconf.Server, error) {
		calls++
		return nil, nil
	}}
	home := Machine{Role: "home", ID: "0a1b2c3d-0000-4000-8000-000000000000", HomeID: "0a1b2c3d-0000-4000-8000-000000000000",
		Name: "mac", WebPort: 8088, Version: "1.5.0", SchemaRev: 134, Join: true}
	if _, err := p.Publish(home, 8080); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(home.TXT(), ",")
	if !strings.Contains(joined, "schema_rev=134") || !strings.Contains(joined, "join=1") || !strings.Contains(joined, "role=home") {
		t.Fatalf("home txt = %s", joined)
	}
	if calls != 1 {
		t.Fatalf("registered %d times", calls)
	}
}

// TC3 of story 35.11: a new version re-publishes; the same machine does not.
func TestAdvertiseRepublishOnVersionChange(t *testing.T) {
	calls := 0
	p := &Publisher{register: func(_, _, _ string, _ int, _ []string) (*zeroconf.Server, error) {
		calls++
		return nil, nil
	}}
	m := Machine{Role: "node", ID: "6f1c2a3b", HomeID: "0a1b2c3d", Name: "n", WebPort: 8088, Version: "1.5.0"}
	if changed, _ := p.Publish(m, 8080); !changed {
		t.Fatal("first publication")
	}
	if changed, _ := p.Publish(m, 8080); changed {
		t.Fatal("nothing changed")
	}
	m.Version = "1.6.0"
	if changed, _ := p.Publish(m, 8080); !changed || calls != 1 {
		t.Fatalf("a new version re-publishes the TXT in place (calls=%d)", calls)
	}
	m.WebPort = 8090
	if changed, _ := p.Publish(m, 8081); !changed || calls != 2 {
		t.Fatalf("a new port re-registers (calls=%d)", calls)
	}
}
