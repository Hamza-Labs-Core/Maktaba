package placement

import "sort"

// Need is what a channel needs of its host.
type Need struct {
	// Live: the channel has live (yt-dlp) blocks within the next 24 h.
	Live bool
}

// Why nothing was chosen.
const (
	ReasonAllFull    = "all_full"
	ReasonNoLiveHost = "no_live_host"
	ReasonNoHost     = "no_host"
)

// Choice is the outcome of Choose.
type Choice struct {
	Host string
	// Why: when Host is "", why nothing fits.
	Why string
	// PinSkipped: the pin could not be honoured, and why ("" when there was no pin or
	// it was honoured).
	PinSkipped string
}

// Choose picks the host for a new placement (design §3.3). used counts the active
// placements per host (not the hosts' own reports, so concurrent decisions never
// overfill a host); pin is the preferred host *name* ("" for none); exclude is a host
// the channel must leave (move), considered only when nothing else fits.
func Choose(cands []Candidate, used map[string]int, s Settings, need Need, pin, exclude string) Choice {
	type opt struct {
		Candidate
		used int
	}
	var opts []opt
	anyRoom, anyEligible := false, false
	for _, c := range cands {
		if !c.Eligible {
			continue
		}
		anyEligible = true
		u := used[c.ID]
		if u >= c.Capacity {
			continue
		}
		anyRoom = true
		if need.Live && !c.CanLive {
			continue
		}
		opts = append(opts, opt{c, u})
	}
	if len(opts) > 1 && exclude != "" {
		kept := opts[:0]
		for _, o := range opts {
			if o.ID != exclude {
				kept = append(kept, o)
			}
		}
		opts = kept
	}
	var ch Choice
	if pin != "" {
		for _, o := range opts {
			if o.Name == pin || o.ID == pin {
				return Choice{Host: o.ID}
			}
		}
		ch.PinSkipped = pinWhy(cands, used, need, pin)
	}
	if len(opts) == 0 {
		switch {
		case !anyEligible:
			ch.Why = ReasonNoHost
		case !anyRoom:
			ch.Why = ReasonAllFull
		case need.Live:
			ch.Why = ReasonNoLiveHost
		default:
			ch.Why = ReasonAllFull
		}
		return ch
	}
	ratio := func(o opt) float64 {
		if o.Capacity <= 0 {
			return 1
		}
		return float64(o.used) / float64(o.Capacity)
	}
	less := func(a, b opt, homeBias bool) bool {
		ra, rb := ratio(a), ratio(b)
		if ra != rb {
			return ra < rb
		}
		if homeBias && a.IsHome() != b.IsHome() {
			return a.IsHome()
		}
		if a.Weight != b.Weight {
			return a.Weight > b.Weight
		}
		return a.ID < b.ID
	}
	if s.Policy == PolicyBalanced {
		sort.SliceStable(opts, func(i, j int) bool { return less(opts[i], opts[j], true) })
		ch.Host = opts[0].ID
		return ch
	}
	// home_first
	var home *opt
	var others []opt
	for i := range opts {
		if opts[i].IsHome() {
			home = &opts[i]
		} else {
			others = append(others, opts[i])
		}
	}
	spill := s.SpillAt
	if spill <= 0 || spill > 100 {
		spill = 100
	}
	if home != nil && float64(home.used) < float64(spill)/100*float64(home.Capacity) {
		ch.Host = home.ID
		return ch
	}
	if len(others) > 0 {
		sort.SliceStable(others, func(i, j int) bool { return less(others[i], others[j], false) })
		ch.Host = others[0].ID
		return ch
	}
	if home != nil {
		ch.Host = home.ID
		return ch
	}
	ch.Why = ReasonAllFull
	return ch
}

func pinWhy(cands []Candidate, used map[string]int, need Need, pin string) string {
	for _, c := range cands {
		if c.Name != pin && c.ID != pin {
			continue
		}
		switch {
		case !c.Eligible:
			if c.Why != "" {
				return c.Why
			}
			return "not_online"
		case used[c.ID] >= c.Capacity:
			return "full"
		case need.Live && !c.CanLive:
			return "no_live"
		}
		return "excluded"
	}
	return "unknown_host"
}
