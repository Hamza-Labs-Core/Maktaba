package plugins

// Plugin isolation levels (Epic 59 story 59.07, README §6.6). Each machine's server
// computes its level at start (full, or container without Landlock or user namespaces)
// and writes it to app_settings plugins.isolation, a map machine → level
// (cmd/maktaba-server/internal/plugins/isolation.go). On a Container-level machine
// third-party plugins wait (isolation_held) until an admin allows plugins with
// container-only isolation on this home: the policy's allow_container_isolation.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
)

// IsolationKey is the app_settings key the servers write their levels to.
const IsolationKey = "plugins.isolation"

// The levels.
const (
	IsolationFull      = "full"
	IsolationContainer = "container"
)

// MachineIsolation is one machine's level as its server wrote it.
type MachineIsolation struct {
	Level    string   `json:"level"`
	Missing  []string `json:"missing"`
	Landlock int      `json:"landlock"`
	UserNS   *bool    `json:"userns,omitempty"`
	Plugind  string   `json:"plugind,omitempty"`
}

// Isolation is GET /api/plugins/isolation's answer.
type Isolation struct {
	// Level is the home's: container when any machine is, full when every machine that
	// wrote one is, "" before any server wrote its level.
	Level string `json:"level"`
	// Machines are each machine's level, sorted by name.
	Machines []NamedIsolation `json:"machines"`
	// Allowed is the policy's allow_container_isolation; Held whether third-party
	// plugins wait for it now.
	Allowed bool `json:"allow_container_isolation"`
	Held    bool `json:"held"`
}

// NamedIsolation is a machine and its level.
type NamedIsolation struct {
	Machine string `json:"machine"`
	MachineIsolation
}

// LoadIsolation reads the machines' levels and the policy.
func LoadIsolation(ctx context.Context, q querier) (Isolation, error) {
	out := Isolation{Machines: []NamedIsolation{}}
	pol, err := LoadPolicy(ctx, q)
	if err != nil {
		return out, err
	}
	out.Allowed = pol.AllowContainerIsolation
	var raw []byte
	err = q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, IsolationKey).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	m := map[string]MachineIsolation{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return out, err
		}
	}
	for name, mi := range m {
		if mi.Missing == nil {
			mi.Missing = []string{}
		}
		out.Machines = append(out.Machines, NamedIsolation{Machine: name, MachineIsolation: mi})
		switch {
		case mi.Level == IsolationContainer:
			out.Level = IsolationContainer
		case mi.Level == IsolationFull && out.Level == "":
			out.Level = IsolationFull
		}
	}
	sort.Slice(out.Machines, func(i, j int) bool { return out.Machines[i].Machine < out.Machines[j].Machine })
	out.Held = out.Level == IsolationContainer && !out.Allowed
	return out, nil
}

// SetContainerIsolation turns Allow plugins with container-only isolation on or off for
// this home, audited (plugin.isolation). Turned off, the plugins it let run wait again
// (their gate, isolation_held): nothing is held by hand. Every plugin's desired row moves.
func (l *Lifecycle) SetContainerIsolation(ctx context.Context, allow bool, actor svc.Actor) (Isolation, error) {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return Isolation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	before, err := LoadPolicy(ctx, tx)
	if err != nil {
		return Isolation{}, err
	}
	after := before
	after.AllowContainerIsolation = allow
	b, _ := json.Marshal(after)
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()`, PolicyKey, string(b)); err != nil {
		return Isolation{}, err
	}
	if before != after {
		// the level the admin said yes (or no) to, beside who and when (story 59.16)
		iso, err := LoadIsolation(ctx, tx)
		if err != nil {
			return Isolation{}, err
		}
		if err := audit(ctx, tx, "plugin.isolation", PolicyKey, actor,
			map[string]bool{"allow_container_isolation": before.AllowContainerIsolation},
			map[string]bool{"allow_container_isolation": allow}, map[string]any{"level": iso.Level}); err != nil {
			return Isolation{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE components SET updated_by = $1 WHERE id LIKE 'plugin.%'`, actor.By); err != nil {
			return Isolation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Isolation{}, err
	}
	return LoadIsolation(ctx, l.DB)
}
