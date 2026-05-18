package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// Story 16.2 / gap analysis: "Seat enforcement on POST /api/users".
// This is the FIRST real premium-gate call site in the codebase — the
// audit's headline finding was that `.Allows(`/seat-cap checks had
// ZERO callers. These tests pin the gate at the user-create boundary.

// seatStub is a SeatLimiter + SeatCounter test double. limit==
// subscriptions.SeatsUnlimited means "no cap".
type seatStub struct {
	limit int
	count int
	err   error
}

func (s seatStub) SeatLimit() int { return s.limit }
func (s seatStub) CountUsers(_ context.Context) (int, error) {
	return s.count, s.err
}

// At the free-tier cap (1 seat, 1 existing user): creating a 2nd user
// is rejected 403 with the seat-limit problem type, and the store is
// NEVER called.
func TestSeatGate_FreeTierBlocksSecondUser(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{
		UserAdmin: fa,
		Seats:     seatStub{limit: 1, count: 1},
	}
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, adminReq(http.MethodPost, "/api/users",
		`{"username":"bob","password":"pw-correct-horse"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "seat-limit") {
		t.Fatalf("body = %s, want seat-limit problem type", rec.Body.String())
	}
	if fa.created != nil {
		t.Fatal("store must NOT be called when the seat cap is exceeded")
	}
}

// Under the cap (home tier, 4 seats, 2 existing): create succeeds 201.
func TestSeatGate_UnderCapAllows(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{
		UserAdmin: fa,
		Seats:     seatStub{limit: 4, count: 2},
	}
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, adminReq(http.MethodPost, "/api/users",
		`{"username":"carol","password":"pw-correct-horse"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fa.created == nil || fa.created.Username != "carol" {
		t.Fatalf("store should have been called; got %+v", fa.created)
	}
}

// pro tier (unlimited): never blocked regardless of count.
func TestSeatGate_UnlimitedNeverBlocks(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{
		UserAdmin: fa,
		Seats:     seatStub{limit: subscriptions.SeatsUnlimited, count: 9999},
	}
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, adminReq(http.MethodPost, "/api/users",
		`{"username":"dave","password":"pw-correct-horse"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (unlimited); body=%s", rec.Code, rec.Body.String())
	}
}

// No Seats seam wired (nil): the gate is inert — backward-compatible
// with every existing call path that doesn't supply entitlements.
func TestSeatGate_NilSeamIsInert(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{UserAdmin: fa} // Seats nil
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, adminReq(http.MethodPost, "/api/users",
		`{"username":"erin","password":"pw-correct-horse"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// A counter error fails CLOSED: we cannot prove we're under the cap, so
// the create is refused (503) rather than silently over-provisioning a
// paid tier's seats.
func TestSeatGate_CounterErrorFailsClosed(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{
		UserAdmin: fa,
		Seats:     seatStub{limit: 4, err: errCount},
	}
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, adminReq(http.MethodPost, "/api/users",
		`{"username":"frank","password":"pw-correct-horse"}`))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (fail-closed); body=%s", rec.Code, rec.Body.String())
	}
	if fa.created != nil {
		t.Fatal("store must NOT be called when the seat count is unknown")
	}
}

// The subscriptions Store adapter satisfies the SeatLimiter seam, so
// the production wiring is real (not a test-only fiction).
func TestSubscriptionsStoreSatisfiesSeatLimiter(t *testing.T) {
	var _ SeatLimiter = subscriptions.NewSeatLimiter(subscriptions.NewStore())
	var _ SeatCounter = (*users.Store)(nil)
}

var errCount = &countErr{}

type countErr struct{}

func (*countErr) Error() string { return "db: count failed" }
