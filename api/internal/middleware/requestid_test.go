package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
)

func TestMintsWhenAbsent(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)

	var seen uuid.UUID
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = reqid.FromContext(r.Context())
	}))
	h.ServeHTTP(w, r)

	if seen == uuid.Nil {
		t.Fatal("middleware did not mint an id")
	}
	if seen.Version() != 7 {
		t.Fatalf("minted UUID is v%d, want v7", seen.Version())
	}
	if got := w.Header().Get(reqid.Header); got != seen.String() {
		t.Fatalf("response header = %q, want %q", got, seen.String())
	}
}

func TestReusesValidV7(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set(reqid.Header, id.String())

	var seen uuid.UUID
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = reqid.FromContext(r.Context())
	}))
	h.ServeHTTP(w, r)

	if seen != id {
		t.Fatalf("seen %v, want %v (must reuse client-supplied v7)", seen, id)
	}
}

func TestRejectsMalformedID(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set(reqid.Header, "abcdefg")

	var seen uuid.UUID
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = reqid.FromContext(r.Context())
	}))
	h.ServeHTTP(w, r)

	if seen == uuid.Nil {
		t.Fatal("middleware did not mint a fresh id")
	}
	if seen.Version() != 7 {
		t.Fatalf("minted UUID is v%d, want v7", seen.Version())
	}
}

func TestRejectsV4(t *testing.T) {
	v4 := uuid.New() // v4 by default
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set(reqid.Header, v4.String())

	var seen uuid.UUID
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = reqid.FromContext(r.Context())
	}))
	h.ServeHTTP(w, r)

	if seen == v4 {
		t.Fatal("middleware accepted a v4 UUID; only v7 should be reused")
	}
	if seen.Version() != 7 {
		t.Fatalf("minted UUID is v%d, want v7", seen.Version())
	}
}
