package relay

import "testing"

func TestSlugFromHost(t *testing.T) {
	cases := []struct {
		host, public, want string
	}{
		{"abc.relay.maktaba.app", "relay.maktaba.app", "abc"},
		{"abc.relay.maktaba.app:443", "relay.maktaba.app", "abc"},
		{"relay.maktaba.app", "relay.maktaba.app", ""},
		{"abc.example.com", "relay.maktaba.app", ""},
		{"abc.def.relay.maktaba.app", "relay.maktaba.app", "abc.def"},
	}
	for _, c := range cases {
		got := slugFromHost(c.host, c.public)
		if got != c.want {
			t.Errorf("slugFromHost(%q,%q) = %q, want %q", c.host, c.public, got, c.want)
		}
	}
}
