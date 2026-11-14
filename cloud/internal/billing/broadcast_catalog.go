package billing

import (
	"fmt"
	"strings"
)

// Story 50.20 §1: what a home may publish to people outside it. Every unit is infrastructure
// (channels carried, concurrent viewers served, viewer-hours delivered, the ladder's bandwidth);
// no price depends on what airs, and viewers never pay.

// Broadcast tier ids. servers.broadcast_plan holds BroadcastPlanBroadcaster on a home that bought
// the add-on; guest sharing is Pro's own and is never stored.
const (
	BroadcastGuest           = "guest"
	BroadcastPlanBroadcaster = "broadcaster"
)

// Viewer access a channel can have (viewers never pay).
const (
	AccessInvite = "invite"
	AccessOpen   = "open"
)

// ProviderBroadcastPrices are the payment provider's price ids for the add-on's items (§4),
// set per environment once the owner creates them. Empty: not created.
type ProviderBroadcastPrices struct {
	Broadcaster     string // item 4: the recurring flat price
	PAYGViewerHours string // item 5: the metered price broadcast_viewer_hours
	ExtraChannel    string // item 6
	ExtraViewers100 string // item 7
}

// BroadcastTier is what a home may publish. A pointer that is nil is a price the owner hasn't
// set (TBD): the broadcasting gate stays shut while one is (BroadcastLaunchCheck).
type BroadcastTier struct {
	ID          string   // BroadcastGuest (inside Pro), BroadcastPlanBroadcaster (add-on)
	MaxChannels int      // 1 / 3
	Access      []string // {"invite"} / {"invite","open"}
	// OnDemandOnly: channels start when a viewer comes and stop when the last leaves (guest).
	OnDemandOnly bool
	// MaxConcurrentViewers is per publisher, across its channels (5 / 100); extra 100-viewer
	// blocks add to it.
	MaxConcurrentViewers int
	ViewerHoursPerMonth  int // the allowance: 50 / 5000
	MaxRungs             int // 2 / 3
	MaxTopKbps           int // 3000 / 10000
	PAYG                 bool
	// FairUseMultiplier: past the allowance and the cap, new sessions get the lowest rung until
	// the month's viewing reaches this many allowances, then wait (0: none, wait at once).
	FairUseMultiplier float64
	// Prices in cents (nil: TBD). Guest sharing is included in Pro and has none.
	PriceCents             *int
	PAYGPricePer100HCents  *int
	ExtraChannelPriceCents *int
	ExtraViewers100Cents   *int
	ProviderPriceIDs       ProviderBroadcastPrices
}

// AllowanceMinutes is the allowance in viewer-minutes, the unit the edge counts.
func (b *BroadcastTier) AllowanceMinutes() int64 { return int64(b.ViewerHoursPerMonth) * 60 }

// FairUseMinutes is the ceiling of lowest-rung viewing (0: none).
func (b *BroadcastTier) FairUseMinutes() int64 {
	return int64(float64(b.AllowanceMinutes()) * b.FairUseMultiplier)
}

// AllowsAccess says whether a channel may have this access.
func (b *BroadcastTier) AllowsAccess(access string) bool {
	for _, a := range b.Access {
		if a == access {
			return true
		}
	}
	return false
}

// GuestSharing is Pro's (README §14.10 question 5): one invite-only channel on demand, 5
// viewers at once, 50 viewer-hours a month, then new viewers wait.
var GuestSharing = BroadcastTier{
	ID: BroadcastGuest, MaxChannels: 1, Access: []string{AccessInvite}, OnDemandOnly: true,
	MaxConcurrentViewers: 5, ViewerHoursPerMonth: 50, MaxRungs: 2, MaxTopKbps: 3000,
}

// Broadcaster is the add-on to Pro (README §14.10 questions 1–4, 6, 7): 3 channels, 100 viewers
// at once, 5,000 viewer-hours a month, up to 1080p in 3 rungs; then pay-as-you-go up to the
// owner's cap, then the lowest rung up to 2× the allowance, then new viewers wait. Its prices
// are the owner's and TBD.
var Broadcaster = BroadcastTier{
	ID: BroadcastPlanBroadcaster, MaxChannels: 3, Access: []string{AccessInvite, AccessOpen},
	MaxConcurrentViewers: 100, ViewerHoursPerMonth: 5000, MaxRungs: 3, MaxTopKbps: 10000,
	PAYG: true, FairUseMultiplier: 2.0,
}

// BroadcastFor is the tier a home on plan with broadcastPlan (servers.broadcast_plan) may
// publish under, nil for none. Broadcaster needs Pro: on any other plan the add-on grants
// nothing (dropping Pro drops it).
func BroadcastFor(plan, broadcastPlan string) *BroadcastTier {
	t, ok := Tiers[plan]
	if !ok || t.Broadcast == nil {
		return nil
	}
	if broadcastPlan == BroadcastPlanBroadcaster && plan == PlanPro {
		b := Broadcaster
		return &b
	}
	g := *t.Broadcast
	return &g
}

// Limits are a publisher's tier with the add-on's extra units bought.
type Limits struct {
	MaxChannels          int `json:"max_channels"`
	MaxConcurrentViewers int `json:"max_concurrent_viewers"`
}

// LimitsWith adds extra channels and extra 100-viewer blocks (items 6 and 7, Broadcaster only).
func (b *BroadcastTier) LimitsWith(extraChannels, extraViewers100 int) Limits {
	l := Limits{MaxChannels: b.MaxChannels, MaxConcurrentViewers: b.MaxConcurrentViewers}
	if b.ID == BroadcastPlanBroadcaster {
		l.MaxChannels += max(extraChannels, 0)
		l.MaxConcurrentViewers += 100 * max(extraViewers100, 0)
	}
	return l
}

// BroadcastLaunchCheck is the broadcasting gate's check (50.19 T12's launch switch extended to
// the broadcast catalog): broadcasting can't be switched on while any broadcast value is TBD.
// It names each one.
func BroadcastLaunchCheck(tiers map[string]Tier, addOn BroadcastTier) error {
	var missing []string
	pro, ok := tiers[PlanPro]
	if !ok || pro.Broadcast == nil {
		missing = append(missing, "pro.broadcast")
	} else if pro.PriceCents == nil {
		missing = append(missing, "pro.price") // guest sharing is sold inside Pro
	}
	if addOn.PriceCents == nil {
		missing = append(missing, "broadcaster.price")
	}
	if addOn.PAYG && addOn.PAYGPricePer100HCents == nil {
		missing = append(missing, "broadcaster.payg_price_per_100h")
	}
	if addOn.ExtraChannelPriceCents == nil {
		missing = append(missing, "broadcaster.extra_channel_price")
	}
	if addOn.ExtraViewers100Cents == nil {
		missing = append(missing, "broadcaster.extra_viewers_100_price")
	}
	if len(missing) > 0 {
		return fmt.Errorf("billing: broadcasting can't be switched on: %s TBD", strings.Join(missing, ", "))
	}
	return nil
}
