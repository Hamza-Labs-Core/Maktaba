package mail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Resend webhook event types we act on.
const (
	EventBounced         = "email.bounced"
	EventComplained      = "email.complained"
	EventDeliveryDelayed = "email.delivery_delayed"
	EventFailed          = "email.failed"
)

// Normalized bounce classifications.
const (
	BounceHard = "hard"
	BounceSoft = "soft"
)

// Operator alert codes logged at error level so obs/maktaba_errors_total and alerts.yml catch them.
const (
	AlertBounceRate    = "mail_bounce_rate_high"
	AlertComplaintRate = "mail_complaint_rate_high"
)

// SvixTolerance bounds how far a webhook's svix-timestamp may drift from now.
const SvixTolerance = 5 * time.Minute

var (
	// ErrBadWebhookSignature: missing, expired or non-matching Svix signature headers.
	ErrBadWebhookSignature = errors.New("mail: invalid webhook signature")
	// ErrBadWebhookEvent: malformed webhook payload.
	ErrBadWebhookEvent = errors.New("mail: malformed webhook event")
)

// WebhookEvent is the privacy-preserving slice of a Resend webhook delivery: never a recipient
// address, subject, or bounce diagnostic text.
type WebhookEvent struct {
	EventID    string
	Type       string
	EmailID    string
	BounceType string // BounceHard, BounceSoft, or ""
	OccurredAt time.Time
}

// TrackedWebhookEvent reports whether eventType is one of the delivery-failure events we record.
func TrackedWebhookEvent(eventType string) bool {
	switch eventType {
	case EventBounced, EventComplained, EventDeliveryDelayed, EventFailed:
		return true
	default:
		return false
	}
}

// DecodeWebhookSecret extracts the raw HMAC key from a Resend/Svix signing secret ("whsec_<base64>").
func DecodeWebhookSecret(secret string) ([]byte, error) {
	secret = strings.TrimSpace(secret)
	raw, ok := strings.CutPrefix(secret, "whsec_")
	if !ok || raw == "" {
		return nil, errors.New("mail: the Resend webhook secret must begin with whsec_")
	}
	if strings.ContainsFunc(secret, func(r rune) bool { return r <= ' ' || r > '~' }) {
		return nil, errors.New("mail: the Resend webhook secret holds a space or non-ASCII character")
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(raw); err == nil && len(b) > 0 {
			return b, nil
		}
	}
	return nil, errors.New("mail: the Resend webhook secret is not valid base64")
}

// VerifySvix checks Svix's svix-id, svix-timestamp and svix-signature headers against body using
// secret ("whsec_<base64>"). Returns the svix-id on success.
func VerifySvix(secret string, headers http.Header, body []byte, now time.Time) (string, error) {
	key, err := DecodeWebhookSecret(secret)
	if err != nil {
		return "", fmt.Errorf("%w: secret", ErrBadWebhookSignature)
	}
	id := strings.TrimSpace(headers.Get("svix-id"))
	tsRaw := strings.TrimSpace(headers.Get("svix-timestamp"))
	sigHeader := strings.TrimSpace(strings.Join(headers.Values("svix-signature"), " "))
	if id == "" || tsRaw == "" || sigHeader == "" {
		return "", fmt.Errorf("%w: missing headers", ErrBadWebhookSignature)
	}
	tsSec, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return "", fmt.Errorf("%w: bad timestamp", ErrBadWebhookSignature)
	}
	if now.IsZero() {
		now = time.Now()
	}
	drift := now.UTC().Sub(time.Unix(tsSec, 0).UTC())
	if drift < -SvixTolerance || drift > SvixTolerance {
		return "", fmt.Errorf("%w: timestamp outside tolerance", ErrBadWebhookSignature)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write([]byte(tsRaw))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)

	for _, tok := range strings.Fields(sigHeader) {
		ver, b64, ok := strings.Cut(tok, ",")
		if !ok || ver != "v1" || b64 == "" {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			continue
		}
		if hmac.Equal(got, expected) {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: mismatch", ErrBadWebhookSignature)
}

// SignSvix produces svix-signature for tests and tooling.
func SignSvix(secret, id, timestamp string, body []byte) (string, error) {
	key, err := DecodeWebhookSecret(secret)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// ParseResendWebhook verifies the Svix signature and extracts the fields we keep (never any email
// address, subject, or diagnostic text).
func ParseResendWebhook(secret string, headers http.Header, body []byte, now time.Time) (WebhookEvent, error) {
	svixID, err := VerifySvix(secret, headers, body, now)
	if err != nil {
		return WebhookEvent{}, err
	}
	var wire struct {
		Type      string `json:"type"`
		CreatedAt string `json:"created_at"`
		Data      struct {
			EmailID string `json:"email_id"`
			Bounce  *struct {
				Type string `json:"type"`
			} `json:"bounce"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return WebhookEvent{}, fmt.Errorf("%w: json", ErrBadWebhookEvent)
	}
	if wire.Type == "" {
		return WebhookEvent{}, fmt.Errorf("%w: missing type", ErrBadWebhookEvent)
	}
	at := now.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if wire.CreatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, wire.CreatedAt); err == nil {
			at = parsed.UTC()
		}
	}
	ev := WebhookEvent{
		EventID:    svixID,
		Type:       wire.Type,
		EmailID:    strings.TrimSpace(wire.Data.EmailID),
		OccurredAt: at,
	}
	if ev.Type == EventBounced {
		bt := ""
		if wire.Data.Bounce != nil {
			bt = strings.ToLower(strings.TrimSpace(wire.Data.Bounce.Type))
		}
		switch bt {
		case "transient", "soft", "undetermined":
			ev.BounceType = BounceSoft
		default:
			// Permanent, hard, or omitted bounce type: suppress to protect sender reputation.
			ev.BounceType = BounceHard
		}
	}
	return ev, nil
}
