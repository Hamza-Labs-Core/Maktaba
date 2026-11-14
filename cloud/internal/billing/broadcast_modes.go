package billing

// Broadcast modes (story 50.20 §2): what the edge does for a NEW viewer session. A playing
// session keeps the mode it started in until it ends; only BroadcastOff stops playing sessions.
const (
	// BroadcastFull: every rung the channel publishes.
	BroadcastFull = "full"
	// BroadcastLowest: the master playlist lists only the lowest rung (Broadcaster past its
	// allowance and its pay-as-you-go cap, below the fair-use ceiling).
	BroadcastLowest = "lowest"
	// BroadcastWait: 429 broadcast_full for new sessions (past the ceiling, guest sharing past
	// its allowance, or at the concurrent-viewer limit).
	BroadcastWait = "wait"
	// BroadcastOff: no plan, a kill-switch entry or a takedown; 403/451, playing sessions stop
	// at their next playlist.
	BroadcastOff = "off"
)

// BroadcastUsage is a publisher's viewer-minutes this month by how they were counted. Full is
// within the allowance (plus any overshoot past the cap, never billed), PAYG is bought.
type BroadcastUsage struct {
	MinutesFull   int64 `json:"minutes_full"`
	MinutesLowest int64 `json:"minutes_lowest"`
	MinutesPAYG   int64 `json:"minutes_payg"`
}

// Total is every viewer-minute.
func (u BroadcastUsage) Total() int64 { return u.MinutesFull + u.MinutesLowest + u.MinutesPAYG }

// BroadcastPAYG is a publisher's pay-as-you-go: on with a cap in cents, what this month's
// pay-as-you-go viewer-minutes cost so far, and those minutes against the cap's worth.
type BroadcastPAYG struct {
	On         bool  `json:"on"`
	CapCents   int64 `json:"cap_cents"`
	SpentCents int64 `json:"spent_cents"`
	Minutes    int64 `json:"minutes"`
	CapMinutes int64 `json:"cap_minutes"`
}

// HasRoom says whether pay-as-you-go may buy more viewing now (counted in minutes, so rounding a
// cost down to a whole cent never leaves a cap that can't be reached).
func (p BroadcastPAYG) HasRoom() bool { return p.On && p.CapMinutes > p.Minutes }

// BroadcastInput is what the mode for a new session is decided from.
type BroadcastInput struct {
	Tier *BroadcastTier // nil: no plan
	// Off: a kill-switch entry (50.25), a takedown (50.24), a suspended account, or the
	// broadcasting gate shut for this publisher.
	Off   bool
	Usage BroadcastUsage
	PAYG  BroadcastPAYG
	// Concurrent is the publisher's sessions with a heartbeat in the last 150 s (the edge's
	// count; 0 when the cloud decides), MaxConcurrent its limit with extra viewers bought.
	Concurrent    int
	MaxConcurrent int
	// Enforce is broadcast.plans.enforce: off, every new session gets full up to the
	// concurrent-viewer limit, and counting continues (the rollback).
	Enforce bool
}

// BroadcastModeFor decides a new session's mode (story 50.20 §2, §3). The order of spending on
// Broadcaster: the allowance, then pay-as-you-go until its cost reaches the cap, then lowest
// until the month's non-bought viewing reaches the fair-use ceiling (2× the allowance), then
// wait. Guest sharing: the allowance, then wait. At the concurrent-viewer limit: wait.
func BroadcastModeFor(in BroadcastInput) string {
	if in.Tier == nil || in.Off {
		return BroadcastOff
	}
	limit := in.MaxConcurrent
	if limit <= 0 {
		limit = in.Tier.MaxConcurrentViewers
	}
	if in.Concurrent >= limit {
		return BroadcastWait
	}
	if !in.Enforce {
		return BroadcastFull
	}
	allowance := in.Tier.AllowanceMinutes()
	if in.Usage.MinutesFull < allowance {
		return BroadcastFull
	}
	if in.Tier.PAYG && in.PAYG.HasRoom() {
		return BroadcastFull
	}
	// bought viewing doesn't eat fair use: the ceiling counts what wasn't paid for
	if ceiling := in.Tier.FairUseMinutes(); ceiling > 0 && in.Usage.MinutesFull+in.Usage.MinutesLowest < ceiling {
		return BroadcastLowest
	}
	return BroadcastWait
}

// BroadcastPAYGCents is what viewer-minutes of pay-as-you-go cost at pricePer100H cents
// (rounded down to a whole cent).
func BroadcastPAYGCents(minutes, pricePer100H int64) int64 {
	if minutes <= 0 || pricePer100H <= 0 {
		return 0
	}
	return minutes * pricePer100H / 6000 // 100 h = 6,000 minutes
}

// BroadcastCapMinutes is how many pay-as-you-go viewer-minutes a cap buys at pricePer100H.
func BroadcastCapMinutes(capCents, pricePer100H int64) int64 {
	if capCents <= 0 || pricePer100H <= 0 {
		return 0
	}
	return capCents * 6000 / pricePer100H
}

// BroadcastSplit places a window's full-quality minutes (story 50.20 §3): first into what is
// left of the allowance, then, with pay-as-you-go on, into what is left of the cap's minutes;
// anything past both (the edge acted on a state up to 5 minutes old) is counted as full and
// never billed, so the provider is never sent more than the cap's worth.
func BroadcastSplit(tier *BroadcastTier, month BroadcastUsage, fullMinutes int64, payg BroadcastPAYG, pricePer100H int64) (toFull, toPAYG int64) {
	if fullMinutes <= 0 {
		return 0, 0
	}
	if tier == nil {
		return fullMinutes, 0
	}
	room := max(tier.AllowanceMinutes()-month.MinutesFull, 0)
	toFull = min(fullMinutes, room)
	rest := fullMinutes - toFull
	if rest > 0 && tier.PAYG && payg.On && pricePer100H > 0 {
		paygRoom := max(BroadcastCapMinutes(payg.CapCents, pricePer100H)-month.MinutesPAYG, 0)
		toPAYG = min(rest, paygRoom)
		rest -= toPAYG
	}
	return toFull + rest, toPAYG
}
