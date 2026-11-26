package mail

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testWebhookSecret() string {
	return "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
}

func signedHeaders(t *testing.T, secret, id string, ts time.Time, body []byte) http.Header {
	t.Helper()
	tsStr := strconv.FormatInt(ts.Unix(), 10)
	sig, err := SignSvix(secret, id, tsStr, body)
	if err != nil {
		t.Fatal(err)
	}
	h := make(http.Header)
	h.Set("svix-id", id)
	h.Set("svix-timestamp", tsStr)
	h.Set("svix-signature", sig)
	return h
}

func TestParseResendWebhookValidAndEvents(t *testing.T) {
	sec := testWebhookSecret()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		body       string
		wantType   string
		wantEmail  string
		wantBounce string
	}{
		{
			name:       "hard bounce",
			body:       `{"type":"email.bounced","created_at":"2026-11-25T11:59:50Z","data":{"email_id":"re-msg-1","to":["secret@example.test"],"bounce":{"type":"Permanent","message":"550 secret@example.test unknown"}}}`,
			wantType:   EventBounced,
			wantEmail:  "re-msg-1",
			wantBounce: BounceHard,
		},
		{
			name:       "soft bounce",
			body:       `{"type":"email.bounced","created_at":"2026-11-25T11:59:50Z","data":{"email_id":"re-msg-2","bounce":{"type":"Transient"}}}`,
			wantType:   EventBounced,
			wantEmail:  "re-msg-2",
			wantBounce: BounceSoft,
		},
		{
			name:       "bounce without subtype defaults hard",
			body:       `{"type":"email.bounced","data":{"email_id":"re-msg-3"}}`,
			wantType:   EventBounced,
			wantEmail:  "re-msg-3",
			wantBounce: BounceHard,
		},
		{
			name:      "complaint",
			body:      `{"type":"email.complained","created_at":"2026-11-25T11:59:55Z","data":{"email_id":"re-msg-4","to":["victim@example.test"]}}`,
			wantType:  EventComplained,
			wantEmail: "re-msg-4",
		},
		{
			name:      "delivery delayed",
			body:      `{"type":"email.delivery_delayed","data":{"email_id":"re-msg-5"}}`,
			wantType:  EventDeliveryDelayed,
			wantEmail: "re-msg-5",
		},
		{
			name:      "failed",
			body:      `{"type":"email.failed","data":{"email_id":"re-msg-6"}}`,
			wantType:  EventFailed,
			wantEmail: "re-msg-6",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := signedHeaders(t, sec, "msg_"+tc.wantEmail, now, []byte(tc.body))
			ev, err := ParseResendWebhook(sec, h, []byte(tc.body), now)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if ev.EventID != "msg_"+tc.wantEmail || ev.Type != tc.wantType || ev.EmailID != tc.wantEmail || ev.BounceType != tc.wantBounce {
				t.Fatalf("got %+v", ev)
			}
			if !TrackedWebhookEvent(ev.Type) {
				t.Fatalf("%q should be tracked", ev.Type)
			}
		})
	}
	if TrackedWebhookEvent("email.sent") || TrackedWebhookEvent("email.delivered") {
		t.Fatal("non-failure events should not be tracked")
	}
}

func TestVerifySvixRotationAndRefusals(t *testing.T) {
	sec := testWebhookSecret()
	other := "whsec_" + base64.StdEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"type":"email.complained","data":{"email_id":"id-1"}}`)

	// Multiple space-separated signatures during key rotation: second matches.
	tsStr := strconv.FormatInt(now.Unix(), 10)
	badSig, _ := SignSvix(other, "msg_rot", tsStr, body)
	goodSig, _ := SignSvix(sec, "msg_rot", tsStr, body)
	h := http.Header{"Svix-Id": {"msg_rot"}, "Svix-Timestamp": {tsStr}, "Svix-Signature": {badSig + " " + goodSig}}
	if id, err := VerifySvix(sec, h, body, now); err != nil || id != "msg_rot" {
		t.Fatalf("rotated sig: id=%q err=%v", id, err)
	}

	// Wrong secret / tampered body / missing header / stale timestamp / future timestamp.
	goodH := signedHeaders(t, sec, "msg_1", now, body)
	if _, err := VerifySvix(other, goodH, body, now); !errors.Is(err, ErrBadWebhookSignature) {
		t.Errorf("wrong secret: %v", err)
	}
	if _, err := VerifySvix(sec, goodH, append(body, ' '), now); !errors.Is(err, ErrBadWebhookSignature) {
		t.Errorf("tampered body: %v", err)
	}
	if _, err := VerifySvix(sec, http.Header{}, body, now); !errors.Is(err, ErrBadWebhookSignature) {
		t.Errorf("missing headers: %v", err)
	}
	staleH := signedHeaders(t, sec, "msg_old", now.Add(-SvixTolerance-time.Second), body)
	if _, err := VerifySvix(sec, staleH, body, now); !errors.Is(err, ErrBadWebhookSignature) {
		t.Errorf("stale timestamp: %v", err)
	}
	futureH := signedHeaders(t, sec, "msg_fut", now.Add(SvixTolerance+time.Second), body)
	if _, err := VerifySvix(sec, futureH, body, now); !errors.Is(err, ErrBadWebhookSignature) {
		t.Errorf("future timestamp: %v", err)
	}
	// Secret never leaks into error text.
	_, err := DecodeWebhookSecret("whsec_not!base64")
	if err == nil || strings.Contains(err.Error(), "not!base64") {
		t.Errorf("bad secret error: %v", err)
	}
}
