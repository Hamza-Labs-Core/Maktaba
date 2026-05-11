package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func TestVerifyWebhookSignature_Roundtrip(t *testing.T) {
	secret := "whsec_test"
	payload := []byte(`{"id":"evt_1","type":"checkout.session.completed"}`)
	now := time.Unix(1_700_000_000, 0)

	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", now.Unix())
	mac.Write(payload)
	sig := hex.EncodeToString(mac.Sum(nil))
	header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), sig)

	ts, err := VerifyWebhookSignature(payload, header, secret, now.Add(30*time.Second), 5*time.Minute)
	if err != nil {
		t.Fatalf("Verify good: %v", err)
	}
	if !ts.Equal(now) {
		t.Errorf("timestamp mismatch: got %v want %v", ts, now)
	}

	_, err = VerifyWebhookSignature(payload, header, "wrong", now, 5*time.Minute)
	if err == nil {
		t.Error("Verify wrong secret: expected error, got nil")
	}

	_, err = VerifyWebhookSignature(payload, header, secret, now.Add(10*time.Minute), 5*time.Minute)
	if err == nil {
		t.Error("Verify stale: expected error, got nil")
	}
}
