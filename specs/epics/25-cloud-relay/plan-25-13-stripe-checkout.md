# Implementation Plan — Story 25.13 Stripe checkout session

> Companion to [story-25-13-stripe-checkout.md](story-25-13-stripe-checkout.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Library | `github.com/stripe/stripe-go/v76`. |
| Endpoints | `POST /api/billing/checkout`, `POST /api/billing/portal`, `POST /api/billing/upgrade-preview`, `GET /api/billing/plans`, `GET /api/billing/subscription`. Webhook (25.14) is separate. |
| Stripe customers | Created lazily on first checkout. `stripe_customer_id` lives on the `subscriptions` table only (single source of truth); we **do not** denormalize it onto `users`. Find-or-create resolves via `SELECT stripe_customer_id FROM subscriptions WHERE user_id=$1 FOR UPDATE`. Missing-row case inserts a `subscriptions` row with `plan='free', status='inactive'` carrying the new customer id. |
| Tier vocabulary | Canonical: `tier IN ('free','pro','family')` and `interval IN ('monthly','yearly')`. Stripe price IDs are matched on the `(tier, interval)` pair in config; nothing flattens this into a single string. Matches `architecture.md` §13.10. |
| Idempotency | `Idempotency-Key: sha256(user_id || tier || interval || YYYY-MM-DD)` for checkout-session creation; portal not idempotent (multiple URLs is harmless). |
| Apple IAP | Suppressed on iOS UA in v1 — returns `451 apple_iap_required`. Web/desktop/Android always succeed. |
| Out of scope | Webhook processing (25.14). Family invites flow UI (deferred). |

## 1. Migration `00060001_billing.sql` (slot 0006 per README)

```sql
-- +goose Up
CREATE TABLE subscriptions (
    user_id                UUID         PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    tier                   TEXT         NOT NULL DEFAULT 'free' CHECK (tier IN ('free','pro','family')),
    interval               TEXT         CHECK (interval IS NULL OR interval IN ('monthly','yearly')),
    stripe_customer_id     TEXT         UNIQUE,                  -- single source of truth
    stripe_subscription_id TEXT         UNIQUE,
    status                 TEXT         NOT NULL DEFAULT 'inactive', -- 'active' | 'past_due' | 'canceled' | 'trialing' | 'inactive'
    current_period_start   TIMESTAMPTZ,
    current_period_end     TIMESTAMPTZ,
    cancel_at_period_end   BOOLEAN      NOT NULL DEFAULT FALSE,
    cancel_at              TIMESTAMPTZ,
    seats                  INTEGER      NOT NULL DEFAULT 1,
    last_event_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    metadata               JSONB,
    updated_at             TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX subscriptions_status_idx ON subscriptions(status) WHERE status IN ('active','past_due','trialing');

CREATE TABLE invoices (
    stripe_invoice_id   TEXT PRIMARY KEY,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE SET NULL,
    total_cents         BIGINT NOT NULL,
    currency            TEXT NOT NULL,
    status              TEXT NOT NULL,
    period_start        TIMESTAMPTZ,
    period_end          TIMESTAMPTZ,
    hosted_invoice_url  TEXT,
    pdf_url             TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE family_members (
    payer_user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invited_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at    TIMESTAMPTZ,
    PRIMARY KEY (payer_user_id, member_user_id)
);
CREATE INDEX family_members_member_idx ON family_members(member_user_id);

CREATE TABLE stripe_events (
    event_id          TEXT PRIMARY KEY,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    type              TEXT NOT NULL,
    processed_at      TIMESTAMPTZ,
    payload           JSONB NOT NULL
);

-- Webhook-secret rotation. plan-25-14 reads `Active(ctx)` against this
-- table; at most two rows have `retired_at IS NULL` (current + last).
CREATE TABLE stripe_secrets (
    id            SERIAL PRIMARY KEY,
    secret_sealed BYTEA NOT NULL,
    active_from   TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX stripe_secrets_one_active_idx ON stripe_secrets((1)) WHERE retired_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS stripe_secrets, stripe_events, family_members, invoices, subscriptions;
```

(25.14 adds nothing — that story consumes this migration.)

## 2. Plan registry

```go
// cloud/internal/billing/plans.go
const (
    TierFree   = "free"
    TierPro    = "pro"
    TierFamily = "family"

    IntervalMonthly = "monthly"
    IntervalYearly  = "yearly"
)

type PlanInfo struct {
    Tier          string  // 'free' | 'pro' | 'family'
    Interval      string  // 'monthly' | 'yearly' (empty for free)
    StripePriceID string
    Display       string
    AmountCents   int
    Currency      string
}

var Plans = []PlanInfo{
    {Tier: TierPro,    Interval: IntervalMonthly, StripePriceID: cfg.PriceIDs.ProMonthly,    Display: "Pro",    AmountCents: 999,   Currency: "usd"},
    {Tier: TierPro,    Interval: IntervalYearly,  StripePriceID: cfg.PriceIDs.ProYearly,     Display: "Pro",    AmountCents: 9900,  Currency: "usd"},
    {Tier: TierFamily, Interval: IntervalMonthly, StripePriceID: cfg.PriceIDs.FamilyMonthly, Display: "Family", AmountCents: 2499,  Currency: "usd"},
    {Tier: TierFamily, Interval: IntervalYearly,  StripePriceID: cfg.PriceIDs.FamilyYearly,  Display: "Family", AmountCents: 24900, Currency: "usd"},
}
```

`GET /api/billing/plans` returns this list (price IDs stripped) for
the marketing page. Checkout body is `{tier, interval}`; the handler
rejects unknown combinations with `400 unknown_plan`.

## 3. Customer find-or-create

```go
// cloud/internal/billing/customer.go
//
// stripe_customer_id is owned by the `subscriptions` row. The first
// checkout for a user inserts the row (tier='free', status='inactive')
// carrying the customer id; subsequent state transitions UPDATE the
// same row in place. There is exactly one row per user.
func (s *Service) FindOrCreateCustomer(ctx context.Context, userID uuid.UUID, email string) (string, error) {
    tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
    if err != nil { return "", err }
    defer tx.Rollback(ctx)
    var existing sql.NullString
    err = tx.QueryRow(ctx,
        `SELECT stripe_customer_id FROM subscriptions WHERE user_id=$1 FOR UPDATE`,
        userID,
    ).Scan(&existing)
    switch {
    case errors.Is(err, pgx.ErrNoRows):
        // first checkout — fall through, will insert below
    case err != nil:
        return "", err
    case existing.Valid && existing.String != "":
        return existing.String, tx.Commit(ctx)
    }
    cust, err := customer.New(&stripe.CustomerParams{
        Email: stripe.String(email),
        Metadata: map[string]string{"maktaba_user_id": userID.String()},
    })
    if err != nil { return "", err }
    _, err = tx.Exec(ctx, `
        INSERT INTO subscriptions (user_id, tier, status, stripe_customer_id)
        VALUES ($1, 'free', 'inactive', $2)
        ON CONFLICT (user_id) DO UPDATE
           SET stripe_customer_id = EXCLUDED.stripe_customer_id,
               updated_at = now()`,
        userID, cust.ID,
    )
    if err != nil { return "", err }
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
// POST /api/billing/upgrade-preview body={tier:"family",interval:"monthly"}
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

- 25.1, 25.2 (`users` table).
- 25.12 (consumes `subscriptions`).
- 25.14 (Stripe webhook updates the same `subscriptions` row in place; pairs with this story).
- 25.14 (webhook does the actual state change after Stripe processes).

## 11. Acceptance checklist

- [ ] Migration 00060001 (billing) applies.
- [ ] `POST /api/billing/checkout` returns Stripe URL.
- [ ] `POST /api/billing/portal` returns portal URL.
- [ ] Customer find-or-create transactional + idempotent.
- [ ] iOS UA → 451.
- [ ] return_url validated.
- [ ] Tests in §8 pass.
