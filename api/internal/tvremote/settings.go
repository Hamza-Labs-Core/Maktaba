package tvremote

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// SettingsKey is the rules section of remote viewing (story 32.10 AC1):
// app_settings key rules.tv_remote holds only the values that differ from
// the defaults. tv_remote.enabled stays its own key (turning it on needs a
// passed Test connection, story 32.6).
const SettingsKey = "rules.tv_remote"

// Viewer modes.
const (
	ViewersAll    = "all"
	ViewersAdmins = "admins"
	ViewersListed = "listed"
)

// Budget actions.
const (
	BudgetBlockNew = "block_new"
	BudgetWarn     = "warn"
)

// Settings are rules.tv_remote, defaults filled in.
type Settings struct {
	Viewers              string
	ViewerUsers          []string
	MaxStreamsPerUser    int
	MaxChannels          int
	AllowedCountries     []string
	IdleTimeout          time.Duration
	TokenTTL             time.Duration
	Ingest               string
	LowLatency           bool
	MaxPushHours         int
	MonthlyBudgetUSD     float64
	BudgetAction         string
	DeliveryUSDPer1000   float64
	StorageUSDPer1000    float64
	StoragePlanMinutes   int
	StorageMarginMinutes int
	RemoteDetection      string
	// Bad lists the stored values that were refused (their default applies).
	Bad []string
}

// DefaultSettings are the owner's defaults (README Q1, Q4, Q6, Q8).
func DefaultSettings() Settings {
	return Settings{
		Viewers: ViewersAll, MaxStreamsPerUser: 2, MaxChannels: 22,
		IdleTimeout: 3 * time.Minute, TokenTTL: 60 * time.Minute, Ingest: "rtmps",
		MaxPushHours: 8, MonthlyBudgetUSD: 10, BudgetAction: BudgetBlockNew,
		DeliveryUSDPer1000: 1, StorageUSDPer1000: 5, StoragePlanMinutes: 1000,
		StorageMarginMinutes: 30, RemoteDetection: "relay",
	}
}

// MaxPush is max_push_hours as a duration.
func (s Settings) MaxPush() time.Duration { return time.Duration(s.MaxPushHours) * time.Hour }

// ParseSettings overlays the stored changes on the defaults. A value out of
// its range (story 32.10 AC1) keeps the default and is named in Bad.
func ParseSettings(raw []byte) Settings {
	s := DefaultSettings()
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return s
	}
	bad := func(k string) { s.Bad = append(s.Bad, k) }
	intIn := func(k string, v json.RawMessage, lo, hi int, dst *int) {
		var f float64
		if json.Unmarshal(v, &f) != nil || f != float64(int(f)) || int(f) < lo || int(f) > hi {
			bad(k)
			return
		}
		*dst = int(f)
	}
	nonNeg := func(k string, v json.RawMessage, dst *float64) {
		var f float64
		if json.Unmarshal(v, &f) != nil || f < 0 {
			bad(k)
			return
		}
		*dst = f
	}
	choice := func(k string, v json.RawMessage, dst *string, opts ...string) {
		var x string
		if json.Unmarshal(v, &x) == nil {
			for _, o := range opts {
				if x == o {
					*dst = x
					return
				}
			}
		}
		bad(k)
	}
	for k, v := range m {
		switch k {
		case "viewers":
			choice(k, v, &s.Viewers, ViewersAll, ViewersAdmins, ViewersListed)
		case "viewer_users":
			var xs []string
			if json.Unmarshal(v, &xs) != nil {
				bad(k)
				continue
			}
			for _, x := range xs {
				if x = strings.TrimSpace(x); x != "" {
					s.ViewerUsers = append(s.ViewerUsers, x)
				}
			}
		case "max_streams_per_user":
			intIn(k, v, 1, 10, &s.MaxStreamsPerUser)
		case "max_channels":
			intIn(k, v, 1, 50, &s.MaxChannels)
		case "allowed_countries":
			var xs []string
			if json.Unmarshal(v, &xs) != nil {
				bad(k)
				continue
			}
			cs, ok := countries(xs)
			if !ok {
				bad(k)
				continue
			}
			s.AllowedCountries = cs
		case "idle_timeout_minutes":
			n := 3
			intIn(k, v, 1, 60, &n)
			s.IdleTimeout = time.Duration(n) * time.Minute
		case "token_ttl_minutes":
			n := 60
			intIn(k, v, 5, 1440, &n)
			s.TokenTTL = time.Duration(n) * time.Minute
		case "ingest":
			choice(k, v, &s.Ingest, "rtmps", "srt")
		case "low_latency":
			if json.Unmarshal(v, &s.LowLatency) != nil {
				bad(k)
			}
		case "max_push_hours":
			intIn(k, v, 1, 24, &s.MaxPushHours)
		case "monthly_budget_usd":
			nonNeg(k, v, &s.MonthlyBudgetUSD)
		case "budget_action":
			choice(k, v, &s.BudgetAction, BudgetBlockNew, BudgetWarn)
		case "delivery_usd_per_1000_min":
			nonNeg(k, v, &s.DeliveryUSDPer1000)
		case "storage_usd_per_1000_min":
			nonNeg(k, v, &s.StorageUSDPer1000)
		case "storage_plan_minutes":
			intIn(k, v, 0, 1_000_000, &s.StoragePlanMinutes)
		case "storage_margin_minutes":
			intIn(k, v, 0, 10000, &s.StorageMarginMinutes)
		case "remote_detection":
			choice(k, v, &s.RemoteDetection, "relay", "relay_or_public_ip")
		default:
			bad(k)
		}
	}
	sort.Strings(s.Bad)
	return s
}

// countries upper-cases and checks ISO 3166-1 alpha-2 codes (two letters;
// the user-assigned XA-XZ and ZZ are refused).
func countries(xs []string) ([]string, bool) {
	out := make([]string, 0, len(xs))
	seen := map[string]bool{}
	for _, x := range xs {
		c := strings.ToUpper(strings.TrimSpace(x))
		if len(c) != 2 || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' || c[0] == 'X' || c == "ZZ" || c == "AA" {
			return nil, false
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, true
}

// LoadSettings reads rules.tv_remote (defaults when unset or unreadable).
func LoadSettings(ctx context.Context, db *sql.DB) Settings {
	if db == nil {
		return DefaultSettings()
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, SettingsKey).Scan(&raw); err != nil {
		return DefaultSettings()
	}
	return ParseSettings(raw)
}
