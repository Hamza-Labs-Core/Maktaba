package router

import (
	"testing"

	subpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// A build that cannot verify a license has no seat cap: the free tier's single seat
// would hold its home at one user for good. One that can enforces its entitlement's.
func TestSeatGateNeedsALicenseSource(t *testing.T) {
	store := subpkg.NewStore()
	if g := SeatGate(store, nil); g != nil {
		t.Fatalf("no verifier: gate = %v, want none", g)
	}
	g := SeatGate(store, &subpkg.Verifier{})
	if g == nil {
		t.Fatal("verifier: no gate")
	}
	if got := g.SeatLimit(); got != 1 {
		t.Fatalf("free tier with a verifier: limit = %d, want 1", got)
	}
}
