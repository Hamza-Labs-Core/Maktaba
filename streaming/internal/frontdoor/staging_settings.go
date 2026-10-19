package frontdoor

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StagingSettings are the rules.tv_staging keys the runtime reads (story 33.1;
// the change-management keys belong to stories 33.5-33.13).
type StagingSettings struct {
	// Enabled: changes go through staging (owner: on by default). Effective only
	// in a StagingBuild.
	Enabled bool
	// HotStandby: a second slot runs the live release, warm on the watched
	// channels, and takes over at once when the live slot crashes (home: on).
	HotStandby bool
	// StandbyWarm: "viewers" (channels with human viewers first, then pushes by
	// remote viewers, within the encoder budget) or "demand" (every channel with
	// demand, as live, within the budget).
	StandbyWarm string
}

// Standby warm modes.
const (
	StandbyWarmViewers = "viewers"
	StandbyWarmDemand  = "demand"
)

// DefaultStagingSettings are the owner's decisions; standby_warm is viewers so
// the standby never spends an encoder on a channel nobody watches by default.
func DefaultStagingSettings() StagingSettings {
	return StagingSettings{Enabled: true, HotStandby: true, StandbyWarm: StandbyWarmViewers}
}

// StagingActive reports whether staging may run (the setting and the build).
func (s StagingSettings) StagingActive() bool { return s.Enabled && StagingBuild }

// ApplyStagingRules overlays a stored rules.tv_staging value on the defaults; a
// value of the wrong type keeps the default.
func ApplyStagingRules(base StagingSettings, raw []byte) StagingSettings {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return base
	}
	out := base
	var b bool
	if v, ok := m["enabled"]; ok && json.Unmarshal(v, &b) == nil {
		out.Enabled = b
	}
	if v, ok := m["hot_standby"]; ok && json.Unmarshal(v, &b) == nil {
		out.HotStandby = b
	}
	var s string
	if v, ok := m["standby_warm"]; ok && json.Unmarshal(v, &s) == nil &&
		(s == StandbyWarmViewers || s == StandbyWarmDemand) {
		out.StandbyWarm = s
	}
	return out
}

// PGStagingSettings reads rules.tv_staging (cached for a few seconds).
type PGStagingSettings struct {
	Pool *pgxpool.Pool

	mu  sync.Mutex
	at  time.Time
	cur StagingSettings
}

// Staging is the current settings (defaults when unreadable).
func (p *PGStagingSettings) Staging(ctx context.Context) StagingSettings {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.at.IsZero() && time.Since(p.at) < 5*time.Second {
		return p.cur
	}
	cur := DefaultStagingSettings()
	if p.Pool != nil {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		var raw []byte
		if p.Pool.QueryRow(qctx, `SELECT value::text FROM app_settings WHERE key = 'rules.tv_staging'`).Scan(&raw) == nil {
			cur = ApplyStagingRules(cur, raw)
		}
		cancel()
	}
	p.cur, p.at = cur, time.Now()
	return cur
}
