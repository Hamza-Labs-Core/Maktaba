package middleware

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8::1]:443"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7")
	r.Header.Set("CF-Connecting-IP", "198.51.100.9")
	if got := ClientIP(r, false); got != "2001:db8::1" {
		t.Errorf("untrusted: %q", got)
	}
	if got := ClientIP(r, true); got != "198.51.100.9" {
		t.Errorf("trusted CF: %q", got)
	}
	r.Header.Del("CF-Connecting-IP")
	if got := ClientIP(r, true); got != "198.51.100.7" {
		t.Errorf("trusted XFF: %q", got)
	}
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := ClientIP(r, true); got != "2001:db8::1" {
		t.Errorf("garbage XFF: %q", got)
	}
}
