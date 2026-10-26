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
// REST API. We use three endpoints:
//   - POST /v1/checkout/sessions       — create a checkout session
//   - POST /v1/billing_portal/sessions — open the customer portal
//   - GET  /v1/charges/{id}            — resolve a disputed charge's customer
//
// Doing this by hand avoids pulling in stripe-go (large transitive deps)
// for what amounts to three calls. Tests point APIBase at a fake.
type StripeClient struct {
	SecretKey string
	HTTP      *http.Client
	APIBase   string
}

func NewStripeClient(secret string) *StripeClient {
	return &StripeClient{
		SecretKey: secret,
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		APIBase:   "https://api.stripe.com",
	}
}

// Configured reports whether a secret key is set.
func (c *StripeClient) Configured() bool { return c != nil && c.SecretKey != "" }

// CheckoutSession is the trimmed-down response shape we care about.
type CheckoutSession struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// CheckoutParams describes a subscription checkout.
type CheckoutParams struct {
	PriceID string
	// Exactly one of Customer (a returning payer) or CustomerEmail.
	Customer      string
	CustomerEmail string
	SuccessURL    string
	CancelURL     string
	// UserID is stamped on the session (client_reference_id) and on the
	// subscription's metadata, so every later subscription event names
	// the account even before we know the Stripe customer.
	UserID string
}

// CreateCheckoutSession opens a hosted Stripe Checkout.
func (c *StripeClient) CreateCheckoutSession(ctx context.Context, p CheckoutParams) (CheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "subscription")
	if p.Customer != "" {
		form.Set("customer", p.Customer)
	} else {
		form.Set("customer_email", p.CustomerEmail)
	}
	form.Set("client_reference_id", p.UserID)
	form.Set("metadata[user_id]", p.UserID)
	form.Set("subscription_data[metadata][user_id]", p.UserID)
	form.Set("success_url", p.SuccessURL)
	form.Set("cancel_url", p.CancelURL)
	form.Set("line_items[0][price]", p.PriceID)
	form.Set("line_items[0][quantity]", "1")
	form.Set("allow_promotion_codes", "true")

	body, err := c.post(ctx, "/v1/checkout/sessions", form)
	if err != nil {
		return CheckoutSession{}, err
	}
	var s CheckoutSession
	return s, json.Unmarshal(body, &s)
}

// CreatePortalSession opens the Stripe customer portal, where the payer
// changes plan, card, or cancels.
func (c *StripeClient) CreatePortalSession(ctx context.Context, customer, returnURL string) (string, error) {
	form := url.Values{}
	form.Set("customer", customer)
	form.Set("return_url", returnURL)
	body, err := c.post(ctx, "/v1/billing_portal/sessions", form)
	if err != nil {
		return "", err
	}
	var s struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return "", err
	}
	return s.URL, nil
}

// ChargeCustomer returns the customer id of a charge (dispute events
// carry only the charge).
func (c *StripeClient) ChargeCustomer(ctx context.Context, chargeID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+"/v1/charges/"+url.PathEscape(chargeID), nil)
	if err != nil {
		return "", err
	}
	body, err := c.do(req)
	if err != nil {
		return "", err
	}
	var ch struct {
		Customer string `json:"customer"`
	}
	if err := json.Unmarshal(body, &ch); err != nil {
		return "", err
	}
	return ch.Customer, nil
}

func (c *StripeClient) post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIBase+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

func (c *StripeClient) do(req *http.Request) ([]byte, error) {
	req.Header.Set("Authorization", "Bearer "+c.SecretKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return nil, fmt.Errorf("stripe: %s %s: %d %s", req.Method, req.URL.Path, resp.StatusCode, e.Error.Message)
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
