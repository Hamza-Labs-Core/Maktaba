package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

// fakeMinter is the interface-seam stand-in for TokenMinter — mirrors
// the mock convention used by subscriptions/authz tests.
type fakeMinter struct {
	calls   int
	lastUID string
	lastKnd string
	lastLbl string
	err     error
}

func (f *fakeMinter) Mint(_ context.Context, userID, kind, label string) (MintedTokens, error) {
	f.calls++
	f.lastUID, f.lastKnd, f.lastLbl = userID, kind, label
	if f.err != nil {
		return MintedTokens{}, f.err
	}
	return MintedTokens{
		AccessToken:      "access." + userID,
		AccessExpiresIn:  900,
		RefreshToken:     "mkt_rt_v1.row." + userID,
		RefreshExpiresIn: int(PairRefreshTTL.Seconds()),
		UserID:           userID,
	}, nil
}

func newTestHandler(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	h := &Handler{
		Store:  discovery.NewMemoryPairingStore(),
		Minter: &fakeMinter{},
		TTL:    time.Minute,
	}
	r := chi.NewRouter()
	h.Mount(r)
	return h, r
}

func TestRequestRequiresPrincipal(t *testing.T) {
	_, r := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/request", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

// TestExchangeMintsTokens is the regression test for the headline gap:
// Exchange must return real access + refresh tokens, not just
// {user_id, expires_at}.
func TestExchangeMintsTokens(t *testing.T) {
	h, r := newTestHandler(t)
	fm := h.Minter.(*fakeMinter)

	ctx := principal.WithPrincipal(context.Background(), &principal.Principal{UserID: "user-1"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/pairing/request", nil).WithContext(ctx))
	if rec.Code != http.StatusCreated {
		t.Fatalf("request status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got requestResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Code == "" {
		t.Fatal("missing code")
	}

	body := `{"code":"` + got.Code + `","device_kind":"phone","device_label":"Pixel 8"}`
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/api/pairing/exchange",
		strings.NewReader(body)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("exchange status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var ex exchangeResponse
	if err := json.NewDecoder(rec2.Body).Decode(&ex); err != nil {
		t.Fatal(err)
	}
	if ex.AccessToken == "" || ex.RefreshToken == "" {
		t.Fatalf("expected access+refresh tokens, got %+v", ex)
	}
	if ex.RefreshExpiresIn != int(PairRefreshTTL.Seconds()) {
		t.Fatalf("refresh ttl = %d, want %d", ex.RefreshExpiresIn, int(PairRefreshTTL.Seconds()))
	}
	if fm.calls != 1 || fm.lastUID != "user-1" {
		t.Fatalf("minter not invoked correctly: calls=%d uid=%q", fm.calls, fm.lastUID)
	}
	if fm.lastKnd != "phone" || fm.lastLbl != "Pixel 8" {
		t.Fatalf("device meta not forwarded: kind=%q label=%q", fm.lastKnd, fm.lastLbl)
	}

	// One-time: a second exchange of the same code is a 409 and the
	// minter is NOT invoked again.
	rec3 := httptest.NewRecorder()
	r.ServeHTTP(rec3, httptest.NewRequest(http.MethodPost, "/api/pairing/exchange",
		strings.NewReader(body)))
	if rec3.Code != http.StatusConflict {
		t.Fatalf("second exchange status=%d", rec3.Code)
	}
	if fm.calls != 1 {
		t.Fatalf("minter invoked %d times; one-time guarantee broken", fm.calls)
	}
}

// TestExchangeNoMinterIsUnavailable: a handler without a minter must
// fail loudly (503) instead of dead-ending with an unusable body.
func TestExchangeNoMinterIsUnavailable(t *testing.T) {
	h := &Handler{Store: discovery.NewMemoryPairingStore(), TTL: time.Minute}
	r := chi.NewRouter()
	h.Mount(r)
	_ = h.Store.Put(context.Background(), discovery.PairingTicket{
		Code: "ABCD1234", UserID: "u1",
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute),
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/pairing/exchange",
		strings.NewReader(`{"code":"ABCD-1234"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestExchangeMintFailureDoesNotLeakUsableCode: if the minter errors
// AFTER the ticket is consumed, the code stays burned (replay-safe).
func TestExchangeMintFailureBurnsCode(t *testing.T) {
	h, r := newTestHandler(t)
	h.Minter = &fakeMinter{err: errors.New("kms down")}
	_ = h.Store.Put(context.Background(), discovery.PairingTicket{
		Code: "ZZZZ9999", UserID: "u9",
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute),
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/pairing/exchange",
		strings.NewReader(`{"code":"ZZZZ-9999"}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
	// Code is already consumed — a retry is 409, never a second mint.
	h.Minter = &fakeMinter{}
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/api/pairing/exchange",
		strings.NewReader(`{"code":"ZZZZ-9999"}`)))
	if rec2.Code != http.StatusConflict {
		t.Fatalf("retry status=%d, expected 409 (code must stay burned)", rec2.Code)
	}
}

func TestStatusPendingThenPaired(t *testing.T) {
	h, r := newTestHandler(t)
	now := time.Now().UTC()
	h.NowFunc = func() time.Time { return now }
	store := h.Store.(*discovery.MemoryPairingStore)
	store.SetNow(func() time.Time { return now })
	_ = store.Put(context.Background(), discovery.PairingTicket{
		Code:      "ABCD1234",
		UserID:    "u1",
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Minute),
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/pairing/status?code=ABCD-1234", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("pending status=%d", rec.Code)
	}

	if _, err := store.Consume(context.Background(), "ABCD1234"); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/pairing/status?code=ABCD-1234", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("paired status=%d body=%s", rec2.Code, rec2.Body.String())
	}
}
