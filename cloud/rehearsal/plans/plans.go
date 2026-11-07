// Package plans gives the rehearsal's paid plans the numbers not set yet (Epic 50 story 50.19,
// scenario relay-plans): Pro's allowance and stream count and the prices are TBD in the catalog,
// and a TBD allowance never runs out, so the relay under test (built with the rehearsalplans tag)
// and the scenario's own reporter both take these. Never in a release: only
// tools/rehearsal-cloud/Dockerfile's BUILD_TAGS=rehearsalplans build links it.
package plans

import "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"

// The rehearsal's numbers: a Pro home past AllowanceBytes falls back to Basic's quality, unless
// its pay-as-you-go is on and under its cap.
const (
	AllowanceBytes   = 100 * billing.GB
	ProStreams       = 4
	PAYGCentsPerGB   = 10
	ExtraStreamCents = 300
)

// Apply fills the catalog's TBD values with the rehearsal's numbers.
func Apply() {
	tiers := make(map[string]billing.Tier, len(billing.Tiers))
	for k, v := range billing.Tiers {
		tiers[k] = v
	}
	basic := tiers[billing.PlanBasic]
	basic.PriceCents = intp(500)
	tiers[billing.PlanBasic] = basic
	pro := tiers[billing.PlanPro]
	allowance := AllowanceBytes
	pro.AllowanceBytesPerMo = &allowance
	pro.MaxRelayStreams = intp(ProStreams)
	pro.PriceCents = intp(1000)
	pro.PAYGPricePerGBCents = intp(PAYGCentsPerGB)
	pro.ExtraStreamPriceCents = intp(ExtraStreamCents)
	tiers[billing.PlanPro] = pro
	billing.Tiers = tiers
}

func intp(v int) *int { return &v }
