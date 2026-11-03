package components

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.14: the health a machine's status rows carry, for GET /api/components and the
// Components page. Everything comes from component_status (the rows the list already
// reads); the runners put the numbers in each row's detail when they report:
//
//	restarts        the supervisor's restart count of the component's processes
//	last_error      {code, at}: the last failure the runner saw (kept after it recovers)
//	last_transition {to, seconds}: from the generation it acted on to the phase reached
//	rss_mb          resident memory of its processes
//	disk_mb         its files on disk (assets and packs)
//
// The only other read is cluster_nodes' last heartbeat, once per list: a machine that has
// not been heard from for staleAfter has stale rows.

// staleAfter is how long a joined machine may go unheard before its rows are stale.
const staleAfter = 90 * time.Second

// Health is one component's health on one machine.
type Health struct {
	// Since is when it last started running (running or degraded only): its uptime.
	Since *time.Time `json:"since,omitempty"`
	// Restarts is how often the supervisor restarted its processes since the supervisor
	// started (process kinds; 0 for the others).
	Restarts int `json:"restarts"`
	// LastErrorCode and LastErrorAt are the last failure's reason code and time.
	LastErrorCode string     `json:"last_error_code,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
	// Version is the release that reported it.
	Version string `json:"version,omitempty"`
	// RSSMB is the memory of its processes, when a runner measures it.
	RSSMB *int64 `json:"rss_mb,omitempty"`
	// DiskMB is its files on disk: what a runner measured, else for an installed asset
	// the catalog's file sizes.
	DiskMB *int64 `json:"disk_mb,omitempty"`
	// Stale: the machine has not been heard from for 90 s, so the rows may be old.
	Stale bool `json:"stale"`
	// UpdatedAt is the newest of its rows' writes.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// num reads a JSON number of a detail map (float64 after a decode, int when built in Go).
func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func timeOf(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

// healthOf is the health of one component on one machine from its parts' rows. phase
// is the machine's rolled-up phase; installMB the catalog's size of an asset.
func healthOf(c *components.Component, rows []store.Status, phase string, installMB int64, stale bool) *Health {
	if len(rows) == 0 {
		return nil
	}
	h := &Health{Stale: stale}
	var rss, disk int64
	haveRSS, haveDisk := false, false
	for _, r := range rows {
		if h.Version == "" && r.Version != "" {
			h.Version = r.Version
		}
		if !r.UpdatedAt.IsZero() && (h.UpdatedAt == nil || r.UpdatedAt.After(*h.UpdatedAt)) {
			t := r.UpdatedAt
			h.UpdatedAt = &t
		}
		if (phase == components.PhaseRunning || phase == components.PhaseDegraded) &&
			(r.Phase == components.PhaseRunning || r.Phase == components.PhaseDegraded) && !r.Since.IsZero() {
			// the part that came up last is when the whole component did
			if h.Since == nil || r.Since.After(*h.Since) {
				t := r.Since
				h.Since = &t
			}
		}
		if r.Phase == components.PhaseFailed {
			if why := r.Reason(); why != "" {
				at := r.Since
				if h.LastErrorAt == nil || at.After(*h.LastErrorAt) {
					h.LastErrorCode, h.LastErrorAt = why, &at
				}
			}
		}
		d := r.Detail
		if d == nil {
			continue
		}
		if n, ok := num(d["restarts"]); ok && n > 0 {
			h.Restarts += int(n)
		}
		if n, ok := num(d["rss_mb"]); ok && n >= 0 {
			rss += int64(n)
			haveRSS = true
		}
		if n, ok := num(d["disk_mb"]); ok && n >= 0 {
			disk += int64(n)
			haveDisk = true
		}
		if le, ok := d["last_error"].(map[string]any); ok {
			code, _ := le["code"].(string)
			at, okAt := timeOf(le["at"])
			if code != "" && okAt && (h.LastErrorAt == nil || at.After(*h.LastErrorAt)) {
				h.LastErrorCode, h.LastErrorAt = code, &at
			}
		}
	}
	if haveRSS {
		h.RSSMB = &rss
	}
	if !haveDisk && strings.Contains(c.Kind, "asset") && installMB > 0 &&
		phase != components.PhaseNotInstalled && phase != components.PhaseInstalling {
		disk, haveDisk = installMB, true
	}
	if haveDisk {
		h.DiskMB = &disk
	}
	return h
}

// heardFrom is each joined machine's last heartbeat (cluster_nodes.last_seen_at); nil
// when it cannot be read (no stale marks then).
func (s *Service) heardFrom(ctx context.Context) map[string]time.Time {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id, last_seen_at FROM cluster_nodes WHERE last_seen_at IS NOT NULL`)
	if err != nil {
		s.log().Debug("components: the machines' heartbeats cannot be read", "err", err, "event", "components_heartbeat_unread")
		return nil
	}
	defer func() { _ = rows.Close() }()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var at time.Time
		if rows.Scan(&id, &at) == nil {
			out[id] = at
		}
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}

// stale reports whether machine's rows are old: a joined machine not heard from for
// staleAfter (the home answers this request, so its rows are its own).
func (snap *snapshot) stale(machine string, now time.Time) bool {
	if machine == store.Home || snap.heard == nil {
		return false
	}
	at, ok := snap.heard[machine]
	return !ok || now.Sub(at) > staleAfter
}
