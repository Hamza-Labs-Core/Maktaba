package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// StripeClient is a hand-rolled minimum-surface client for the Stripe
// REST API. We only use two endpoints:
//   - POST /v1/checkout/sessions       — create a checkout session
//   - POST /v1/billing_portal/sessions — open the customer portal
// Doing this by hand avoids pulling in stripe-go (large transitive deps)
// for what amounts to two POSTs.
type StripeClient struct {
	SecretKey   string
	HTTP        *http.Client
	APIBase     string // override in tests; "" → live API
}

func NewStripeClient(secret string) *StripeClient {
	return &StripeClient{
		SecretKey: secret,
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		APIBase:   "https://api.stripe.com",
	}
}

// CheckoutSession is the trimmed-down response shape we care about.
type CheckoutSession struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// CreateCheckoutSession opens a hosted Stripe Checkout. `priceID` is
// the price reference we configured in the Stripe dashboard; `successURL`
// and `cancelURL` close the loop back to our SPA.
func (c *StripeClient) CreateCheckoutSession(ctx context.Context, customerEmail, priceID, successURL, cancelURL, clientReferenceID string) (CheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("customer_email", customerEmail)
	form.Set("client_reference_id", clientReferenceID)
	form.Set("success_url", successURL)
	form.Set("cancel_url", cancelURL)
	form.Set("line_items[0][price]", priceID)
	form.Set("line_items[0][quantity]", "1")
	form.Set("allow_promotion_codes", "true")

	body, err := c.post(ctx, "/v1/checkout/sessions", form)
	if err != nil {
		return CheckoutSession{}, err
	}
	var s CheckoutSession
	return s, json.Unmarshal(body, &s)
}

func (c *StripeClient) post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIBase+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.SecretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("stripe: %s %s: %s", req.Method, path, string(body))
	}
	return body, nil
}

// VerifyWebhookSignature checks the Stripe-Signature header.
// Format: `t=<unix>,v1=<hex hmac>` (multiple v1= are allowed; we
// accept the FIRST that matches).
//
// Returns the parsed timestamp so the caller can enforce a freshness
// window (typically 5 minutes) to defeat replay.
func VerifyWebhookSignature(payload []byte, sigHeader, secret string, now time.Time, tolerance time.Duration) (time.Time, error) {
	if sigHeader == "" {
		return time.Time{}, errors.New("stripe webhook: missing signature header")
	}
	var ts int64
	var v1s []string
	for _, part := range strings.Split(sigHeader, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			n, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return time.Time{}, errors.New("stripe webhook: bad timestamp")
			}
			ts = n
		case "v1":
			v1s = append(v1s, kv[1])
		}
	}
	if ts == 0 || len(v1s) == 0 {
		return time.Time{}, errors.New("stripe webhook: incomplete signature")
	}
	tsTime := time.Unix(ts, 0)
	if now.Sub(tsTime) > tolerance || tsTime.Sub(now) > tolerance {
		return time.Time{}, errors.New("stripe webhook: stale signature")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(payload)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, v := range v1s {
		if hmac.Equal([]byte(v), []byte(want)) {
			return tsTime, nil
		}
	}
	return time.Time{}, errors.New("stripe webhook: signature mismatch")
}
