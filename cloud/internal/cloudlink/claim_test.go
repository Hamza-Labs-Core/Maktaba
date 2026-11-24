package cloudlink

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

// The retry key: the same for the same code after a restart (a new Claimer with the home's key),
// another one for another code or another home, not the home's key itself, and absent without one.
func TestRedeem_RetryKeyIsTheAgents(t *testing.T) {
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req claimRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		sent = append(sent, req.RetryKey)
		if len(sent) == 1 {
			// the answer is lost: the connection drops
			c, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = c.Close()
			}
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(claimResponse{ServerID: "srv", ServerSecret: "s", Slug: "acme"})
	}))
	defer srv.Close()
	key, other := bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)
	redeem := func(k []byte, code string) error {
		_, err := (&Claimer{Endpoint: srv.URL, Key: k}).Redeem(context.Background(), code, "box", "acme", "1", "")
		return err
	}
	if err := redeem(key, "K3F9MZ7P"); err == nil {
		t.Fatal("a dropped answer is an error")
	}
	for _, c := range []struct {
		key  []byte
		code string
	}{{key, "K3F9MZ7P"}, {key, " k3f9mz7p "}, {key, "OTHER777"}, {other, "K3F9MZ7P"}, {nil, "K3F9MZ7P"}} {
		if err := redeem(c.key, c.code); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent[0]) != 64 || sent[1] != sent[0] || sent[2] != sent[0] {
		t.Fatalf("the same agent's key changed: %q", sent[:3])
	}
	// fixed: an agent updated between its first try and its second must send the same key, so the
	// derivation (the prefix, the code's form, HMAC-SHA256) never changes
	if want := "ea63fa43754460eb72fbca3de8f706d3a1649e8a8d638f3940c61265ff65414c"; sent[0] != want {
		t.Fatalf("the retry key's derivation changed: %s, want %s", sent[0], want)
	}
	if sent[3] == sent[0] || sent[4] == sent[0] || sent[5] != "" || sent[0] == hex.EncodeToString(key) {
		t.Fatalf("keys: %q", sent)
	}
}
