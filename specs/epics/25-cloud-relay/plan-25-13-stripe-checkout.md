# Implementation Plan — Story 25.13 Stripe checkout session

> Companion to [story-25-13-stripe-checkout.md](story-25-13-stripe-checkout.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Library | `github.com/stripe/stripe-go/v76`. |
| Endpoints | `POST /api/billing/checkout`, `POST /api/billing/portal`, `POST /api/billing/upgrade-preview`, `GET /api/billing/plans`, `GET /api/billing/subscription`. Webhook (25.14) is separate. |
| Stripe customers | Created lazily on first checkout; `stripe_customer_id` persisted on `cloud_users` (column added in 25.2). Find-or-create under SELECT … FOR UPDATE row lock to prevent dual creation. |
| Idempotency | `Idempotency-Key: sha256(user_id || plan || YYYY-MM-DD)` for checkout-session creation; portal not idempotent (multiple URLs is harmless). |
| Apple IAP | Suppressed on iOS UA in v1 — returns `451 apple_iap_required`. Web/desktop/Android always succeed. |
| Out of scope | Webhook processing (25.14). Family invites flow UI (deferred). |

## 1. Migration `00040001_billing.sql` (slot 0004 per README)

```sql
-- +goose Up
CREATE TABLE cloud_subscriptions (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    stripe_subscription_id TEXT NOT NULL UNIQUE,
    stripe_customer_id     TEXT NOT NULL,
    plan                   TEXT NOT NULL,        -- 'pro_monthly' | 'pro_yearly' | 'family_monthly' | 'family_yearly'
    status                 TEXT NOT NULL,        -- 'active' | 'past_due' | 'canceled' | 'trialing'
    current_period_start   TIMESTAMPTZ NOT NULL,
    current_period_end     TIMESTAMPTZ NOT NULL,
    cancel_at              TIMESTAMPTZ,
    last_event_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    metadata               JSONB
);
CREATE UNIQUE INDEX cloud_subscriptions_active_user_uq
    ON cloud_subscriptions(user_id) WHERE status IN ('active','past_due','trialing');

CREATE TABLE cloud_invoices (
    stripe_invoice_id   TEXT PRIMARY KEY,
    user_id             UUID NOT NULL REFERENCES cloud_users(id) ON DELETE SET NULL,
    total_cents         BIGINT NOT NULL,
    currency            TEXT NOT NULL,
    status              TEXT NOT NULL,
    period_start        TIMESTAMPTZ,
    period_end          TIMESTAMPTZ,
    hosted_invoice_url  TEXT,
    pdf_url             TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE cloud_family_members (
    payer_user_id  UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    member_user_id UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    invited_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at    TIMESTAMPTZ,
    PRIMARY KEY (payer_user_id, member_user_id)
);

CREATE TABLE cloud_webhook_events (
    stripe_event_id   TEXT PRIMARY KEY,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    type              TEXT NOT NULL,
    processed_at      TIMESTAMPTZ,
    payload           JSONB NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS cloud_webhook_events, cloud_family_members, cloud_invoices, cloud_subscriptions;
```

(25.14 adds nothing — that story consumes this migration.)

## 2. Plan registry

```go
// cloud/internal/billing/plans.go
type PlanInfo struct {
    Key          billing.Plan
    StripePriceID string
    Display       string
    AmountCents   int
    Currency      string
    Interval      string   // "month" | "year"
}

var Plans = []PlanInfo{
    {Key: PlanProMonthly,    StripePriceID: cfg.PriceIDs.ProMonthly,   Display: "Pro",    AmountCents: 999,   Currency: "usd", Interval: "month"},
    {Key: PlanProYearly,     StripePriceID: cfg.PriceIDs.ProYearly,    Display: "Pro",    AmountCents: 9900,  Currency: "usd", Interval: "year"},
    {Key: PlanFamilyMonthly, StripePriceID: cfg.PriceIDs.FamilyMonthly, Display: "Family", AmountCents: 2499, Currency: "usd", Interval: "month"},
    {Key: PlanFamilyYearly,  StripePriceID: cfg.PriceIDs.FamilyYearly,  Display: "Family", AmountCents: 24900,Currency: "usd", Interval: "year"},
}
```

`GET /api/billing/plans` returns this list (price IDs stripped) for the marketing page.

## 3. Customer find-or-create

```go
// cloud/internal/billing/customer.go
func (s *Service) FindOrCreateCustomer(ctx context.Context, user *User) (string, error) {
    if user.StripeCustomerID != "" { return user.StripeCustomerID, nil }
    // Acquire row lock to avoid dual creation
    tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
    if err != nil { return "", err }
    defer tx.Rollback(ctx)
    var existing string
    err = tx.QueryRow(ctx, `SELECT stripe_customer_id FROM cloud_users WHERE id=$1 FOR UPDATE`, user.ID).Scan(&existing)
    if err != nil { return "", err }
    if existing != "" { tx.Commit(ctx); return existing, nil }
    cust, err := customer.New(&stripe.CustomerParams{
        Email: stripe.String(user.Email),
        Metadata: map[string]string{"maktaba_user_id": user.ID.String()},
    })
    if err != nil { return "", err }
    if _, err := tx.Exec(ctx, `UPDATE cloud_users SET stripe_customer_id=$1 WHERE id=$2`, cust.ID, user.ID); err != nil {
        return "", err
    }
    return cust.ID, tx.Commit(ctx)
}
```

## 4. Checkout

```go
// POST /api/billing/checkout
type checkoutReq struct {
    Plan      string `json:"plan"`
    PromoCode string `json:"promo_code"`
    ReturnURL string `json:"return_url"`
}

func checkout(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Apple guard
        if isIOSUA(r.Header.Get("User-Agent")) {
            problem(w, 451, "apple_iap_required", "iOS billing routes through web."); return
        }
        var req checkoutReq
        if err := decodeJSON(r, &req, 4<<10); err != nil { problem(w, 400, "bad_request", ""); return }
        plan, ok := lookupPlan(req.Plan)
        if !ok { problem(w, 400, "unknown_plan", ""); return }
        user, _ := s.repo.GetUser(r.Context(), currentUserID(r))
        sub, _ := s.repo.GetActiveSubscription(r.Context(), user.ID)
        if sub != nil && sub.Plan == plan.Key && sub.Status == "active" {
            problem(w, 409, "already_on_plan", "Use portal to change plan."); return
        }
        if err := validateReturnURL(req.ReturnURL); err != nil { problem(w, 400, "bad_redirect", ""); return }
        custID, err := s.FindOrCreateCustomer(r.Context(), user)
        if err != nil { problem(w, 502, "stripe_unavailable", ""); return }

        params := &stripe.CheckoutSessionParams{
            Customer: stripe.String(custID),
            Mode:     stripe.String(string(stripe.CheckoutSessionModeSubscription)),
            LineItems: []*stripe.CheckoutSessionLineItemParams{{
                Price: stripe.String(plan.StripePriceID),
                Quantity: stripe.Int64(1),
            }},
            SuccessURL: stripe.String(req.ReturnURL+"/billing/success?session_id={CHECKOUT_SESSION_ID}"),
            CancelURL:  stripe.String(req.ReturnURL+"/billing/canceled"),
            AllowPromotionCodes: stripe.Bool(true),
            AutomaticTax: &stripe.CheckoutSessionAutomaticTaxParams{Enabled: stripe.Bool(true)},
            TaxIDCollection: &stripe.CheckoutSessionTaxIDCollectionParams{Enabled: stripe.Bool(true)},
            Metadata: map[string]string{"maktaba_user_id": user.ID.String(), "plan": string(plan.Key)},
        }
        if req.PromoCode != "" {
            // Stripe expects promo_code via discounts; for simplicity rely on AllowPromotionCodes UI input.
        }
        idemKey := fmt.Sprintf("%x", sha256.Sum256([]byte(user.ID.String()+string(plan.Key)+time.Now().Format("2006-01-02"))))
        sess, err := session.New(params, stripe.IdempotencyKey(idemKey))
        if err != nil {
            problem(w, 502, "stripe_unavailable", ""); return
        }
        s.audit(r.Context(), "billing.checkout_created", sess.ID)
        writeJSON(w, 200, map[string]any{"url": sess.URL, "session_id": sess.ID})
    }
}
```

Retry: wrap `session.New` in a 3-attempt retry with exponential backoff on `502`, `503`, network errors. Idempotency key ensures duplicates are safe.

`validateReturnURL`: same allowlist as 25.3's `validateNext`.

## 5. Portal

```go
// POST /api/billing/portal
func portal(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        user, _ := s.repo.GetUser(r.Context(), currentUserID(r))
        if user.StripeCustomerID == "" { problem(w, 409, "no_customer", "Create a subscription first."); return }
        params := &stripe.BillingPortalSessionParams{
            Customer: stripe.String(user.StripeCustomerID),
            ReturnURL: stripe.String("https://app.maktaba.app/billing"),
        }
        sess, err := portalsession.New(params)
        if err != nil { problem(w, 502, "stripe_unavailable", ""); return }
        writeJSON(w, 200, map[string]string{"url": sess.URL})
    }
}
```

## 6. Upgrade preview

```go
// POST /api/billing/upgrade-preview body={plan:"family_monthly"}
func upgradePreview(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct{ Plan string }
        decodeJSON(r, &req, 1<<10)
        plan, ok := lookupPlan(req.Plan); if !ok { problem(w, 400, "unknown_plan", ""); return }
        user, _ := s.repo.GetUser(r.Context(), currentUserID(r))
        sub, _ := s.repo.GetActiveSubscription(r.Context(), user.ID)
        if sub == nil { problem(w, 409, "no_active_sub", ""); return }
        // Stripe invoice preview
        params := &stripe.InvoiceUpcomingParams{
            Customer: stripe.String(user.StripeCustomerID),
            Subscription: stripe.String(sub.StripeSubscriptionID),
            SubscriptionItems: []*stripe.SubscriptionItemsParams{{
                ID: stripe.String(sub.MainItemID),
                Price: stripe.String(plan.StripePriceID),
            }},
        }
        upcoming, err := invoice.GetUpcoming(params)
        if err != nil { problem(w, 502, "stripe_unavailable", ""); return }
        writeJSON(w, 200, map[string]any{
            "amount_due_now": upcoming.AmountDue,
            "next_amount":    upcoming.Total,
            "currency":       upcoming.Currency,
            "period_end":     upcoming.PeriodEnd,
        })
    }
}
```

## 7. Current subscription

```go
// GET /api/billing/subscription
func subscription(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        user, _ := s.repo.GetUser(r.Context(), currentUserID(r))
        sub, _ := s.repo.GetActiveSubscription(r.Context(), user.ID)
        if sub == nil {
            writeJSON(w, 200, map[string]any{"plan": "free", "status": "active"}); return
        }
        writeJSON(w, 200, map[string]any{
            "plan": sub.Plan, "status": sub.Status,
            "current_period_end": sub.CurrentPeriodEnd,
            "cancel_at": sub.CancelAt,
        })
    }
}
```

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestUnknownPlanRejected` | 400 unknown_plan. |
| `TestReturnURLAllowList` | `attacker.tld` → 400 bad_redirect. |
| `TestIsIOSUA` | `Mozilla/.../iPhone` → true; others false. |
| `TestIdempotencyKeyShape` | Deterministic key per user/plan/day. |
| `TestFindOrCreateCustomerLock` | Concurrent calls produce one customer. |

### 8.2 Integration (stripe-mock or `stripe listen` test fixture)

| Test | Pins |
|---|---|
| `TestNewCheckoutSession` | Returns URL; customer metadata set. |
| `TestReusedCustomer` | Existing `stripe_customer_id` → no duplicate. |
| `TestRapidDoubleClickIdempotent` | Two POSTs in 1s → same URL. |
| `TestStripeUnavailableRetries` | Inject 502 three times → 502 surfaced after retries. |
| `TestAlreadyOnPlan` | Pro user requests Pro → 409 with portal link. |
| `TestPortalURL` | Returns valid URL. |
| `TestiOSUACheckoutBlocked` | iOS UA → 451. |
| `TestUpgradePreviewProration` | Pro → Family preview shows proration line. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| iOS UA | 451; UI surfaces "manage on web" (25.15). | `TestiOSUACheckoutBlocked`. |
| Mid-flow currency | Stripe resolves at session creation; no toggle. | Spec. |
| No-email user (Apple relay) | Stripe customer email = relay address. | Spec. |
| Failed payment | Webhook downgrades (25.14). | Cross-story. |
| Stripe rate limit | Mid-cycle 100 RPS plenty for v1. | Doc. |
| return_url validation | 400 bad_redirect on attacker URL. | `TestReturnURLAllowList`. |
| B2B / VAT | Stripe collects VAT IDs; we link to invoice PDFs. | Spec. |
| Concurrent customer creation | Row lock + ON CONFLICT. | `TestFindOrCreateCustomerLock`. |
| Region availability | Stripe-side reject → propagate `region_unavailable`. | Spec. |
| Promo code invalid | Stripe surfaces at payment. | Spec. |
| Multiple subs per user | Unique partial index prevents. | Migration. |
| Customer email mismatch | We don't sync; Stripe owns billing email. | Spec. |

## 10. Dependencies

- 25.1, 25.2 (`cloud_users.stripe_customer_id`).
- 25.12 (consumes `cloud_subscriptions`).
- 25.14 (webhook does the actual state change after Stripe processes).

## 11. Acceptance checklist

- [ ] Migration 00040001 applies.
- [ ] `POST /api/billing/checkout` returns Stripe URL.
- [ ] `POST /api/billing/portal` returns portal URL.
- [ ] Customer find-or-create transactional + idempotent.
- [ ] iOS UA → 451.
- [ ] return_url validated.
- [ ] Tests in §8 pass.
