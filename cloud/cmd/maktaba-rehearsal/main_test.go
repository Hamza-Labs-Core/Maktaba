package main

import "testing"

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
