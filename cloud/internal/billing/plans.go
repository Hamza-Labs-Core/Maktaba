// Package billing owns the plan catalog, relay metering, and the payment-provider seam
// (Provider; no provider is chosen yet). The catalog is hard-coded here (not in the DB) so the
// source of truth lives in version control and a plan change ships as a PR.
//
// Epic 50 story 50.19: three plans. Only the relay is paid, and a paid plan never cuts remote
// play off. Free has no relay (direct remote access is free on every plan, story 50.15); Basic
// relays at capped quality on 2 streams with 500 GB of soft fair use; Pro relays at full quality
// for an allowance, then pay-as-you-go up to the owner's cap, then Basic's capped quality.
package billing

import (
	"fmt"
	"strings"
	"time"
)

// Plan identifiers used across the cloud, stored on users.plan, servers.plan and
// subscriptions.plan. The plans before story 50.19 were free / pro / family; migration
// 00500006 moved pro to basic and family to pro.
const (
	PlanFree  = "free"
	PlanBasic = "basic"
	PlanPro   = "pro"
)

// Plans is the catalog's order (cheapest first).
var Plans = []string{PlanFree, PlanBasic, PlanPro}

// GB is a decimal gigabyte, the unit plans are sold in.
const GB = int64(1_000_000_000)

// Tier is what a plan grants. A pointer that is nil is a value the owner hasn't set yet (TBD):
// paid plans can't be switched on while one is (LaunchCheck).
type Tier struct {
	ID   string
	Name string
	// Relay: the home is reached through the relay (false on Free).
	Relay bool
	// RelayCapKbps is the relayed stream's cap (3000 on Basic); 0 is full quality (Pro).
	RelayCapKbps int
	// RelaySlowCapKbps is the cap past fair use (Basic) or past the allowance, the
	// pay-as-you-go cap and fair use in capped mode (Pro): half the cap.
	RelaySlowCapKbps int
	// MaxRelayStreams is how many relayed streams may play at once (2 on Basic, N on Pro;
	// nil: TBD). Pro's extra streams add to it.
	MaxRelayStreams *int
	// FairUseBytesPerMo is the soft fair use in capped mode (500 GB): past it, slower, never
	// stopped.
	FairUseBytesPerMo int64
	// AllowanceBytesPerMo is Pro's full-quality allowance (nil: TBD).
	AllowanceBytesPerMo *int64
	// PayAsYouGo and ExtraStreamsAddOn are Pro's options.
	PayAsYouGo        bool
	ExtraStreamsAddOn bool
	// Prices in cents a month (nil: TBD); Free's is 0.
	PriceCents            *int
	PAYGPricePerGBCents   *int
	ExtraStreamPriceCents *int
	// MaxServers is how many homes an account links (values set with the prices).
	MaxServers int
}

func intp(v int) *int { return &v }

// Tiers indexed by plan id. The numbers the owner hasn't set (the paid plans' prices, Pro's
// allowance and stream count, a pay-as-you-go GB's and an extra stream's price) are nil.
var Tiers = map[string]Tier{
	PlanFree: {
		ID: PlanFree, Name: "Free", Relay: false,
		PriceCents: intp(0), MaxServers: 1,
	},
	PlanBasic: {
		ID: PlanBasic, Name: "Basic", Relay: true,
		RelayCapKbps: 3000, RelaySlowCapKbps: 1500, MaxRelayStreams: intp(2),
		FairUseBytesPerMo: 500 * GB, MaxServers: 3,
	},
	PlanPro: {
		ID: PlanPro, Name: "Pro", Relay: true,
		RelayCapKbps: 0, RelaySlowCapKbps: 1500,
		FairUseBytesPerMo: 500 * GB, PayAsYouGo: true, ExtraStreamsAddOn: true, MaxServers: 5,
	},
}

// RelayIncluded says whether a plan reaches its home through the relay. An unknown plan keeps
// the relay (a plan this build doesn't know is never cut off by it). Story 50.13: a home whose
// plan has no relay lists no relay connections.
func RelayIncluded(plan string) bool {
	t, ok := Tiers[plan]
	return !ok || t.Relay
}

// LaunchCheck is the launch gate's check (story 50.19 T12): paid plans can't be switched on
// while any of their values is TBD. It names each one.
func LaunchCheck(tiers map[string]Tier) error {
	var missing []string
	for _, id := range Plans {
		t, ok := tiers[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		if id == PlanFree {
			continue
		}
		if t.PriceCents == nil {
			missing = append(missing, id+".price")
		}
		if t.MaxRelayStreams == nil {
			missing = append(missing, id+".max_relay_streams")
		}
		if id == PlanPro {
			if t.AllowanceBytesPerMo == nil {
				missing = append(missing, id+".allowance")
			}
			if t.PayAsYouGo && t.PAYGPricePerGBCents == nil {
				missing = append(missing, id+".payg_price_per_gb")
			}
			if t.ExtraStreamsAddOn && t.ExtraStreamPriceCents == nil {
				missing = append(missing, id+".extra_stream_price")
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("billing: paid plans can't be switched on: %s TBD", strings.Join(missing, ", "))
	}
	return nil
}

// MonthStart returns the first day of `t`'s month at 00:00 UTC, the boundary of the usage
// months.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
