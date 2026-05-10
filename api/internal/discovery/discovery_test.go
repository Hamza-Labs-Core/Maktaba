package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGenerateCodeShape(t *testing.T) {
	for i := 0; i < 16; i++ {
		c, err := GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		if len(c) != 9 || c[4] != '-' {
			t.Fatalf("unexpected shape %q", c)
		}
	}
}

func TestNormalizeCode(t *testing.T) {
	got := NormalizeCode("abcd-1234")
	if got != "ABCD1234" {
		t.Fatalf("normalize: got %q", got)
	}
	if NormalizeCode(" a b c d 1 2 3 4 ") != "ABCD1234" {
		t.Fatal("whitespace handling broken")
	}
}

func TestMemoryPairingStoreConsumeFlow(t *testing.T) {
	store := NewMemoryPairingStore()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	ticket := PairingTicket{
		Code:      "ABCD-1234",
		UserID:    "u1",
		IssuedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
	}
	if err := store.Put(context.Background(), ticket); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), "ABCD-1234")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != "u1" {
		t.Fatalf("user mismatch %q", got.UserID)
	}

	if _, err := store.Consume(context.Background(), "ABCD-1234"); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if _, err := store.Consume(context.Background(), "ABCD-1234"); !errors.Is(err, ErrCodeConsumed) {
		t.Fatalf("expected ErrCodeConsumed, got %v", err)
	}
}

func TestMemoryPairingStoreExpiry(t *testing.T) {
	store := NewMemoryPairingStore()
	current := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return current }

	_ = store.Put(context.Background(), PairingTicket{
		Code:      "EXP",
		IssuedAt:  current,
		ExpiresAt: current.Add(time.Minute),
	})
	current = current.Add(2 * time.Minute)
	if _, err := store.Get(context.Background(), "EXP"); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("expected expired, got %v", err)
	}
}

func TestNoopPublisherCapturesService(t *testing.T) {
	p := &NoopPublisher{}
	svc := Service{
		Instance: "Maktaba @ test",
		Domain:   "local.",
		Port:     8080,
		TXT:      map[string]string{"version": "1"},
	}
	if err := p.Publish(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	last := p.Last()
	if last == nil || !strings.Contains(last.Instance, "Maktaba") {
		t.Fatalf("missing publish: %+v", last)
	}
}
