package mdns

import "testing"

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
