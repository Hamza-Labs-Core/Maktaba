//go:build integration

package main

import (
	"net/http"
	"testing"
	"time"
)

func subscriptionObj(id, customer, userID, status, price string, periodEnd time.Time) map[string]any {
	return map[string]any{
		"id": id, "object": "subscription", "customer": customer, "status": status,
		"cancel_at_period_end": false,
		"metadata":             map[string]string{"user_id": userID},
		"items": map[string]any{"data": []any{map[string]any{
			"current_period_end": periodEnd.Unix(),
			"price":              map[string]any{"id": price, "recurring": map[string]string{"interval": "month"}},
		}}},
	}
}

// Checkout → webhooks → plan, end to end against a fake Stripe in test
// mode: the plan follows the subscription, retries are no-ops, a late
// older event cannot undo a newer one, and the servers' plan follows.
func TestStripeCheckoutAndWebhooks(t *testing.T) {
	e := newEnv(t)
	const email = "payer@example.test"
	c := e.registerVerified(email)
	uid := e.userID(email)
	e.exec(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, 'Home', 'payer-home', 'x')`, uid)

	plans := c.mustStatus(c.do("GET", "/v1/billing/plans", nil), 200).json(t)["plans"].([]any)
	if len(plans) != 3 || plans[1].(map[string]any)["purchasable"] != true {
		t.Fatalf("plans: %v", plans)
	}
	if r := c.do("POST", "/v1/billing/checkout", map[string]string{"plan": "free"}); r.code != 422 {
		t.Fatalf("checkout free: %d", r.code)
	}
	if r := c.do("POST", "/v1/billing/portal", nil); r.code != 409 || r.errCode() != "no_customer" {
		t.Fatalf("portal before paying: %d %s", r.code, r.body)
	}
	r := c.mustStatus(c.do("POST", "/v1/billing/checkout", map[string]string{"plan": "pro"}), 200)
	if r.json(t)["checkout_url"] != "https://checkout.stripe.test/c/cs_test_1" {
		t.Fatalf("checkout: %s", r.body)
	}
	req, _ := e.stripe.last("/v1/checkout/sessions")
	if req.Auth != "Bearer sk_test_integration" || req.Form.Get("line_items[0][price]") != testPricePro ||
		req.Form.Get("subscription_data[metadata][user_id]") != uid || req.Form.Get("client_reference_id") != uid ||
		req.Form.Get("customer_email") != email || req.Form.Get("mode") != "subscription" {
		t.Fatalf("checkout request: %+v", req)
	}

	stripe := e.client()
	// A forged event is refused and recorded.
	body, sig := signedEvent("evt_forged", "customer.subscription.created", time.Now(), subscriptionObj("sub_1", "cus_test_1", uid, "active", testPricePro, time.Now()))
	if r := stripe.webhook(body, forge(sig)); r.code != 400 || r.errCode() != "invalid_signature" {
		t.Fatalf("forged: %d %s", r.code, r.body)
	}
	if e.count(`SELECT count(*) FROM abuse_signals WHERE kind = 'stripe_signature_forgery'`) != 1 {
		t.Fatal("forgery not recorded")
	}

	t0 := time.Now().Add(-time.Minute)
	body, sig = signedEvent("evt_checkout", "checkout.session.completed", t0, map[string]any{
		"id": "cs_test_1", "customer": "cus_test_1", "subscription": "sub_1", "client_reference_id": uid,
	})
	stripe.mustStatus(stripe.webhook(body, sig), 200)

	periodEnd := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	body, sig = signedEvent("evt_created", "customer.subscription.created", t0.Add(time.Second), subscriptionObj("sub_1", "cus_test_1", uid, "active", testPricePro, periodEnd))
	stripe.mustStatus(stripe.webhook(body, sig), 200)
	// Retry of the same event: 200, no second row, no work.
	stripe.mustStatus(stripe.webhook(body, sig), 200)
	if n := e.count(`SELECT count(*) FROM stripe_events WHERE event_id = 'evt_created'`); n != 1 {
		t.Fatalf("event rows %d", n)
	}
	me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t)
	if me["plan"] != "pro" || me["status"] != "active" || me["interval"] != "monthly" || me["has_customer"] != true {
		t.Fatalf("billing/me: %v", me)
	}
	if e.count(`SELECT count(*) FROM servers WHERE owner_user_id = $1 AND plan = 'pro'`, uid) != 1 {
		t.Fatal("server plan did not follow the subscription")
	}

	// Already subscribed: checkout points to the portal; portal works.
	if r := c.do("POST", "/v1/billing/checkout", map[string]string{"plan": "family"}); r.code != 409 || r.errCode() != "already_subscribed" {
		t.Fatalf("second checkout: %d %s", r.code, r.body)
	}
	if r := c.mustStatus(c.do("POST", "/v1/billing/portal", nil), 200); r.json(t)["portal_url"] == "" {
		t.Fatal("no portal url")
	}
	if req, _ := e.stripe.last("/v1/billing_portal/sessions"); req.Form.Get("customer") != "cus_test_1" {
		t.Fatalf("portal request: %+v", req)
	}

	// Upgrade to family (newer), then a stale "updated" from before it
	// arrives late and must not downgrade.
	body, sig = signedEvent("evt_upgrade", "customer.subscription.updated", t0.Add(10*time.Second), subscriptionObj("sub_1", "cus_test_1", uid, "active", testPriceFamily, periodEnd))
	stripe.mustStatus(stripe.webhook(body, sig), 200)
	body, sig = signedEvent("evt_stale", "customer.subscription.updated", t0.Add(5*time.Second), subscriptionObj("sub_1", "cus_test_1", uid, "active", testPricePro, periodEnd))
	stripe.mustStatus(stripe.webhook(body, sig), 200)
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["plan"] != "family" {
		t.Fatalf("stale event applied: %v", me)
	}

	// A failed invoice: ledger row, past_due, a mail.
	body, sig = signedEvent("evt_inv_failed", "invoice.payment_failed", t0.Add(20*time.Second), map[string]any{
		"id": "in_1", "customer": "cus_test_1", "amount_due": 1000, "currency": "usd",
	})
	stripe.mustStatus(stripe.webhook(body, sig), 200)
	if e.count(`SELECT count(*) FROM invoices WHERE stripe_invoice_id = 'in_1' AND status = 'failed'`) != 1 {
		t.Fatal("failed invoice not recorded")
	}
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["status"] != "past_due" || me["plan"] != "family" {
		t.Fatalf("past_due: %v", me)
	}
	if _, ok := e.mail.Last("payment_failed", email); !ok {
		t.Fatal("payment-failed mail not sent")
	}

	// Cancelled: back to free, servers too. Metadata absent — resolved by customer.
	obj := subscriptionObj("sub_1", "cus_test_1", "", "canceled", testPriceFamily, periodEnd)
	body, sig = signedEvent("evt_deleted", "customer.subscription.deleted", t0.Add(30*time.Second), obj)
	stripe.mustStatus(stripe.webhook(body, sig), 200)
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["plan"] != "free" || me["status"] != "canceled" {
		t.Fatalf("after cancel: %v", me)
	}
	if e.count(`SELECT count(*) FROM servers WHERE owner_user_id = $1 AND plan = 'free'`, uid) != 1 {
		t.Fatal("server plan not reset")
	}
	// After a cancellation a new checkout is allowed, reusing the customer.
	c.mustStatus(c.do("POST", "/v1/billing/checkout", map[string]string{"plan": "pro"}), 200)
	if req, _ := e.stripe.last("/v1/checkout/sessions"); req.Form.Get("customer") != "cus_test_1" || req.Form.Get("customer_email") != "" {
		t.Fatalf("returning checkout: %+v", req.Form)
	}

	// An event about a customer we never created is acknowledged.
	body, sig = signedEvent("evt_foreign", "invoice.paid", time.Now(), map[string]any{"id": "in_x", "customer": "cus_unknown"})
	stripe.mustStatus(stripe.webhook(body, sig), 200)

	// A dispute suspends the payer and signs them out.
	body, sig = signedEvent("evt_dispute", "charge.dispute.created", time.Now(), map[string]any{"id": "dp_1", "charge": "ch_1"})
	stripe.mustStatus(stripe.webhook(body, sig), 200)
	if r := c.do("GET", "/v1/account/me", nil); r.code != 403 || r.errCode() != "account_suspended" {
		t.Fatalf("after dispute: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/v1/auth/refresh", nil); r.code != 401 && r.code != 403 {
		t.Fatalf("refresh after dispute: %d", r.code)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'billing.dispute_suspend' AND target_id = $1`, uid) != 1 {
		t.Fatal("dispute suspension not audited")
	}
}

// Usage reads the bandwidth meter's monthly rows.
func TestUsage(t *testing.T) {
	e := newEnv(t)
	const email = "ent@example.test"
	c := e.registerVerified(email)
	uid := e.userID(email)
	e.exec(`UPDATE users SET plan = 'pro' WHERE id = $1`, uid)

	var sid string
	if err := e.pool.DB.QueryRow(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1,'NAS','ent-nas','x') RETURNING id`, uid).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO bandwidth_monthly (server_id, month, bytes_in, bytes_out) VALUES ($1, date_trunc('month', now() AT TIME ZONE 'UTC')::date, 1000, 5000)`, sid)
	u := c.mustStatus(c.do("GET", "/v1/billing/usage", nil), http.StatusOK).json(t)
	if u["used_bytes"].(float64) != 6000 || u["plan"] != "pro" || u["limit_bytes"].(float64) != float64(100<<30) {
		t.Fatalf("usage: %v", u)
	}
}

// forge changes the signature's last hex digit to another one, so it never matches. (Setting
// the last two digits to "00" left a signature that already ended in "00" valid: one run in
// 256 failed.)
func forge(sig string) string {
	last := sig[len(sig)-1]
	repl := byte('0')
	if last == '0' {
		repl = '1'
	}
	return sig[:len(sig)-1] + string(repl)
}
