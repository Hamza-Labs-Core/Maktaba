package tvremote

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"
)

// SimulcastSettingsKey is the rules section of simulcast (story 32.13, README
// "Settings"): app_settings key rules.tv_simulcast holds only the values
// that differ from the defaults.
const SimulcastSettingsKey = "rules.tv_simulcast"

// SimulcastSettings are rules.tv_simulcast, defaults filled in.
type SimulcastSettings struct {
	Enabled              bool
	MaxOutputsPerChannel int
	MaxHoursPerMonth     float64
	Lead                 time.Duration
	Tail                 time.Duration
	Bad                  []string
}

// DefaultSimulcastSettings: off, 3 outputs a channel, 60 output-hours a
// month, start 60 s before a window and stop 30 s after.
func DefaultSimulcastSettings() SimulcastSettings {
	return SimulcastSettings{MaxOutputsPerChannel: 3, MaxHoursPerMonth: 60, Lead: 60 * time.Second, Tail: 30 * time.Second}
}

// ParseSimulcastSettings overlays the stored changes on the defaults; a value
// out of range keeps its default and is named in Bad.
func ParseSimulcastSettings(raw []byte) SimulcastSettings {
	s := DefaultSimulcastSettings()
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return s
	}
	num := func(k string, v json.RawMessage, lo, hi float64, whole bool) (float64, bool) {
		var f float64
		if json.Unmarshal(v, &f) != nil || f < lo || f > hi || (whole && f != float64(int(f))) {
			s.Bad = append(s.Bad, k)
			return 0, false
		}
		return f, true
	}
	for k, v := range m {
		switch k {
		case "enabled":
			if json.Unmarshal(v, &s.Enabled) != nil {
				s.Bad = append(s.Bad, k)
			}
		case "max_outputs_per_channel":
			if f, ok := num(k, v, 1, 50, true); ok {
				s.MaxOutputsPerChannel = int(f)
			}
		case "max_hours_per_month":
			if f, ok := num(k, v, 0, 10000, false); ok {
				s.MaxHoursPerMonth = f
			}
		case "lead_seconds":
			if f, ok := num(k, v, 0, 3600, true); ok {
				s.Lead = time.Duration(f) * time.Second
			}
		case "tail_seconds":
			if f, ok := num(k, v, 0, 3600, true); ok {
				s.Tail = time.Duration(f) * time.Second
			}
		default:
			s.Bad = append(s.Bad, k)
		}
	}
	sort.Strings(s.Bad)
	return s
}

// LoadSimulcastSettings reads rules.tv_simulcast (defaults when unset).
func LoadSimulcastSettings(ctx context.Context, db *sql.DB) SimulcastSettings {
	if db == nil {
		return DefaultSimulcastSettings()
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, SimulcastSettingsKey).Scan(&raw); err != nil {
		return DefaultSimulcastSettings()
	}
	return ParseSimulcastSettings(raw)
}
