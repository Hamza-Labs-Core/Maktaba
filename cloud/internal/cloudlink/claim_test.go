package cloudlink

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestRedeem_Success drives the claim REST call against a server that
// mimics cloud/internal/handlers/servers/servers.go RedeemClaim: it
// asserts the request body shape matches redeemReq and returns the
// redeemResp shape.
func TestRedeem_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/claims/redeem" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var req claimRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("server could not decode redeemReq: %v", err)
		}
		if req.Code != "K3F9MZ7P" || req.Slug != "acme" || req.Version != "1.2.3" {
			t.Errorf("redeemReq mismatch: %+v", req)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(claimResponse{
			ServerID:     "srv-uuid",
			ServerSecret: "secret-token",
			Slug:         "acme",
		})
	}))
	defer srv.Close()

	c := &Claimer{Endpoint: srv.URL}
	creds, err := c.Redeem(context.Background(), "K3F9MZ7P", "box", "acme", "1.2.3", "")
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if creds.ServerID != "srv-uuid" || creds.Secret != "secret-token" || creds.Slug != "acme" {
		t.Fatalf("creds = %+v", creds)
	}
}

func TestRedeem_RejectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"claim_invalid"}`))
	}))
	defer srv.Close()
	c := &Claimer{Endpoint: srv.URL}
	_, err := c.Redeem(context.Background(), "BADCODE0", "n", "s", "v", "")
	if !errors.Is(err, ErrClaimRejected) {
		t.Fatalf("err = %v, want ErrClaimRejected", err)
	}
}

func TestRedeem_EmptyCredsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"server_id":"","server_secret":"","slug":"x"}`))
	}))
	defer srv.Close()
	c := &Claimer{Endpoint: srv.URL}
	_, err := c.Redeem(context.Background(), "CODE0000", "n", "s", "v", "")
	if !errors.Is(err, ErrClaimRejected) {
		t.Fatalf("err = %v, want ErrClaimRejected for empty creds", err)
	}
}

// TestCredentials_RoundTrip proves credentials are sealed at rest and
// recover exactly (Story 25.6 encrypted-at-rest AC).
func TestCredentials_RoundTrip(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	path := filepath.Join(t.TempDir(), "nested", "cloudlink.cred")
	want := Credentials{ServerID: "id", Secret: "s3kr3t", Slug: "acme"}

	if err := SaveCredentials(path, key, want); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	got, err := LoadCredentials(path, key)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if got != want {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, want)
	}
}

func TestCredentials_SealedNotPlaintext(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	path := filepath.Join(t.TempDir(), "c.cred")
	if err := SaveCredentials(path, key, Credentials{Secret: "PLAINTEXTSECRET"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("PLAINTEXTSECRET")) {
		t.Fatal("secret stored in plaintext on disk")
	}
}

func TestCredentials_WrongKeyFails(t *testing.T) {
	k1 := make([]byte, 32)
	k2 := make([]byte, 32)
	_, _ = rand.Read(k1)
	_, _ = rand.Read(k2)
	path := filepath.Join(t.TempDir(), "c.cred")
	if err := SaveCredentials(path, k1, Credentials{Secret: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(path, k2); err == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
}

func TestNewGCM_RejectsShortKey(t *testing.T) {
	if _, err := newGCM([]byte("short")); err == nil {
		t.Fatal("expected error for non-32-byte key")
	}
}
