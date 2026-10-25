package cluster

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LinkMode reads the home's cluster link mode (rules.cluster link_mode, Epic 35 story
// 35.16) at most every 30 s: in require, the front door reads the machines' channels
// only over their links and a machine's streaming port refuses the network.
type LinkMode struct {
	Pool *pgxpool.Pool

	mu   sync.Mutex
	at   time.Time
	mode string
}

// Mode is the current mode ("prefer" until it is read).
func (m *LinkMode) Mode(ctx context.Context) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Pool == nil {
		return "prefer"
	}
	if time.Since(m.at) < 30*time.Second && m.mode != "" {
		return m.mode
	}
	var mode *string
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := m.Pool.QueryRow(qctx, `SELECT value->>'link_mode' FROM app_settings WHERE key = 'rules.cluster'`).Scan(&mode)
	m.at = time.Now()
	switch {
	case err != nil && m.mode != "":
		// the last word until the database answers
	case mode != nil && (*mode == "off" || *mode == "prefer" || *mode == "require"):
		m.mode = *mode
	default:
		m.mode = "prefer"
	}
	return m.mode
}

// Required reports whether the link is required.
func (m *LinkMode) Required() bool { return m.Mode(context.Background()) == "require" }
