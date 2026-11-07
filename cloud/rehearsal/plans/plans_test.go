package plans

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
)

// The rehearsal's plans have every number the launch gate wants, and Pro's allowance runs out.
func TestApplyFillsEveryTBD(t *testing.T) {
	old := billing.Tiers
	t.Cleanup(func() { billing.Tiers = old })
	if billing.LaunchCheck(billing.Tiers) == nil {
		t.Skip("the catalog has no TBD left: this package can go")
	}
	Apply()
	if err := billing.LaunchCheck(billing.Tiers); err != nil {
		t.Fatal(err)
	}
	if d := billing.ModeFor(billing.PlanPro, billing.Usage{Full: AllowanceBytes}, billing.PAYG{}); d.Mode != billing.ModeCapped {
		t.Fatalf("pro past the allowance: %+v", d)
	}
	if old[billing.PlanPro].AllowanceBytesPerMo != nil {
		t.Fatal("Apply changed the catalog it copied")
	}
}
