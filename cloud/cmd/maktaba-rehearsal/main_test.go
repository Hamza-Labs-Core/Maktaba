package main

import (
	"testing"
	"time"
)

func TestParseServers(t *testing.T) {
	names, ips, err := parseServers("1=node-a@10.50.0.11, 2=node-b")
	if err != nil || names[1] != "node-a" || ips[1] != "10.50.0.11" || names[2] != "node-b" || ips[2] != "" {
		t.Fatalf("%v %v %v", names, ips, err)
	}
	if _, _, err := parseServers("x=node"); err == nil {
		t.Error("a bad id parsed")
	}
}

func TestNewFakeACMEDNS(t *testing.T) {
	t.Setenv("FAKEACMEDNS_USER", "u")
	t.Setenv("FAKEACMEDNS_KEY", "k")
	s := newFakeACMEDNS()
	if s.Zone != "relay.test" || s.User != "u" || s.Key != "k" {
		t.Errorf("%+v", s)
	}
}

func TestFipfrontAddrs(t *testing.T) {
	if l, tg := fipfrontAddrs(); l != ":80" || tg != "relay.test:80" {
		t.Errorf("defaults %s %s", l, tg)
	}
	t.Setenv("FIPFRONT_TARGET", "x:1")
	if _, tg := fipfrontAddrs(); tg != "x:1" {
		t.Errorf("target %s", tg)
	}
}

// story 50.18: db.internal's front follows the database's Floating IP; unset, it never looks again
func TestFipfrontFollow(t *testing.T) {
	if d := fipfrontFollow(); d != 0 {
		t.Errorf("unset: %s", d)
	}
	t.Setenv("FIPFRONT_FOLLOW", "500ms")
	if d := fipfrontFollow(); d != 500*time.Millisecond {
		t.Errorf("500ms: %s", d)
	}
}

func TestFakeGatewayConfig(t *testing.T) {
	if _, _, _, err := fakeGatewayConfig(); err == nil {
		t.Error("no FAKEGW_WAN passed")
	}
	t.Setenv("FAKEGW_WAN", "198.51.100.2")
	t.Setenv("FAKEGW_LAN", "192.168.77.20")
	if _, _, _, err := fakeGatewayConfig(); err == nil {
		t.Error("a bare address passed as FAKEGW_LAN")
	}
	t.Setenv("FAKEGW_LAN", "192.168.77.0/24")
	wan, lan, listen, err := fakeGatewayConfig()
	if err != nil || wan.String() != "198.51.100.2" || lan.String() != "192.168.77.0/24" || listen != ":5351" {
		t.Errorf("%v %v %q %v", wan, lan, listen, err)
	}
}
