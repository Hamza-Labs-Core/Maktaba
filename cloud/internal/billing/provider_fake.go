package billing

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Fake is a payment provider for tests and the rehearsal (story 50.19): it records usage
// reports by ID (a repeated ID is taken once, as a provider's idempotency key does), the items
// set on each subscription, and can be made unreachable. It never contacts anything.
type Fake struct {
	mu      sync.Mutex
	reports map[string]UsageReport
	items   map[string]FakeItems
	// story 50.20: broadcast items by subscription and home, and viewer-hour reports by ID
	bitems   map[string]BroadcastItems
	breports map[string]BroadcastUsageReport
	// Down makes every call fail as an unreachable provider would.
	Down bool
}

// FakeItems are a subscription's items 2 and 3.
type FakeItems struct {
	PAYG         bool
	ExtraStreams int
}

// ErrFakeDown is the fake's outage.
var ErrFakeDown = errors.New("billing: the payment provider is unreachable")

func (*Fake) Name() string      { return "fake" }
func (*Fake) Enabled() bool     { return true }
func (*Fake) Sells(string) bool { return true }
func (*Fake) CreateCheckout(context.Context, CheckoutParams) (CheckoutSession, error) {
	return CheckoutSession{ID: "cs_fake", URL: "https://pay.invalid/checkout"}, nil
}
func (*Fake) CreatePortal(context.Context, string, string) (string, error) {
	return "https://pay.invalid/portal", nil
}
func (*Fake) ParseWebhook(context.Context, []byte, http.Header) (Event, error) {
	return Event{}, ErrBadSignature
}

func (f *Fake) ReportUsage(_ context.Context, r UsageReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return ErrFakeDown
	}
	if f.reports == nil {
		f.reports = map[string]UsageReport{}
	}
	if _, seen := f.reports[r.ID]; !seen {
		f.reports[r.ID] = r
	}
	return nil
}

func (f *Fake) SetItems(_ context.Context, sub string, payg bool, extra int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return ErrFakeDown
	}
	if f.items == nil {
		f.items = map[string]FakeItems{}
	}
	f.items[sub] = FakeItems{PAYG: payg, ExtraStreams: extra}
	return nil
}

func (f *Fake) ReportedUsage(_ context.Context, sub string, month time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return 0, ErrFakeDown
	}
	var gb int64
	for _, r := range f.reports {
		if r.SubscriptionID == sub && MonthStart(r.At).Equal(MonthStart(month)) {
			gb += r.GB
		}
	}
	return gb, nil
}

// Reports are the reports taken, by ID.
func (f *Fake) Reports() map[string]UsageReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]UsageReport, len(f.reports))
	for k, v := range f.reports {
		out[k] = v
	}
	return out
}

// Items are a subscription's items as last set.
func (f *Fake) Items(sub string) FakeItems {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[sub]
}

func (f *Fake) SetBroadcastItems(_ context.Context, sub string, it BroadcastItems) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return ErrFakeDown
	}
	if f.bitems == nil {
		f.bitems = map[string]BroadcastItems{}
	}
	f.bitems[sub+"/"+it.ServerID] = it
	return nil
}

func (f *Fake) ReportBroadcastUsage(_ context.Context, r BroadcastUsageReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return ErrFakeDown
	}
	if f.breports == nil {
		f.breports = map[string]BroadcastUsageReport{}
	}
	if _, seen := f.breports[r.ID]; !seen {
		f.breports[r.ID] = r
	}
	return nil
}

func (f *Fake) ReportedBroadcastUsage(_ context.Context, sub, server string, month time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return 0, ErrFakeDown
	}
	var h int64
	for _, r := range f.breports {
		if r.SubscriptionID == sub && r.ServerID == server && MonthStart(r.At).Equal(MonthStart(month)) {
			h += r.Hours
		}
	}
	return h, nil
}

// BroadcastReports are the viewer-hour reports taken, by ID.
func (f *Fake) BroadcastReports() map[string]BroadcastUsageReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]BroadcastUsageReport, len(f.breports))
	for k, v := range f.breports {
		out[k] = v
	}
	return out
}

// BroadcastItemsOf are a home's broadcast items on a subscription as last set.
func (f *Fake) BroadcastItemsOf(sub, server string) BroadcastItems {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bitems[sub+"/"+server]
}
