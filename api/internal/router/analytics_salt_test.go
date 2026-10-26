package router

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/watch"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// The viewers' addresses are hashed with the home's own secret salt, made once and
// kept; with no store there is no salt, and no address is hashed at all (an unsalted
// hash of an address is no secret).
func TestAnalyticsSaltIsTheHomesAndKept(t *testing.T) {
	ctx := context.Background()
	store := secrets.NewMemory()
	a := AnalyticsSalt(ctx, store, nil)
	if len(a) != 64 {
		t.Fatalf("salt %q", a)
	}
	if b := AnalyticsSalt(ctx, store, nil); b != a {
		t.Fatal("a second start made another salt")
	}
	if c := AnalyticsSalt(ctx, secrets.NewMemory(), nil); c == a {
		t.Fatal("two homes share a salt")
	}
	if AnalyticsSalt(ctx, nil, nil) != "" {
		t.Fatal("no store, yet a salt")
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.20:5000"
	if watch.HashIP("", r) != "" {
		t.Fatal("an address hashed without a salt")
	}
	if h := watch.HashIP(a, r); h == "" || h == watch.HashIP(AnalyticsSalt(ctx, secrets.NewMemory(), nil), r) {
		t.Fatalf("hash %q", h)
	}
}
