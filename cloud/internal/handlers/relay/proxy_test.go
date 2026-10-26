package relay

import (
	"net/http"
	"testing"
)

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

func TestCloudCookiesStayOffHomes(t *testing.T) {
	h := http.Header{}
	h.Add("Cookie", "maktaba_refresh=secret; mkt_sess=home-session")
	h.Add("Cookie", "MAKTABA_other=x; oauth_state=s; mkt_csrf=c")
	stripCloudCookies(h)
	if got := h.Get("Cookie"); got != "mkt_sess=home-session; mkt_csrf=c" || len(h.Values("Cookie")) != 1 {
		t.Fatalf("Cookie = %q", h.Values("Cookie"))
	}
	only := http.Header{"Cookie": {"maktaba_refresh=secret"}}
	stripCloudCookies(only)
	if _, ok := only["Cookie"]; ok {
		t.Fatalf("Cookie kept: %q", only.Values("Cookie"))
	}
	for v, ok := range map[string]bool{
		"mkt_sess=a; Path=/; HttpOnly; SameSite=Lax":    true,
		"mkt_sess=a; Path=/; Domain=.relay.maktaba.app": false,
		"x=1; domain=maktaba.app":                       false,
		"maktaba_refresh=evil; Path=/":                  false,
		"oauth_pkce=evil; Path=/":                       false,
	} {
		if hostOnlyCookie(v) != ok {
			t.Errorf("hostOnlyCookie(%q) = %v", v, !ok)
		}
	}
}
