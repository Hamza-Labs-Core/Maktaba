// Package placement is the home's balancer (Epic 32.2, design §3): one placement row
// per channel naming the single host that may encode it, fenced by a cluster-wide
// generation; the policies that pick the host; the host health the decisions use
// (heartbeats, front-door failures, flapping); the fences the channel engine asks on
// the home and on a node; and the failover sweeper (Epic 32.4).
package placement

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Policies.
const (
	PolicyHomeFirst = "home_first"
	PolicyBalanced  = "balanced"
)

// HLS modes.
const (
	HLSProxy    = "proxy"
	HLSRedirect = "redirect"
)

// Settings are rules.tv_cluster (defaults keep today's behaviour: the home streams).
type Settings struct {
	NodesStream bool
	Policy      string
	SpillAt     int
	HLSMode     string
	Failover    bool
	DeadAfter   time.Duration
	// Pins: channel slug → preferred host name.
	Pins map[string]string
}

// DefaultSettings are the rules' defaults.
func DefaultSettings() Settings {
	return Settings{Policy: PolicyHomeFirst, SpillAt: 100, HLSMode: HLSProxy, Failover: true,
		DeadAfter: 15 * time.Second, Pins: map[string]string{}}
}

// ParseSettings overlays the stored rules.tv_cluster changes on the defaults; a value
// that cannot be used keeps its default.
func ParseSettings(raw []byte) Settings {
	s := DefaultSettings()
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return s
	}
	for k, v := range m {
		switch k {
		case "nodes_stream":
			var b bool
			if json.Unmarshal(v, &b) == nil {
				s.NodesStream = b
			}
		case "failover":
			var b bool
			if json.Unmarshal(v, &b) == nil {
				s.Failover = b
			}
		case "policy":
			var p string
			if json.Unmarshal(v, &p) == nil && (p == PolicyHomeFirst || p == PolicyBalanced) {
				s.Policy = p
			}
		case "hls_mode":
			var p string
			if json.Unmarshal(v, &p) == nil && (p == HLSProxy || p == HLSRedirect) {
				s.HLSMode = p
			}
		case "spill_at":
			var n float64
			if json.Unmarshal(v, &n) == nil && n >= 1 && n <= 100 {
				s.SpillAt = int(n)
			}
		case "dead_after_seconds":
			var n float64
			if json.Unmarshal(v, &n) == nil && n >= 5 && n <= 300 {
				s.DeadAfter = time.Duration(n) * time.Second
			}
		case "pins":
			// {slug: "host"} or the settings form {slug: ["host"]}
			var raw map[string]json.RawMessage
			if json.Unmarshal(v, &raw) != nil {
				continue
			}
			for slug, hv := range raw {
				var one string
				var list []string
				if json.Unmarshal(hv, &one) == nil {
					one = strings.TrimSpace(one)
				} else if json.Unmarshal(hv, &list) == nil && len(list) > 0 {
					one = strings.TrimSpace(list[0])
				}
				if slug = strings.TrimSpace(slug); slug != "" && one != "" {
					s.Pins[slug] = one
				}
			}
		}
	}
	return s
}

// SettingsSource returns the current settings.
type SettingsSource interface {
	Cluster(ctx context.Context) Settings
}

// StaticSettings serves fixed settings (tests).
type StaticSettings Settings

func (s StaticSettings) Cluster(context.Context) Settings { return Settings(s) }

// PGSettings reads rules.tv_cluster from app_settings, cached for TTL (5 s).
type PGSettings struct {
	Pool *pgxpool.Pool
	TTL  time.Duration

	mu sync.Mutex
	at time.Time
	s  Settings
	ok bool
}

func (p *PGSettings) Cluster(ctx context.Context) Settings {
	ttl := p.TTL
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	p.mu.Lock()
	if p.ok && time.Since(p.at) < ttl {
		s := p.s
		p.mu.Unlock()
		return s
	}
	p.mu.Unlock()
	var raw []byte
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := p.Pool.QueryRow(qctx, `SELECT value::text FROM app_settings WHERE key = 'rules.tv_cluster'`).Scan(&raw)
	cancel()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil && p.ok && !strings.Contains(err.Error(), "no rows") {
		p.at = time.Now() // keep the last good values
		return p.s
	}
	p.s, p.at, p.ok = ParseSettings(raw), time.Now(), true
	return p.s
}
