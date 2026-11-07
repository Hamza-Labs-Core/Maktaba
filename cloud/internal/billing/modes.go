package billing

// Relay modes (story 50.19 §3): what the relay does with a home's forwarded request.
const (
	// ModeRefused: no relay on the plan (Free, or a paid plan that lapsed): 402
	// relay_needs_plan, the tunnel kept.
	ModeRefused = "refused"
	// ModeFull: forwarded untouched (Pro within its allowance or its pay-as-you-go cap).
	ModeFull = "full"
	// ModeCapped: X-Maktaba-Relay-Cap-Kbps with the plan's cap, media paced.
	ModeCapped = "capped"
	// ModeCappedSlow: the same with the slow cap, past fair use.
	ModeCappedSlow = "capped_slow"
)

// Usage is a home's relayed bytes this month, by the mode they were counted in. PAYG is the
// part of full-quality bytes bought pay-as-you-go (Full excludes it).
type Usage struct {
	Full, PAYG, Capped, CappedSlow int64
}

// Total is every relayed byte.
func (u Usage) Total() int64 { return u.Full + u.PAYG + u.Capped + u.CappedSlow }

// PAYG is a Pro account's pay-as-you-go: on, its monthly cap in cents (none: off, story 50.19
// §5), and the price of a GB.
type PAYG struct {
	On                bool
	CapCents          int64
	PricePerGBCents   int64
	SpentCentsAlready int64 // what this month's PAYG bytes cost (from Usage.PAYG at the price)
}

// Decision is a request's mode, and whether its bytes count as pay-as-you-go.
type Decision struct {
	Mode    string
	PAYG    bool
	CapKbps int // the cap the forwarded request carries (0: none)
}

// ModeFor decides a home's relay mode from its plan, this month's usage and its pay-as-you-go
// (story 50.19 §3, §4). The order of spending on Pro: the allowance at full quality, then
// pay-as-you-go until its cost reaches the cap, then capped, then capped_slow past fair use
// in capped mode. Basic: capped, then capped_slow past fair use. An allowance not set yet
// (TBD) is not enforced: full. A plan this build doesn't know is forwarded in full (never cut
// off by a catalog it can't read).
func ModeFor(plan string, u Usage, p PAYG) Decision {
	t, ok := Tiers[plan]
	if !ok {
		return Decision{Mode: ModeFull}
	}
	if !t.Relay {
		return Decision{Mode: ModeRefused}
	}
	capped := func() Decision {
		if t.FairUseBytesPerMo > 0 && u.Capped >= t.FairUseBytesPerMo {
			return Decision{Mode: ModeCappedSlow, CapKbps: t.RelaySlowCapKbps}
		}
		capKbps := t.RelayCapKbps
		if capKbps == 0 {
			capKbps = Tiers[PlanBasic].RelayCapKbps // Pro past everything: Basic's quality
		}
		return Decision{Mode: ModeCapped, CapKbps: capKbps}
	}
	if t.RelayCapKbps > 0 {
		return capped() // Basic
	}
	if t.AllowanceBytesPerMo == nil || u.Full < *t.AllowanceBytesPerMo {
		return Decision{Mode: ModeFull}
	}
	if t.PayAsYouGo && p.On && p.CapCents > 0 && p.SpentCentsAlready < p.CapCents {
		return Decision{Mode: ModeFull, PAYG: true}
	}
	return capped()
}

// PAYGSpentCents is what bytes of pay-as-you-go cost at pricePerGB cents (rounded down to a
// whole cent: the provider is billed in whole GB-hours, clamped to the cap, story 50.19 §6).
func PAYGSpentCents(bytes, pricePerGBCents int64) int64 {
	if bytes <= 0 || pricePerGBCents <= 0 {
		return 0
	}
	// bytes × price / GB without overflow for any month a home can relay
	return bytes / 1_000_000 * pricePerGBCents / 1_000
}
