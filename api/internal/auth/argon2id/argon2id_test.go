package argon2id

import (
	"strings"
	"testing"
)

// fastParams keeps tests under a millisecond per hash. The behavioural
// surface (PHC encoding, salt randomness, constant-time verify) is the
// same; the cost parameters only change wall-clock time.
func fastParams() Params {
	p := DefaultParams()
	p.Memory = 64
	p.Time = 1
	return p
}

func TestHash_AC1_FormatAndParameters(t *testing.T) {
	p := DefaultParams()
	p.Memory = 64
	h, err := Hash("hunter2", p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=") {
		t.Errorf("hash should start with PHC prefix, got %q", h)
	}
	if !strings.Contains(h, "m=64,t=2,p=1") {
		t.Errorf("PHC string should embed parameters, got %q", h)
	}
}

func TestVerify_AC2_ConstantTimeMatch(t *testing.T) {
	p := fastParams()
	h, err := Hash("hunter2", p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if err := Verify("hunter2", h); err != nil {
		t.Errorf("Verify(matching) = %v, want nil", err)
	}
	if err := Verify("hunter3", h); err != ErrMismatch {
		t.Errorf("Verify(wrong) = %v, want ErrMismatch", err)
	}
}

func TestHash_RandomSalt(t *testing.T) {
	p := fastParams()
	a, err := Hash("same", p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Hash("same", p)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("two hashes of the same password should differ; got %q == %q", a, b)
	}
	if err := Verify("same", a); err != nil {
		t.Errorf("a verify: %v", err)
	}
	if err := Verify("same", b); err != nil {
		t.Errorf("b verify: %v", err)
	}
}

func TestHash_RejectsOversize(t *testing.T) {
	p := fastParams()
	long := strings.Repeat("x", p.MaxLen+1)
	_, err := Hash(long, p)
	if err != ErrPasswordTooLong {
		t.Errorf("oversized password: got %v, want ErrPasswordTooLong", err)
	}
}

func TestHash_ParametersFromConfigEndUpInPHC(t *testing.T) {
	p := DefaultParams()
	p.Memory = 1024 // unusual value to detect threading
	p.Time = 3
	p.Parallelism = 2
	h, err := Hash("x", p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.Contains(h, "m=1024,t=3,p=2") {
		t.Errorf("expected m=1024,t=3,p=2 in PHC, got %q", h)
	}
	// And verify still works against the embedded parameters.
	if err := Verify("x", h); err != nil {
		t.Errorf("Verify with non-default params: %v", err)
	}
}

func TestVerify_BadFormat(t *testing.T) {
	cases := []string{
		"",
		"not-a-phc",
		"$argon2i$v=19$m=64,t=1,p=1$YQ$Yg",          // wrong variant
		"$argon2id$v=19$only-three-fields",          // missing parts
		"$argon2id$v=19$m=64,t=1,p=1$not-base64!$Y", // malformed b64
	}
	for _, in := range cases {
		if err := Verify("anything", in); err != ErrInvalidHash {
			t.Errorf("Verify(%q) = %v, want ErrInvalidHash", in, err)
		}
	}
}

func TestIsDisabled(t *testing.T) {
	if !IsDisabled("<unsalted-disabled>") {
		t.Error("sentinel placeholder should be disabled")
	}
	if !IsDisabled("anything-without-prefix") {
		t.Error("non-PHC string should be treated as disabled")
	}
	h, _ := Hash("x", fastParams())
	if IsDisabled(h) {
		t.Errorf("real PHC string should not be disabled, got %q", h)
	}
}
