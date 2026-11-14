package broadcast

import (
	"slices"
	"time"
)

// The six publish checks (§1), in the order they run. A refusal names the first that fails.
const (
	CheckPlan        = "plan"
	CheckAgreement   = "agreement"
	CheckPublishable = "publishable"
	CheckUplink      = "uplink"
	CheckEncoder     = "encoder"
	CheckRights      = "rights"
)

// CheckOrder is the checks in the order publishing runs them.
var CheckOrder = []string{CheckPlan, CheckAgreement, CheckPublishable, CheckUplink, CheckEncoder, CheckRights}

// Refusal codes: the web's i18n keys (broadcast.<code>), with Params filling them.
const (
	CodeNeedsPlan      = "needsPro"
	CodePlanChannels   = "limit.channels"
	CodePlanAccess     = "planAccess"
	CodePlanOnDemand   = "planOnDemand"
	CodePlanRungs      = "planRungs"
	CodePlanTopKbps    = "planTopKbps"
	CodeAgreement      = "agreementRequired"
	CodePublishable    = "notEnoughPublishable"
	CodeNoMeasurement  = "uplinkUnmeasured"
	CodeUplinkLow      = "uplinkLow"
	CodeNoCapacity     = "noCapacity"
	CodeSessions       = "noSessions"
	CodeSoftwareRungs  = "softwareRungs"
	CodeRightsRequired = "rightsRequired"
)

// MinPublishableHours is check 3's floor: hours of the next 24 that must be publishable.
const MinPublishableHours = 6

// Plan is what the home's plan lets it publish (the cloud's GET /v1/broadcast/state).
type Plan struct {
	// Tier is "" (none: no Pro), "guest" or "broadcaster".
	Tier              string   `json:"plan"`
	MaxChannels       int      `json:"max_channels"`
	MaxRungs          int      `json:"max_rungs"`
	MaxTopKbps        int      `json:"max_top_kbps"`
	Access            []string `json:"access"`
	OnDemandOnly      bool     `json:"on_demand_only"`
	AgreementAccepted bool     `json:"agreement_accepted"`
	AgreementVersion  int      `json:"agreement_version"`
}

// Setup is the channel as the admin set it up.
type Setup struct {
	Access string   `json:"access"` // invite, open
	Mode   string   `json:"mode"`   // always_on, on_demand
	Rungs  []string `json:"rungs"`
}

// Published is another channel already published (its effective ladder and source).
type Published struct {
	ChannelID   string
	Rungs       []string
	Source      Source
	PublishedAt time.Time
	Dropped     int
}

// Host is the encoder room on the machine the channel's encode is placed on (or the home).
type Host struct {
	// MaxActive is tv_output.max_active_channels for the host (its override if set).
	MaxActive int
	// InUse is the slots its channels hold now, the channel being published left out.
	InUse float64
	// Reserve is tv_public.reserve_slots: slots kept free for LAN and Plex viewers.
	Reserve int
	// SessionCap is the encoder family's hardware session cap (0: no cap: software).
	SessionCap int
	// SessionsInUse is the encoder sessions its channels and published rungs hold now.
	SessionsInUse int
	// Hardware reports whether the channel's encoder is a hardware one.
	Hardware bool
}

// Settings are rules.broadcast (§7).
type Settings struct {
	UplinkShare        float64 `json:"uplink_share"`
	RungSlotWeight     float64 `json:"rung_slot_weight"`
	AllowSoftwareRungs bool    `json:"allow_software_rungs"`
}

// DefaultSettings are rules.broadcast's defaults.
var DefaultSettings = Settings{UplinkShare: 0.5, RungSlotWeight: 0.5}

// Clamp keeps the settings in their ranges (uplink_share 0.2-0.8, weight 0.1-1).
func (s Settings) Clamp() Settings {
	if s.UplinkShare < 0.2 || s.UplinkShare > 0.8 {
		s.UplinkShare = DefaultSettings.UplinkShare
	}
	if s.RungSlotWeight < 0.1 || s.RungSlotWeight > 1 {
		s.RungSlotWeight = DefaultSettings.RungSlotWeight
	}
	return s
}

// Inputs are everything the checks read; nothing in them starts anything.
type Inputs struct {
	Plan     Plan
	Setup    Setup
	Source   Source
	Others   []Published // the home's other published channels
	Settings Settings
	// PublishableSeconds is how much of the next 24 h the channel can fill with
	// publishable items (the Publishable seam).
	PublishableSeconds int
	// UplinkKbps is the last measurement (0: none yet).
	UplinkKbps int
	Host       Host
	// RightsConfirmed is the admin's tick (check 6).
	RightsConfirmed bool
}

// Refusal is a failed check: which one, the page's code and its values.
type Refusal struct {
	Check  string         `json:"check"`
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// Run runs the six checks in order and returns the first that fails (nil: publish).
func Run(in Inputs) *Refusal {
	for _, c := range CheckOrder {
		if r := runOne(c, in); r != nil {
			return r
		}
	}
	return nil
}

func runOne(check string, in Inputs) *Refusal {
	switch check {
	case CheckPlan:
		return checkPlan(in)
	case CheckAgreement:
		if !in.Plan.AgreementAccepted {
			return &Refusal{Check: CheckAgreement, Code: CodeAgreement}
		}
	case CheckPublishable:
		if in.PublishableSeconds < MinPublishableHours*3600 {
			return &Refusal{Check: CheckPublishable, Code: CodePublishable, Params: map[string]any{
				"hours": in.PublishableSeconds / 3600, "need": MinPublishableHours}}
		}
	case CheckUplink:
		return checkUplink(in)
	case CheckEncoder:
		return checkEncoder(in)
	case CheckRights:
		if !in.RightsConfirmed {
			return &Refusal{Check: CheckRights, Code: CodeRightsRequired}
		}
	}
	return nil
}

func checkPlan(in Inputs) *Refusal {
	p, s := in.Plan, in.Setup
	refuse := func(code string, params map[string]any) *Refusal {
		return &Refusal{Check: CheckPlan, Code: code, Params: params}
	}
	switch {
	case p.Tier == "":
		return refuse(CodeNeedsPlan, nil)
	case len(in.Others) >= p.MaxChannels:
		return refuse(CodePlanChannels, map[string]any{"count": p.MaxChannels})
	case !slices.Contains(p.Access, s.Access):
		return refuse(CodePlanAccess, map[string]any{"access": s.Access})
	case p.OnDemandOnly && s.Mode != "on_demand":
		return refuse(CodePlanOnDemand, nil)
	case len(s.Rungs) > p.MaxRungs:
		return refuse(CodePlanRungs, map[string]any{"count": p.MaxRungs})
	case p.MaxTopKbps > 0 && TopKbps(s.Rungs, in.Source) > p.MaxTopKbps:
		return refuse(CodePlanTopKbps, map[string]any{"kbps": p.MaxTopKbps})
	}
	return nil
}

// UplinkBudget is the share of a measurement the published channels may use.
func UplinkBudget(measuredKbps int, share float64) int {
	return int(float64(measuredKbps) * share)
}

// UsedKbps is what the published channels need now.
func UsedKbps(others []Published) int {
	used := 0
	for _, o := range others {
		used += NeedKbps(Effective(o.Rungs, o.Dropped), o.Source)
	}
	return used
}

func checkUplink(in Inputs) *Refusal {
	if in.UplinkKbps <= 0 {
		return &Refusal{Check: CheckUplink, Code: CodeNoMeasurement}
	}
	budget := UplinkBudget(in.UplinkKbps, in.Settings.UplinkShare)
	used := UsedKbps(in.Others)
	if used+NeedKbps(in.Setup.Rungs, in.Source) <= budget {
		return nil
	}
	params := map[string]any{"kbps": in.UplinkKbps}
	if fit := Suggest(in.Setup.Rungs, in.Source, budget, used); fit != nil {
		params["suggestion"] = Describe(fit, in.Source)
		params["rungs"] = fit
	}
	return &Refusal{Check: CheckUplink, Code: CodeUplinkLow, Params: params}
}

// Suggest is the largest ladder that fits: the asked one with its top rungs dropped one at
// a time, first within what the other channels leave, else within the whole budget (the
// publisher would unpublish another channel for it). nil: not even the lowest rung fits.
func Suggest(names []string, s Source, budget, used int) []string {
	for _, room := range []int{budget - used, budget} {
		for i := 1; i < len(names); i++ {
			if NeedKbps(names[i:], s) <= room {
				return names[i:]
			}
		}
	}
	return nil
}

// SlotsNeeded is what a published channel holds: one slot for its own encode, plus each
// lower rung's weight (§4).
func SlotsNeeded(names []string, weight float64) float64 {
	return 1 + weight*float64(EncodedRungs(names))
}

func checkEncoder(in Inputs) *Refusal {
	h := in.Host
	enc := EncodedRungs(in.Setup.Rungs)
	if enc > 0 && !h.Hardware && !in.Settings.AllowSoftwareRungs {
		return &Refusal{Check: CheckEncoder, Code: CodeSoftwareRungs}
	}
	need := SlotsNeeded(in.Setup.Rungs, in.Settings.RungSlotWeight)
	if float64(h.MaxActive)-h.InUse-need < float64(h.Reserve) {
		return &Refusal{Check: CheckEncoder, Code: CodeNoCapacity, Params: map[string]any{
			"reserve": h.Reserve, "free": float64(h.MaxActive) - h.InUse, "need": need}}
	}
	// the copy rung takes no session; the channel's own encode already holds one when placed
	if h.Hardware && h.SessionCap > 0 && h.SessionsInUse+enc > h.SessionCap {
		return &Refusal{Check: CheckEncoder, Code: CodeSessions, Params: map[string]any{"cap": h.SessionCap}}
	}
	return nil
}

// Drop is a channel whose top rung the uplink rule takes off: dropped is its new count.
type Drop struct {
	ChannelID string
	Dropped   int
}

// DropsFor is what a new measurement does to the published channels (§3): while their need
// passes the budget, the newest channel that still has more than one rung loses its top
// one. A channel is never stopped for this, so the result can still be over the budget.
func DropsFor(published []Published, measuredKbps int, share float64) []Drop {
	budget := UplinkBudget(measuredKbps, share)
	chs := slices.Clone(published)
	slices.SortStableFunc(chs, func(a, b Published) int { return b.PublishedAt.Compare(a.PublishedAt) })
	changed := map[string]int{}
	for UsedKbps(chs) > budget {
		moved := false
		for i := range chs {
			if len(Effective(chs[i].Rungs, chs[i].Dropped)) > 1 {
				chs[i].Dropped++
				changed[chs[i].ChannelID] = chs[i].Dropped
				moved = true
				break
			}
		}
		if !moved {
			break
		}
	}
	var out []Drop
	for _, c := range chs {
		if d, ok := changed[c.ChannelID]; ok {
			out = append(out, Drop{ChannelID: c.ChannelID, Dropped: d})
		}
	}
	return out
}
