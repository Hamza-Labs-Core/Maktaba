package billing

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestNewProvider(t *testing.T) {
	for _, name := range []string{"", ProviderNone} {
		p, err := New(name)
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		if p.Name() != ProviderNone {
			t.Errorf("New(%q).Name() = %q", name, p.Name())
		}
	}
	for _, name := range []string{"unknown", ProviderManual, "None"} {
		if _, err := New(name); err == nil {
			t.Errorf("New(%q): an unknown provider was accepted", name)
		}
	}
}

// The none provider takes no payments: nothing is sold and every
// payment call answers ErrNotConfigured.
func TestNoneProvider(t *testing.T) {
	var p Provider = None{}
	if p.Enabled() {
		t.Error("none is enabled")
	}
	for _, plan := range []string{PlanFree, PlanPro, PlanFamily} {
		if p.Sells(plan) {
			t.Errorf("none sells %s", plan)
		}
	}
	ctx := context.Background()
	if _, err := p.CreateCheckout(ctx, CheckoutParams{Plan: PlanPro, UserID: "u"}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("checkout: %v", err)
	}
	if _, err := p.CreatePortal(ctx, "c", "https://x.test"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("portal: %v", err)
	}
	if _, err := p.ParseWebhook(ctx, []byte(`{}`), http.Header{}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("webhook: %v", err)
	}
}
