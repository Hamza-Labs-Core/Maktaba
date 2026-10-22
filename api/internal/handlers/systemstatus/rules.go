package systemstatus

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Rules is rules.system_health (pipeline system_health_settings.py, the
// schema's source). The defaults are kept equal to schema.json by a test.
type Rules struct {
	HeartbeatLateSeconds   int     `json:"heartbeat_late_seconds"`
	HeartbeatGoneSeconds   int     `json:"heartbeat_gone_seconds"`
	WorkerForgetHours      float64 `json:"worker_forget_hours"`
	DBLatencyMs            int     `json:"db_latency_ms"`
	DependencyStaleSeconds int     `json:"dependency_stale_seconds"`
	CheckTimeoutMs         int     `json:"check_timeout_ms"`
	CacheSeconds           int     `json:"cache_seconds"`
}

// DefaultRules are the built-in rules.
func DefaultRules() Rules {
	return Rules{
		HeartbeatLateSeconds:   60,
		HeartbeatGoneSeconds:   300,
		WorkerForgetHours:      24,
		DBLatencyMs:            250,
		DependencyStaleSeconds: 600,
		CheckTimeoutMs:         2000,
		CacheSeconds:           5,
	}
}

func (r Rules) late() time.Duration { return time.Duration(r.HeartbeatLateSeconds) * time.Second }
func (r Rules) gone() time.Duration { return time.Duration(r.HeartbeatGoneSeconds) * time.Second }
func (r Rules) forget() time.Duration {
	return time.Duration(r.WorkerForgetHours * float64(time.Hour))
}
func (r Rules) timeout() time.Duration { return time.Duration(r.CheckTimeoutMs) * time.Millisecond }
func (r Rules) cache() time.Duration   { return time.Duration(r.CacheSeconds) * time.Second }

// loadRules reads app_settings rules.system_health over the defaults; a
// missing row or table, or a value out of its range, keeps the default.
func loadRules(ctx context.Context, db *sql.DB) Rules {
	cfg := DefaultRules()
	if db == nil {
		return cfg
	}
	var raw sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT value::text FROM app_settings WHERE key = 'rules.system_health'`).Scan(&raw); err != nil || !raw.Valid {
		return cfg
	}
	return applyRules(cfg, []byte(raw.String))
}

func applyRules(cfg Rules, raw []byte) Rules {
	var stored map[string]json.RawMessage
	if json.Unmarshal(raw, &stored) != nil {
		return cfg
	}
	num := func(key string, lo, hi float64) (float64, bool) {
		v, ok := stored[key]
		if !ok {
			return 0, false
		}
		var f float64
		if json.Unmarshal(v, &f) != nil || f < lo || f > hi {
			return 0, false
		}
		return f, true
	}
	if f, ok := num("heartbeat_late_seconds", 5, 3600); ok {
		cfg.HeartbeatLateSeconds = int(f)
	}
	if f, ok := num("heartbeat_gone_seconds", 10, 86400); ok {
		cfg.HeartbeatGoneSeconds = int(f)
	}
	if f, ok := num("worker_forget_hours", 1, 8760); ok {
		cfg.WorkerForgetHours = f
	}
	if f, ok := num("db_latency_ms", 1, 60000); ok {
		cfg.DBLatencyMs = int(f)
	}
	if f, ok := num("dependency_stale_seconds", 30, 86400); ok {
		cfg.DependencyStaleSeconds = int(f)
	}
	if f, ok := num("check_timeout_ms", 100, 30000); ok {
		cfg.CheckTimeoutMs = int(f)
	}
	if f, ok := num("cache_seconds", 0, 300); ok {
		cfg.CacheSeconds = int(f)
	}
	// gone is never before late
	if cfg.HeartbeatGoneSeconds < cfg.HeartbeatLateSeconds {
		cfg.HeartbeatGoneSeconds = cfg.HeartbeatLateSeconds
	}
	return cfg
}
