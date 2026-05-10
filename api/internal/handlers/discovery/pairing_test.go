package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

func newTestHandler(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	h := &Handler{Store: discovery.NewMemoryPairingStore(), TTL: time.Minute}
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

func TestRequestAndExchange(t *testing.T) {
	_, r := newTestHandler(t)

	ctx := principal.WithPrincipal(context.Background(), &principal.Principal{UserID: "user-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/request", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("request status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp requestResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code == "" {
		t.Fatal("missing code")
	}

	exchangeBody := `{"code":"` + resp.Code + `"}`
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/api/pairing/exchange",
		strings.NewReader(exchangeBody)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("exchange status=%d body=%s", rec2.Code, rec2.Body.String())
	}

	rec3 := httptest.NewRecorder()
	r.ServeHTTP(rec3, httptest.NewRequest(http.MethodPost, "/api/pairing/exchange",
		strings.NewReader(exchangeBody)))
	if rec3.Code != http.StatusConflict {
		t.Fatalf("second exchange status=%d", rec3.Code)
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
