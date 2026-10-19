package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripVia(t *testing.T) {
	cases := []struct {
		remote string
		keep   bool
	}{
		{"127.0.0.1:5555", true},
		{"[::1]:5555", true},
		{"192.168.1.20:5555", false},
		{"8.8.8.8:443", false},
		{"garbage", false},
	}
	for _, c := range cases {
		var got string
		h := StripVia(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Get(ViaHeader) }))
		req := httptest.NewRequest("GET", "/api/tv/info", nil)
		req.RemoteAddr = c.remote
		req.Header.Set(ViaHeader, "relay")
		h.ServeHTTP(httptest.NewRecorder(), req)
		if (got == "relay") != c.keep {
			t.Errorf("%s: header %q, keep %v", c.remote, got, c.keep)
		}
	}
}
