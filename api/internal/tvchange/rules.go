package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
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
	KeepWarmMinutes    int
	RetentionHours     int
	VerifySoakSeconds  int
	RequireSecondAdmin bool
	ParamChangePush    string
	AutoRollback       bool
	HotStandby         bool
	StagingMaxExtra    int
	RollingSwap        string
	StandbyWarm        string
	ProbeChannel       string
	VerifyProbeSeconds int
}

// LoadConfig reads the effective tv_staging rules (the defaults with what was changed).
func LoadConfig(ctx context.Context, db *sql.DB) (Config, error) {
	m, err := rules.Effective(ctx, db, "tv_staging")
	if err != nil {
		return Config{}, err
	}
	c := Config{
		Enabled:            asBool(m["enabled"], true),
		AutoPromote:        map[string]string{},
		QuietWindow:        asStr(m["quiet_window"], "03:00-05:00"),
		QuietMaxViewers:    asInt(m["quiet_max_viewers"], 0),
		SwapWait:           asStr(m["swap_wait"], "boundary"),
		BoundaryMaxWaitMin: asInt(m["boundary_max_wait_minutes"], 30),
		KeepWarmMinutes:    asInt(m["keep_warm_minutes"], 15),
		RetentionHours:     asInt(m["rollback_retention_hours"], 24),
		VerifySoakSeconds:  asInt(m["verify_soak_seconds"], 120),
		VerifyProbeSeconds: asInt(m["verify_probe_seconds"], 30),
		RequireSecondAdmin: asBool(m["require_second_admin"], false),
		ParamChangePush:    asStr(m["param_change_push"], "approve"),
		AutoRollback:       asBool(m["auto_rollback"], true),
		HotStandby:         asBool(m["hot_standby"], true),
		StagingMaxExtra:    asInt(m["staging_max_extra_encodes"], 0),
		RollingSwap:        asStr(m["rolling_swap"], "auto"),
		StandbyWarm:        asStr(m["standby_warm"], "demand"),
		ProbeChannel:       asStr(m["probe_channel"], ""),
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
