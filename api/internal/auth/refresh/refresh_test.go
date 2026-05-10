package refresh

import (
	"errors"
	"strings"
	"testing"
)

func TestParseToken_OK(t *testing.T) {
	id, secret, err := parseToken("mkt_rt_v1.abc-123.s3cr3tABC")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if id != "abc-123" || secret != "s3cr3tABC" {
		t.Fatalf("parsed: id=%q secret=%q", id, secret)
	}
}

func TestParseToken_Malformed(t *testing.T) {
	cases := []string{
		"",
		"abc",
		"mkt_rt_v0.id.secret",
		"mkt_rt_v1.no-secret",
		"mkt_rt_v1..secret",
		"mkt_rt_v1.id.",
	}
	for _, c := range cases {
		if _, _, err := parseToken(c); !errors.Is(err, ErrMalformed) {
			t.Errorf("parseToken(%q) = %v, want ErrMalformed", c, err)
		}
	}
}

func TestParseToken_RoundTrip(t *testing.T) {
	full := TokenPrefix + "row-uuid" + "." + "secretstring"
	id, sec, err := parseToken(full)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if id != "row-uuid" || sec != "secretstring" {
		t.Fatalf("got id=%q secret=%q", id, sec)
	}
	if !strings.HasPrefix(full, TokenPrefix) {
		t.Fatalf("TokenPrefix not on full token")
	}
}

func TestRandomBytes_LengthAndUniqueness(t *testing.T) {
	a, err := randomBytes(32)
	if err != nil || len(a) != 32 {
		t.Fatalf("a: %v len=%d", err, len(a))
	}
	b, err := randomBytes(32)
	if err != nil || len(b) != 32 {
		t.Fatalf("b: %v len=%d", err, len(b))
	}
	// Pr(collide) ≈ 2^-256; if this ever fires there's a bigger problem.
	if string(a) == string(b) {
		t.Fatalf("randomBytes collided")
	}
}

func TestDerefStr(t *testing.T) {
	if got := derefStr(nil); got != "" {
		t.Errorf("nil: got %q", got)
	}
	s := "x"
	if got := derefStr(&s); got != "x" {
		t.Errorf("ptr: got %q", got)
	}
}
