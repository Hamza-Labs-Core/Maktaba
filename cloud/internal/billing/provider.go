package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Provider is the seam between the cloud and a payment provider. The
// cloud never talks to a provider's API directly: the billing handlers
// open checkouts and portals through it, and hand it each webhook to
// verify and translate into a provider-neutral Event.
//
// The payment provider is not chosen yet. The only provider that ships
// is "none" (the default): paid plans show as coming soon, checkout and
// the portal answer that payments are not set up, the webhook route is
// a 404, and service admins set plans by hand.
type Provider interface {
	// Name is the provider's id: the [billing].provider config value,
	// the {provider} segment of /v1/billing/webhook/{provider}, and the
	// `provider` column on the billing tables.
	Name() string
	// Enabled reports whether the provider can take payments at all.
	Enabled() bool
	// Sells reports whether a plan can be bought through the provider.
	Sells(plan string) bool
	// CreateCheckout opens a hosted checkout for a subscription.
	CreateCheckout(ctx context.Context, p CheckoutParams) (CheckoutSession, error)
	// CreatePortal opens the provider's customer portal, where a payer
	// changes plan, payment method, or cancels.
	CreatePortal(ctx context.Context, customerID, returnURL string) (string, error)
	// ParseWebhook verifies a webhook delivery (signature, freshness)
	// and translates it. A delivery that fails verification returns an
	// error wrapping ErrBadSignature. An event the cloud does not act on
	// comes back with an empty Kind and is acknowledged without work.
	ParseWebhook(ctx context.Context, payload []byte, header http.Header) (Event, error)

	// Story 50.19 §6: the subscription's other items. ReportUsage sends a usage report for
	// the metered pay-as-you-go GB (item 2); a report whose ID the provider already has is
	// taken once (the ID is its idempotency key). SetItems adds or removes item 2 and sets
	// item 3's quantity (extra streams, prorated); the webhook then confirms. ReportedUsage
	// is the GB the provider recorded for a subscription in a month (the daily reconciliation).
	ReportUsage(ctx context.Context, r UsageReport) error
	SetItems(ctx context.Context, subscriptionID string, payg bool, extraStreams int) error
	ReportedUsage(ctx context.Context, subscriptionID string, month time.Time) (int64, error)
}

// UsageReport is one hour of a home's pay-as-you-go GB (story 50.19 §6).
type UsageReport struct {
	// ID is <server_id>:<month>:<hour>: the provider's idempotency key, the same on a retry.
	ID             string
	SubscriptionID string
	CustomerID     string
	GB             int64
	At             time.Time
}

// ErrNotConfigured is returned by every payment call when no payment
// provider is set up.
var ErrNotConfigured = errors.New("billing: payments are not set up")

// ErrBadSignature marks a webhook delivery that failed verification.
var ErrBadSignature = errors.New("billing: webhook signature is not valid")

// CheckoutSession is what a checkout returns: the page to send the
// payer to, and the provider's id for the session.
type CheckoutSession struct {
	ID  string
	URL string
}

// CheckoutParams describes a subscription checkout.
type CheckoutParams struct {
	Plan string
	// Exactly one of CustomerID (a returning payer) or CustomerEmail.
	CustomerID    string
	CustomerEmail string
	SuccessURL    string
	CancelURL     string
	// UserID is our account id. A provider stamps it on the session and
	// the subscription, so every later event names the account even
	// before the provider's customer id is known.
	UserID string
}

// EventKind is what a webhook event means to the cloud.
type EventKind string

const (
	// EventCheckoutCompleted links a payer (customer id) to an account.
	EventCheckoutCompleted EventKind = "checkout_completed"
	// EventSubscriptionStarted, Changed and Cancelled carry the
	// subscription's plan, status and period.
	EventSubscriptionStarted   EventKind = "subscription_started"
	EventSubscriptionChanged   EventKind = "subscription_changed"
	EventSubscriptionCancelled EventKind = "subscription_cancelled"
	// EventPaymentSucceeded and EventPaymentFailed are invoice outcomes.
	EventPaymentSucceeded EventKind = "payment_succeeded"
	EventPaymentFailed    EventKind = "payment_failed"
	// EventPaymentDisputed is a chargeback; the payer is suspended.
	EventPaymentDisputed EventKind = "payment_disputed"
)

// Event is a provider-neutral webhook event.
type Event struct {
	// ID is the provider's event id: the idempotency key.
	ID string
	// Type is the provider's own event type, kept in the ledger.
	Type string
	Kind EventKind
	// At is when the provider says the event happened; events older
	// than the last one applied to a subscription are ignored.
	At time.Time

	UserID         string // our account id, when the provider carries it
	CustomerID     string
	SubscriptionID string

	// Subscription events.
	Plan              string // PlanFree / PlanBasic / PlanPro ("" = free)
	PriceID           string
	Interval          string // "monthly" / "yearly" / ""
	Status            string // active, trialing, past_due, canceled, incomplete, ...
	PeriodEnd         time.Time
	CancelAtPeriodEnd bool
	// Story 50.19 §6: whether item 2 (pay-as-you-go) is on the subscription, and item 3's
	// quantity (extra streams).
	PAYG         bool
	ExtraStreams int

	// Payment events.
	InvoiceID   string
	AmountCents int64
	Currency    string

	// Dispute events.
	DisputeID string

	// Payload is the delivery body, kept in the event ledger (JSON).
	Payload []byte
}

// ProviderNone is the provider that takes no payments.
const ProviderNone = "none"

// ProviderManual marks a subscription a service admin set by hand.
const ProviderManual = "manual"

// New returns the provider configured by name. "" means "none".
func New(name string) (Provider, error) {
	switch name {
	case "", ProviderNone:
		return None{}, nil
	default:
		return nil, fmt.Errorf("billing: unknown payment provider %q", name)
	}
}

// None is the default provider: no payments. Paid plans are not sold,
// every payment call returns ErrNotConfigured, and no webhook is
// accepted. Service admins set plans by hand.
type None struct{}

func (None) Name() string      { return ProviderNone }
func (None) Enabled() bool     { return false }
func (None) Sells(string) bool { return false }

func (None) CreateCheckout(context.Context, CheckoutParams) (CheckoutSession, error) {
	return CheckoutSession{}, ErrNotConfigured
}

func (None) CreatePortal(context.Context, string, string) (string, error) {
	return "", ErrNotConfigured
}

func (None) ParseWebhook(context.Context, []byte, http.Header) (Event, error) {
	return Event{}, ErrNotConfigured
}

func (None) ReportUsage(context.Context, UsageReport) error { return ErrNotConfigured }

func (None) SetItems(context.Context, string, bool, int) error { return ErrNotConfigured }

func (None) ReportedUsage(context.Context, string, time.Time) (int64, error) {
	return 0, ErrNotConfigured
}
