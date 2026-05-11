package serverkeys

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	now atomic.Int64 // unix nanos
}

func newFakeClock(t time.Time) *fakeClock {
	c := &fakeClock{}
	c.now.Store(t.UnixNano())
	return c
}

func (c *fakeClock) Now() time.Time { return time.Unix(0, c.now.Load()) }

func (c *fakeClock) advance(d time.Duration) {
	c.now.Add(int64(d))
}

func TestDeriveKidStable(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	k, err := Generate(now, "test")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if k.Kid != DeriveKid(k.PublicKey) {
		t.Fatalf("kid mismatch between Generate and DeriveKid")
	}
	if len(k.Kid) != 16 {
		t.Fatalf("kid length = %d, want 16", len(k.Kid))
	}
}

func TestSignVerifyRoundtrip(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	k, _ := Generate(now, "test")
	store, err := NewStore(k, 0, newFakeClock(now))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	payload := []byte("hello, federation")
	sig, kid := store.Sign(payload)
	if kid != k.Kid {
		t.Fatalf("Sign returned kid=%s, want %s", kid, k.Kid)
	}
	if err := store.Verify(payload, sig, kid); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyUnknownKidVsBadSig(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	k, _ := Generate(now, "test")
	store, _ := NewStore(k, 0, newFakeClock(now))
	sig, kid := store.Sign([]byte("a"))

	if err := store.Verify([]byte("a"), sig, "deadbeefdeadbeef"); !errors.Is(err, ErrUnknownKid) {
		t.Fatalf("unknown kid: got %v, want ErrUnknownKid", err)
	}
	if err := store.Verify([]byte("tampered"), sig, kid); !errors.Is(err, ErrBadSig) {
		t.Fatalf("tampered payload: got %v, want ErrBadSig", err)
	}
}

func TestRotateOverlap(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	clk := newFakeClock(start)
	k, _ := Generate(start, "test")
	store, _ := NewStore(k, time.Hour, clk)

	sigOld, kidOld := store.Sign([]byte("msg"))

	res, err := store.Rotate(RotateOptions{Reason: "test"})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if res.OldKid != kidOld {
		t.Fatalf("OldKid = %s, want %s", res.OldKid, kidOld)
	}
	if res.NewKid == kidOld {
		t.Fatalf("NewKid should differ from old")
	}
	if res.OverlapSeconds != 3600 {
		t.Fatalf("OverlapSeconds = %d, want 3600", res.OverlapSeconds)
	}

	// Inside overlap: old kid still verifies.
	if err := store.Verify([]byte("msg"), sigOld, kidOld); err != nil {
		t.Fatalf("verify during overlap: %v", err)
	}

	// After overlap: old kid is forgotten.
	clk.advance(time.Hour + time.Second)
	if err := store.Verify([]byte("msg"), sigOld, kidOld); !errors.Is(err, ErrUnknownKid) {
		t.Fatalf("verify after overlap: got %v, want ErrUnknownKid", err)
	}
}

func TestRotateImmediate(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	clk := newFakeClock(now)
	k, _ := Generate(now, "test")
	store, _ := NewStore(k, time.Hour, clk)

	sigOld, kidOld := store.Sign([]byte("msg"))
	if _, err := store.Rotate(RotateOptions{Immediate: true}); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := store.Verify([]byte("msg"), sigOld, kidOld); !errors.Is(err, ErrUnknownKid) {
		t.Fatalf("immediate rotate should evict predecessor; got %v", err)
	}
}

func TestNewStoreRequiresActive(t *testing.T) {
	if _, err := NewStore(nil, 0, nil); err == nil {
		t.Fatal("expected error when active key is nil")
	}
	k := &Key{Kid: "x"}
	if _, err := NewStore(k, 0, nil); err == nil {
		t.Fatal("expected error when private key bytes are missing")
	}
}

func TestJWKSShape(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	clk := newFakeClock(start)
	k, _ := Generate(start, "test")
	store, _ := NewStore(k, time.Hour, clk)

	pre := store.JWKS()
	if pre.Active.Kid != k.Kid {
		t.Fatalf("Active.Kid = %s, want %s", pre.Active.Kid, k.Kid)
	}
	if pre.Active.Alg != "EdDSA" {
		t.Fatalf("Alg = %s, want EdDSA", pre.Active.Alg)
	}
	if len(pre.Overlap) != 0 {
		t.Fatalf("Overlap should be empty before rotation, got %d", len(pre.Overlap))
	}

	_, _ = store.Rotate(RotateOptions{})
	post := store.JWKS()
	if len(post.Overlap) != 1 {
		t.Fatalf("Overlap = %d, want 1", len(post.Overlap))
	}
	if post.Overlap[0].Kid != k.Kid {
		t.Fatalf("Overlap[0].Kid = %s, want %s", post.Overlap[0].Kid, k.Kid)
	}
	if post.Overlap[0].RetiresAt == "" {
		t.Fatal("Overlap entry should carry RetiresAt")
	}
}
