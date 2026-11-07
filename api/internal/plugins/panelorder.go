package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
)

// The admin's order of plugin panels (story 54.20 §1: "at most 3 panels per page,
// admin-ordered"). The order is a list of plugin ids in app_settings; a plugin with panels
// that is not in it comes after, in install order. Pages show the first three that have
// something for the subject.

// PanelOrderKey is the order's app_settings key.
const PanelOrderKey = "plugins.panel_order"

// MaxPanelOrder is the most plugins one order lists.
const MaxPanelOrder = 200

// PanelPlugin is an installed plugin with panels, as the order page lists it.
type PanelPlugin struct {
	ID    string   `json:"id"`
	Names Names    `json:"names"`
	On    []string `json:"on"`
}

// LoadPanelOrder reads the order (empty when none was set).
func LoadPanelOrder(ctx context.Context, q querier) ([]string, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, PanelOrderKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var v struct {
		Order []string `json:"order"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Order == nil {
		return []string{}, nil
	}
	return v.Order, nil
}

// orderBy sorts installed plugin ids by the admin's order, the rest after in the order
// given (install order).
func orderBy(ids, order []string) []string {
	rank := map[string]int{}
	for i, id := range order {
		if _, ok := rank[id]; !ok {
			rank[id] = i
		}
	}
	out := append([]string(nil), ids...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, iok := rank[out[i]]
		rj, jok := rank[out[j]]
		switch {
		case iok && jok:
			return ri < rj
		case iok != jok:
			return iok
		}
		return false
	})
	return out
}

// PanelPlugins is every installed plugin that declares a panel, in the admin's order.
func (l *Lifecycle) PanelPlugins(ctx context.Context) ([]PanelPlugin, []string, error) {
	order, err := LoadPanelOrder(ctx, l.DB)
	if err != nil {
		return nil, nil, err
	}
	rows, err := l.DB.QueryContext(ctx, `SELECT plugin_id FROM plugin_installs ORDER BY installed_at, plugin_id`)
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	out := []PanelPlugin{}
	for _, id := range orderBy(ids, order) {
		in, err := loadInstall(ctx, l.DB, id, false)
		if err != nil || in == nil {
			continue
		}
		panels := panelsOf(in)
		if len(panels) == 0 {
			continue
		}
		m, _ := parseManifest(in.Manifest)
		p := PanelPlugin{ID: id, Names: namesOf(in.Path, m), On: []string{}}
		for _, pn := range panels {
			p.On = append(p.On, pn.On)
		}
		out = append(out, p)
	}
	return out, order, nil
}

// SetPanelOrder writes the order, audited. Ids of plugins not installed are kept (an
// uninstall and a reinstall keep its place); duplicates and empty ids are refused.
func (l *Lifecycle) SetPanelOrder(ctx context.Context, order []string, actor svc.Actor) ([]string, error) {
	if len(order) > MaxPanelOrder {
		return nil, problem(400, "plugin_invalid", "at most %d plugins", MaxPanelOrder)
	}
	seen := map[string]bool{}
	for _, id := range order {
		if id == "" || seen[id] || len(id) > 128 {
			return nil, problem(400, "plugin_invalid", "each plugin id once")
		}
		seen[id] = true
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	before, err := LoadPanelOrder(ctx, tx)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(map[string]any{"order": order})
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()`, PanelOrderKey, string(b)); err != nil {
		return nil, err
	}
	if err := audit(ctx, tx, "plugin.panel_order", PanelOrderKey, actor, before, order, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return order, nil
}
