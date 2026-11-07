package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// ReportLogsKey is the app setting that names the plugins whose log lines a problem report
// may carry (story 54.09 §3, README §9 T2): a JSON list of plugin ids. A plugin's lines
// (service plugin.<id>) are left out of every report unless an admin ticked "Include its
// logs in problem reports" on its page. The logs handler reads the same key.
const ReportLogsKey = "plugins_report_logs"

// ReportLogPlugins are the plugins whose lines problem reports carry.
func ReportLogPlugins(ctx context.Context, q querier) (map[string]bool, error) {
	var raw sql.NullString
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, ReportLogsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !raw.Valid) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw.String), &ids); err != nil {
		return map[string]bool{}, nil
	}
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// SetReportLogs ticks or clears a plugin's "Include its logs in problem reports", audited.
func (l *Lifecycle) SetReportLogs(ctx context.Context, id string, include bool, actor svc.Actor) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	in, err := loadInstall(ctx, tx, id, false)
	if err != nil {
		return err
	}
	if in == nil {
		return problem(http.StatusNotFound, CodeNotInstalled, "%s is not installed", id)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, ReportLogsKey); err != nil {
		return err
	}
	set, err := ReportLogPlugins(ctx, tx)
	if err != nil {
		return err
	}
	before := set[id]
	if include {
		set[id] = true
	} else {
		delete(set, id)
	}
	ids := make([]string, 0, len(set))
	for k := range set {
		ids = append(ids, k)
	}
	slices.Sort(ids)
	b, _ := json.Marshal(ids)
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()`, ReportLogsKey, string(b)); err != nil {
		return err
	}
	if err := audit(ctx, tx, "plugin.report_logs", components.PluginPrefix+id, actor, before, include, nil); err != nil {
		return err
	}
	return tx.Commit()
}
