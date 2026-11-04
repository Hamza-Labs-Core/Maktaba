//go:build integration

package main

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

func subEvent(id, kind, sub, customer, userID, status, plan string, at, periodEnd time.Time) testEvent {
	return testEvent{ID: id, Kind: kind, At: at, UserID: userID, CustomerID: customer, SubscriptionID: sub,
		Plan: plan, Status: status, Interval: "monthly", PeriodEnd: periodEnd}
}

// With the default "none" provider (no payment provider is chosen yet)
// nothing is for sale: paid plans are not purchasable, checkout and the
// portal answer 503 payments_not_set_up, every webhook route is a 404,
// and a service admin sets the plan by hand — reason required, audited,
// mirrored onto the servers, and not counted as revenue.
func TestBillingNoneProvider(t *testing.T) {
	e := newEnv(t)
	const email = "member@example.test"
	c := e.registerVerified(email)
	uid := e.userID(email)
	e.exec(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, 'Home', 'member-home', 'x')`, uid)
	admin := e.serviceAdmin("ops@example.test")

	cat := c.mustStatus(c.do("GET", "/v1/billing/plans", nil), 200).json(t)
	if cat["payments_enabled"] != false {
		t.Fatalf("payments_enabled: %v", cat)
	}
	plans := cat["plans"].([]any)
	if len(plans) != 3 {
		t.Fatalf("plans: %v", plans)
	}
	for _, p := range plans {
		if p.(map[string]any)["purchasable"] != false {
			t.Fatalf("a plan is purchasable with no provider: %v", p)
		}
	}
	for _, plan := range []string{"pro", "family"} {
		if r := c.do("POST", "/v1/billing/checkout", map[string]string{"plan": plan}); r.code != 503 || r.errCode() != "payments_not_set_up" {
			t.Fatalf("checkout %s: %d %s", plan, r.code, r.body)
		}
	}
	if r := c.do("POST", "/v1/billing/portal", nil); r.code != 503 || r.errCode() != "payments_not_set_up" {
		t.Fatalf("portal: %d %s", r.code, r.body)
	}
	hook := e.client()
	body, sig := signedEvent(subEvent("evt_1", "subscription_started", "sub_1", "cus_1", uid, "active", "pro", time.Now(), time.Now()))
	for _, provider := range []string{"none", "testpay", "anything"} {
		if r := hook.webhook(provider, body, sig); r.code != 404 {
			t.Fatalf("webhook/%s with no provider: %d %s", provider, r.code, r.body)
		}
	}
	if e.count(`SELECT count(*) FROM provider_events`) != 0 {
		t.Fatal("a webhook was recorded with no provider")
	}
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["plan"] != "free" || me["status"] != "none" {
		t.Fatalf("billing/me: %v", me)
	}

	// The manual path: a service admin sets the plan.
	path := "/v1/admin/accounts/" + uid + "/plan"
	if r := admin.do("POST", path, map[string]string{"plan": "pro"}); r.code != 400 || r.errCode() != "reason_required" {
		t.Fatalf("no reason: %d %s", r.code, r.body)
	}
	if r := admin.do("POST", path, map[string]string{"plan": "gold", "reason": "x"}); r.code != 422 || r.errCode() != "bad_plan" {
		t.Fatalf("bad plan: %d %s", r.code, r.body)
	}
	if r := admin.do("POST", "/v1/admin/accounts/not-a-uuid/plan", map[string]string{"plan": "pro", "reason": "x"}); r.code != 404 {
		t.Fatalf("bad id: %d", r.code)
	}
	if r := c.do("POST", path, map[string]string{"plan": "pro", "reason": "self"}); r.code != 403 {
		t.Fatalf("a member set their own plan: %d", r.code)
	}
	admin.mustStatus(admin.do("POST", path, map[string]string{"plan": "pro", "reason": "early supporter"}), http.StatusNoContent)
	me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t)
	if me["plan"] != "pro" || me["status"] != "active" || me["has_customer"] != false {
		t.Fatalf("after manual pro: %v", me)
	}
	if e.count(`SELECT count(*) FROM servers WHERE owner_user_id = $1 AND plan = 'pro'`, uid) != 1 {
		t.Fatal("server plan did not follow the manual plan")
	}
	if e.count(`SELECT count(*) FROM subscriptions WHERE user_id = $1 AND provider = 'manual'`, uid) != 1 {
		t.Fatal("manual subscription not marked")
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.account.set_plan' AND target_id = $1
        AND detail->>'plan' = 'pro' AND detail->>'previous_plan' = 'free' AND detail->>'reason' = 'early supporter'`, uid) != 1 {
		t.Fatal("manual plan change not audited")
	}
	detail := admin.mustStatus(admin.do("GET", "/v1/admin/accounts/"+uid, nil), 200).json(t)
	if sub := detail["subscription"].(map[string]any); sub["provider"] != "manual" || sub["plan"] != "pro" {
		t.Fatalf("admin detail subscription: %v", sub)
	}
	if u := c.mustStatus(c.do("GET", "/v1/billing/usage", nil), 200).json(t); u["limit_bytes"].(float64) != float64(100<<30) {
		t.Fatalf("usage limit did not follow the plan: %v", u)
	}
	rev := admin.mustStatus(admin.do("GET", "/v1/admin/revenue", nil), 200).json(t)
	if rev["mrr_cents"].(float64) != 0 || rev["active_paid"].(float64) != 0 {
		t.Fatalf("a hand-set plan counted as revenue: %v", rev)
	}

	// And back to free.
	admin.mustStatus(admin.do("POST", path, map[string]string{"plan": "free", "reason": "trial over"}), http.StatusNoContent)
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["plan"] != "free" || me["status"] != "inactive" {
		t.Fatalf("after manual free: %v", me)
	}
	if e.count(`SELECT count(*) FROM servers WHERE owner_user_id = $1 AND plan = 'free'`, uid) != 1 {
		t.Fatal("server plan not reset")
	}
}

// Checkout → webhooks → plan, end to end through the provider seam with
// a fake provider: the plan follows the subscription, retries are
// no-ops, a late older event cannot undo a newer one, the servers' plan
// follows, and a plan a provider bills for cannot be overridden by hand.
func TestBillingProviderCheckoutAndWebhooks(t *testing.T) {
	e := newPaidEnv(t)
	const email = "payer@example.test"
	c := e.registerVerified(email)
	uid := e.userID(email)
	e.exec(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, 'Home', 'payer-home', 'x')`, uid)

	cat := c.mustStatus(c.do("GET", "/v1/billing/plans", nil), 200).json(t)
	plans := cat["plans"].([]any)
	if cat["payments_enabled"] != true || len(plans) != 3 || plans[0].(map[string]any)["purchasable"] != false ||
		plans[1].(map[string]any)["purchasable"] != true {
		t.Fatalf("plans: %v", cat)
	}
	if r := c.do("POST", "/v1/billing/checkout", map[string]string{"plan": "free"}); r.code != 422 {
		t.Fatalf("checkout free: %d", r.code)
	}
	if r := c.do("POST", "/v1/billing/portal", nil); r.code != 409 || r.errCode() != "no_customer" {
		t.Fatalf("portal before paying: %d %s", r.code, r.body)
	}
	r := c.mustStatus(c.do("POST", "/v1/billing/checkout", map[string]string{"plan": "pro"}), 200)
	if r.json(t)["checkout_url"] != "https://pay.example.test/checkout/cs_1" {
		t.Fatalf("checkout: %s", r.body)
	}
	if p := e.pay.lastCheckout(); p.Plan != "pro" || p.UserID != uid || p.CustomerEmail != email || p.CustomerID != "" ||
		p.SuccessURL != testWebURL+"/billing?billing=ok" || p.CancelURL != testWebURL+"/billing?billing=cancel" {
		t.Fatalf("checkout params: %+v", p)
	}

	hook := e.client()
	// Only the configured provider's route answers.
	t0 := time.Now().Add(-time.Minute)
	body, sig := signedEvent(testEvent{ID: "evt_checkout", Kind: "checkout_completed", At: t0, UserID: uid, CustomerID: "cus_1", SubscriptionID: "sub_1"})
	for _, other := range []string{"none", "other"} {
		if r := hook.webhook(other, body, sig); r.code != 404 {
			t.Fatalf("webhook/%s: %d", other, r.code)
		}
	}
	// A forged event is refused and recorded.
	if r := hook.webhook("testpay", body, forge(sig)); r.code != 400 || r.errCode() != "invalid_signature" {
		t.Fatalf("forged: %d %s", r.code, r.body)
	}
	if e.count(`SELECT count(*) FROM abuse_signals WHERE kind = 'billing_webhook_forgery'`) != 1 {
		t.Fatal("forgery not recorded")
	}
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)

	periodEnd := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	body, sig = signedEvent(subEvent("evt_started", "subscription_started", "sub_1", "cus_1", uid, "active", "pro", t0.Add(time.Second), periodEnd))
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)
	// Retry of the same event: 200, no second row, no work.
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)
	if n := e.count(`SELECT count(*) FROM provider_events WHERE provider = 'testpay' AND provider_event_id = 'evt_started'`); n != 1 {
		t.Fatalf("event rows %d", n)
	}
	me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t)
	if me["plan"] != "pro" || me["status"] != "active" || me["interval"] != "monthly" || me["has_customer"] != true {
		t.Fatalf("billing/me: %v", me)
	}
	if e.count(`SELECT count(*) FROM subscriptions WHERE user_id = $1 AND provider = 'testpay' AND provider_price_id = 'price_pro'`, uid) != 1 {
		t.Fatal("subscription not stamped with its provider")
	}
	if e.count(`SELECT count(*) FROM servers WHERE owner_user_id = $1 AND plan = 'pro'`, uid) != 1 {
		t.Fatal("server plan did not follow the subscription")
	}

	// A plan the provider bills for is not changed by hand.
	admin := e.serviceAdmin("ops@example.test")
	if r := admin.do("POST", "/v1/admin/accounts/"+uid+"/plan", map[string]string{"plan": "free", "reason": "x"}); r.code != 409 || r.errCode() != "provider_managed" {
		t.Fatalf("manual override of a billed plan: %d %s", r.code, r.body)
	}

	// Already subscribed: checkout points to the portal; portal works.
	if r := c.do("POST", "/v1/billing/checkout", map[string]string{"plan": "family"}); r.code != 409 || r.errCode() != "already_subscribed" {
		t.Fatalf("second checkout: %d %s", r.code, r.body)
	}
	if r := c.mustStatus(c.do("POST", "/v1/billing/portal", nil), 200); r.json(t)["portal_url"] != "https://pay.example.test/portal/cus_1" {
		t.Fatalf("portal: %s", r.body)
	}
	if e.pay.lastPortal() != "cus_1" {
		t.Fatalf("portal customer: %q", e.pay.lastPortal())
	}

	// Upgrade to family (newer), then a stale change from before it
	// arrives late and must not downgrade.
	body, sig = signedEvent(subEvent("evt_upgrade", "subscription_changed", "sub_1", "cus_1", uid, "active", "family", t0.Add(10*time.Second), periodEnd))
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)
	body, sig = signedEvent(subEvent("evt_stale", "subscription_changed", "sub_1", "cus_1", uid, "active", "pro", t0.Add(5*time.Second), periodEnd))
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["plan"] != "family" {
		t.Fatalf("stale event applied: %v", me)
	}

	// A failed payment: ledger row, past_due, a mail.
	body, sig = signedEvent(testEvent{ID: "evt_pay_failed", Kind: "payment_failed", At: t0.Add(20 * time.Second),
		CustomerID: "cus_1", InvoiceID: "in_1", AmountCents: 1000, Currency: "usd"})
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)
	if e.count(`SELECT count(*) FROM invoices WHERE provider = 'testpay' AND provider_invoice_id = 'in_1' AND status = 'failed'`) != 1 {
		t.Fatal("failed payment not recorded")
	}
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["status"] != "past_due" || me["plan"] != "family" {
		t.Fatalf("past_due: %v", me)
	}
	if _, ok := e.mail.Last("payment_failed", email); !ok {
		t.Fatal("payment-failed mail not sent")
	}

	// Cancelled: back to free, servers too. No user id — resolved by customer.
	body, sig = signedEvent(subEvent("evt_cancelled", "subscription_cancelled", "sub_1", "cus_1", "", "canceled", "family", t0.Add(30*time.Second), periodEnd))
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)
	if me := c.mustStatus(c.do("GET", "/v1/billing/me", nil), 200).json(t); me["plan"] != "free" || me["status"] != "canceled" {
		t.Fatalf("after cancel: %v", me)
	}
	if e.count(`SELECT count(*) FROM servers WHERE owner_user_id = $1 AND plan = 'free'`, uid) != 1 {
		t.Fatal("server plan not reset")
	}
	// After a cancellation a new checkout is allowed, reusing the customer.
	c.mustStatus(c.do("POST", "/v1/billing/checkout", map[string]string{"plan": "pro"}), 200)
	if p := e.pay.lastCheckout(); p.CustomerID != "cus_1" || p.CustomerEmail != "" {
		t.Fatalf("returning checkout: %+v", p)
	}

	// An event about a customer we never created is acknowledged.
	body, sig = signedEvent(testEvent{ID: "evt_foreign", Kind: "payment_succeeded", At: time.Now(), CustomerID: "cus_unknown", InvoiceID: "in_x"})
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)
	// An event kind the cloud does not act on is acknowledged.
	body, sig = signedEvent(testEvent{ID: "evt_other", Kind: "", At: time.Now()})
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)

	// A dispute suspends the payer and signs them out.
	body, sig = signedEvent(testEvent{ID: "evt_dispute", Kind: "payment_disputed", At: time.Now(), CustomerID: "cus_1", DisputeID: "dp_1"})
	hook.mustStatus(hook.webhook("testpay", body, sig), 200)
	if r := c.do("GET", "/v1/account/me", nil); r.code != 403 || r.errCode() != "account_suspended" {
		t.Fatalf("after dispute: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/v1/auth/refresh", nil); r.code != 401 && r.code != 403 {
		t.Fatalf("refresh after dispute: %d", r.code)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'billing.dispute_suspend' AND target_id = $1 AND actor_email = 'billing:testpay'`, uid) != 1 {
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

// Migration 0016 renames the provider-named billing columns and ledger
// without touching rows, adds the provider columns, and rolls back.
func TestMigrationBillingProviderNeutral(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	mig := db.NewMigrator(e.pool, migrations.FS, ".")
	cols := func(table string) map[string]bool {
		rows, err := e.pool.DB.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_name = $1`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]bool{}
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			out[c] = true
		}
		return out
	}
	want := map[string][]string{
		"subscriptions":   {"provider", "provider_customer_id", "provider_subscription_id", "provider_price_id"},
		"invoices":        {"provider", "provider_invoice_id"},
		"provider_events": {"provider", "provider_event_id", "type", "payload", "applied_at"},
	}
	for table, cs := range want {
		got := cols(table)
		for _, c := range cs {
			if !got[c] {
				t.Errorf("%s.%s missing after up: %v", table, c, got)
			}
		}
	}

	// A row written before the down/up round trip survives it.
	c := e.registerVerified("mig@example.test")
	_ = c
	uid := e.userID("mig@example.test")
	e.exec(`INSERT INTO subscriptions (user_id, provider, plan, status, provider_customer_id) VALUES ($1, 'testpay', 'pro', 'active', 'cus_m')`, uid)
	e.exec(`INSERT INTO provider_events (provider, provider_event_id, type, payload) VALUES ('testpay', 'evt_m', 't', '{}')`)
	// The same event id from two providers is two events.
	e.exec(`INSERT INTO provider_events (provider, provider_event_id, type, payload) VALUES ('otherpay', 'evt_m', 't', '{}')`)
	e.exec(`DELETE FROM provider_events WHERE provider = 'otherpay'`)

	// down to before 00160001: it and every migration after it (00510001 and on)
	if err := mig.Down(ctx, stepsDownTo(t, "00160001")); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := cols("provider_events"); len(got) != 0 {
		t.Fatalf("provider_events still there after down: %v", got)
	}
	if got := cols("subscriptions"); got["provider"] || got["provider_customer_id"] {
		t.Fatalf("subscriptions not rolled back: %v", got)
	}
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if e.count(`SELECT count(*) FROM subscriptions WHERE user_id = $1 AND provider_customer_id = 'cus_m' AND plan = 'pro'`, uid) != 1 {
		t.Fatal("subscription row lost in the round trip")
	}
	if e.count(`SELECT count(*) FROM provider_events WHERE provider_event_id = 'evt_m' AND provider = 'legacy'`) != 1 {
		t.Fatal("ledger row lost in the round trip (or not marked legacy)")
	}
}

// stepsDownTo is how many steps down undo migration version and every one after it.
func stepsDownTo(t *testing.T, version string) int {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, name := range names {
		if v, _, _ := strings.Cut(name, "_"); v >= version {
			n++
		}
	}
	if n == 0 {
		t.Fatalf("no migration %s", version)
	}
	return n
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
