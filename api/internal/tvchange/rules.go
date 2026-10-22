package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
)

// Auto-promote rules, strictest first (README "Settings": auto_promote).
const (
	RuleApproval    = "approval"
	RuleQuietWindow = "quiet_window"
	RuleImmediate   = "immediate"
)

// Kinds a change may carry (story 33.10 AC3).
var Kinds = []string{"lineup", "schedule", "settings", "art", "live_slots", "binary", "rollback"}

// Config is rules.tv_staging (pipeline tvrelease/config.py; schema.json).
type Config struct {
	Enabled            bool
	AutoPromote        map[string]string
	QuietWindow        string
	QuietMaxViewers    int
	SwapWait           string
	BoundaryMaxWaitMin int
	// SwapStartTimeoutMin: a swap operation no front door took within this fails
	// (rolled back, with a notice); 0: it waits for the front door however long
	SwapStartTimeoutMin int
	KeepWarmMinutes     int
	RetentionHours      int
	VerifySoakSeconds   int
	RequireSecondAdmin  bool
	ParamChangePush     string
	AutoRollback        bool
	HotStandby          bool
	StagingMaxExtra     int
	RollingSwap         string
	StandbyWarm         string
	ProbeChannel        string
	VerifyProbeSeconds  int
	// AutoMaxChannels: a change a rule would put on the air waits for an admin when it
	// touches more channels than this; 0: no limit
	AutoMaxChannels int
}

// DefaultAutoMaxChannels is tv_staging.auto_max_channels' default.
const DefaultAutoMaxChannels = 3

// DefaultSwapStartTimeoutMin is tv_staging.swap_start_timeout_minutes' default.
// A front door that is up takes a swap within a second and fails one it cannot
// run itself after 90 s, so this only ends a swap while no front door runs.
const DefaultSwapStartTimeoutMin = 10

// LoadConfig reads the effective tv_staging rules (the defaults with what was changed).
func LoadConfig(ctx context.Context, db *sql.DB) (Config, error) {
	m, err := rules.Effective(ctx, db, "tv_staging")
	if err != nil {
		return Config{}, err
	}
	c := Config{
		Enabled:             asBool(m["enabled"], true),
		AutoPromote:         map[string]string{},
		QuietWindow:         asStr(m["quiet_window"], "03:00-05:00"),
		QuietMaxViewers:     asInt(m["quiet_max_viewers"], 0),
		SwapWait:            asStr(m["swap_wait"], "boundary"),
		BoundaryMaxWaitMin:  asInt(m["boundary_max_wait_minutes"], 30),
		SwapStartTimeoutMin: asInt(m["swap_start_timeout_minutes"], DefaultSwapStartTimeoutMin),
		KeepWarmMinutes:     asInt(m["keep_warm_minutes"], 15),
		RetentionHours:      asInt(m["rollback_retention_hours"], 24),
		VerifySoakSeconds:   asInt(m["verify_soak_seconds"], 120),
		VerifyProbeSeconds:  asInt(m["verify_probe_seconds"], 30),
		RequireSecondAdmin:  asBool(m["require_second_admin"], false),
		ParamChangePush:     asStr(m["param_change_push"], "approve"),
		AutoRollback:        asBool(m["auto_rollback"], true),
		HotStandby:          asBool(m["hot_standby"], true),
		StagingMaxExtra:     asInt(m["staging_max_extra_encodes"], 0),
		RollingSwap:         asStr(m["rolling_swap"], "auto"),
		StandbyWarm:         asStr(m["standby_warm"], "demand"),
		ProbeChannel:        asStr(m["probe_channel"], ""),
		AutoMaxChannels:     asInt(m["auto_max_channels"], DefaultAutoMaxChannels),
	}
	for _, k := range Kinds {
		c.AutoPromote[k] = asStr(m["auto_promote_"+k], RuleApproval)
	}
	return c, nil
}

func asBool(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

func asStr(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func asInt(v any, def int) int {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n)
		}
		if f, err := x.Float64(); err == nil {
			return int(f)
		}
	case float64:
		return int(x)
	case int:
		return x
	}
	return def
}

var ruleRank = map[string]int{RuleApproval: 0, RuleQuietWindow: 1, RuleImmediate: 2}

// StrictestRule is the strictest auto-promote rule among kinds (design §11.3, D4).
// No kinds: approval.
func StrictestRule(c Config, kinds []string) string {
	best := ""
	for _, k := range kinds {
		r := c.AutoPromote[k]
		if _, ok := ruleRank[r]; !ok {
			r = RuleApproval
		}
		if best == "" || ruleRank[r] < ruleRank[best] {
			best = r
		}
	}
	if best == "" {
		return RuleApproval
	}
	return best
}

// InQuietWindow reports whether local time t is inside "HH:MM-HH:MM" (a window over
// midnight, 23:00-02:00, works). A malformed window is never quiet.
func InQuietWindow(window string, t time.Time) bool {
	a, b, ok := strings.Cut(strings.TrimSpace(window), "-")
	if !ok {
		return false
	}
	from, ok1 := hm(a)
	to, ok2 := hm(b)
	if !ok1 || !ok2 {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if from <= to {
		return m >= from && m < to
	}
	return m >= from || m < to
}

func hm(s string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// AutoPromote decides whether a ready change goes on the air by itself (design §11.3,
// story 33.10 AC6): approval never; immediate when it has no warnings; quiet_window
// at a moment inside the window with at most quiet_max_viewers viewers and no
// warnings. An explicit swap_at in the future waits for it.
func AutoPromote(c Config, kinds []string, local time.Time, viewers int, warnings bool, swapAt *time.Time) bool {
	if warnings {
		return false
	}
	if swapAt != nil && local.Before(*swapAt) {
		return false
	}
	switch StrictestRule(c, kinds) {
	case RuleImmediate:
		return true
	case RuleQuietWindow:
		return InQuietWindow(c.QuietWindow, local) && viewers <= c.QuietMaxViewers
	}
	return false
}

// lineupLists are the diff.lineup lists that name a channel the change touches.
var lineupLists = []string{"added", "changed", "removed", "renamed", "renumbered"}

// AutoBlocked is why a change an auto rule would put on the air still waits for an
// admin ("" when nothing does): it touches more than AutoMaxChannels channels, or the
// channel planner reported conflicts it left out.
func AutoBlocked(c Config, ch *Change) string {
	if c.AutoMaxChannels > 0 {
		seen := map[string]bool{}
		lineup, _ := ch.Diff["lineup"].(map[string]any)
		for _, k := range lineupLists {
			rows, _ := lineup[k].([]any)
			for _, r := range rows {
				row, _ := r.(map[string]any)
				id, _ := row["id"].(string)
				if id == "" {
					id = fmt.Sprintf("%s#%d", k, len(seen))
				}
				seen[id] = true
			}
		}
		if len(seen) > c.AutoMaxChannels {
			return "too_many_channels"
		}
	}
	var set struct {
		Meta struct {
			Conflicts []json.RawMessage `json:"conflicts"`
		} `json:"meta"`
	}
	if len(ch.ChangeSet) > 0 && json.Unmarshal(ch.ChangeSet, &set) == nil && len(set.Meta.Conflicts) > 0 {
		return "conflicts"
	}
	return ""
}
