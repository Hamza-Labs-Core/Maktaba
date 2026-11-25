package ratelimit

import (
	"net/http/httptest"
	"testing"
)

func TestNetKeyIsTheNetwork(t *testing.T) {
	key := func(peer string) string {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = peer
		return NetKey(r)
	}
	for _, same := range [][2]string{{"203.0.113.7:1", "203.0.113.250:2"}, {"[2001:db8:0:1200::1]:1", "[2001:db8:0:12ff:1::]:1"},
		{"[::ffff:203.0.113.7]:1", "203.0.113.9:1"}} {
		if key(same[0]) != key(same[1]) {
			t.Errorf("%s and %s: %s, %s", same[0], same[1], key(same[0]), key(same[1]))
		}
	}
	for _, other := range [][2]string{{"203.0.113.7:1", "203.0.114.7:1"}, {"[2001:db8:0:1200::1]:1", "[2001:db8:0:1300::1]:1"}} {
		if key(other[0]) == key(other[1]) {
			t.Errorf("%s and %s are one network", other[0], other[1])
		}
	}
	if k := key("not an address"); k == "" {
		t.Error("an unreadable address has no limit")
	}
}
