package dltoken

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A token says its machine, release and expiry, and holds only with the key that signed
// it, before it expires.
func TestMintVerify(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	tok := Mint(key, "node-1", "1.5.0", now.Add(Lifetime))
	if !strings.HasPrefix(tok, Prefix) {
		t.Fatalf("token %q", tok)
	}
	c, err := Verify(key, tok, now)
	if err != nil || c.Node != "node-1" || c.Version != "1.5.0" || c.Expires != now.Add(Lifetime).Unix() {
		t.Fatalf("verify: %+v %v", c, err)
	}
	if _, err := Verify([]byte("another key, another key, another"), tok, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("another key: %v", err)
	}
	if _, err := Verify(key, tok, now.Add(Lifetime)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	// the claims changed (another release), the signature kept
	other := Mint(key, "node-1", "1.6.0", now.Add(Lifetime))
	body, _, _ := strings.Cut(strings.TrimPrefix(other, Prefix), ".")
	_, mac, _ := strings.Cut(strings.TrimPrefix(tok, Prefix), ".")
	if _, err := Verify(key, Prefix+body+"."+mac, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("changed claims: %v", err)
	}
	for _, bad := range []string{"", "mkt_pat_x", Prefix, Prefix + "abc", Prefix + "a.b", tok[:len(tok)-3] + "!!!"} {
		if _, err := Verify(key, bad, now); err == nil {
			t.Errorf("%q verified", bad)
		}
	}
	if _, err := Verify(nil, tok, now); !errors.Is(err, ErrMalformed) {
		t.Fatalf("no key: %v", err)
	}
}

// Only a bearer token with the prefix is one.
func TestFromRequest(t *testing.T) {
	for _, c := range []struct{ header, want string }{
		{"Bearer " + Prefix + "abc.def", Prefix + "abc.def"},
		{"Bearer mkt_pat_abc", ""},
		{Prefix + "abc.def", ""},
		{"", ""},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if c.header != "" {
			r.Header.Set("Authorization", c.header)
		}
		if got := FromRequest(r); got != c.want {
			t.Errorf("%q: %q, want %q", c.header, got, c.want)
		}
	}
}
