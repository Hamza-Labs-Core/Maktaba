package argon2id

import (
	"strings"
	"testing"
)

func fastParams() Params {
	p := DefaultParams()
	p.Memory = 64
	p.Time = 1
	return p
}

func TestHash_PHCFormat(t *testing.T) {
	h, err := Hash("hunter2", fastParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=") {
		t.Errorf("expected PHC prefix, got %q", h)
	}
}

func TestVerify_RoundTrip(t *testing.T) {
	h, _ := Hash("hunter2", fastParams())
	if err := Verify("hunter2", h); err != nil {
		t.Errorf("Verify good password: %v", err)
	}
	if err := Verify("nope", h); err != ErrMismatch {
		t.Errorf("Verify wrong password: %v, want %v", err, ErrMismatch)
	}
}

func TestHash_RejectsLong(t *testing.T) {
	p := fastParams()
	p.MaxLen = 8
	if _, err := Hash("a long password", p); err != ErrTooLong {
		t.Errorf("Hash long: %v, want %v", err, ErrTooLong)
	}
}

func TestVerify_BadFormat(t *testing.T) {
	if err := Verify("x", "not-a-phc-string"); err != ErrBadFormat {
		t.Errorf("Verify bad: %v, want %v", err, ErrBadFormat)
	}
}
