package plugins

import (
	"context"
	"database/sql"
	"log/slog"
	"sort"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// The API's catalog with the installed plugins' entries (story 54.37 §2): read from
// plugin_installs at start, after the API's own install writes, and on NOTIFY
// plugin_installs_changed; swapped whole (components.SetLive) so the effect walk, GET
// /api/components and every other reader never see half a set.

// LoadSpecs reads every installed plugin's spec, sorted by id.
func LoadSpecs(ctx context.Context, db *sql.DB) ([]components.PluginSpec, error) {
	rows, err := db.QueryContext(ctx, `SELECT plugin_id, manifest FROM plugin_installs ORDER BY plugin_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []components.PluginSpec
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		s, err := components.PluginSpecOf(raw, "")
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, rows.Err()
}

// ReloadCatalog swaps the live catalog for the static one with the installed plugins'
// entries; an entry the catalog refuses is left out and logged.
func ReloadCatalog(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	specs, err := LoadSpecs(ctx, db)
	if err != nil {
		return err
	}
	cat, skipped := components.WithPlugins(components.Default(), specs)
	for _, s := range skipped {
		if logger != nil {
			logger.Warn("plugin_left_out_of_the_catalog", "detail", s, "event", "plugin_catalog_skipped")
		}
	}
	components.SetLive(cat)
	return nil
}

// WatchCatalog keeps the live catalog current until ctx ends: a read at once, then on
// each plugin_installs_changed (and every minute, should a notification be lost).
func WatchCatalog(ctx context.Context, db *sql.DB, dsn string, logger *slog.Logger) {
	var notify <-chan *pq.Notification
	if dsn != "" {
		l := pq.NewListener(dsn, time.Second, 30*time.Second, nil)
		if err := l.Listen("plugin_installs_changed"); err == nil {
			notify = l.Notify
		}
		defer func() { _ = l.Close() }()
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := ReloadCatalog(ctx, db, logger); err != nil && ctx.Err() == nil && logger != nil {
			logger.Debug("plugin_catalog_unreadable", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-notify:
		case <-t.C:
		}
	}
}
