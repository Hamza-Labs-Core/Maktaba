package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsettings"
)

// A plugin's settings declared as data (story 54.11): its ui/settings.schema.json, drawn
// by the web's form, saved in app_settings plugins.<id> with plugins_version.<id>, checked
// here on every save. The pipeline passes them to the job (54.06: Run's settings).

// Settings is GET /api/plugins/{id}/settings.
type Settings struct {
	Schema  *pluginsettings.Schema       `json:"schema"`
	Values  map[string]any               `json:"values"`
	Strings map[string]map[string]string `json:"strings"`
	// Reset are the fields an update brought back to their default (their type changed).
	Reset []string `json:"reset,omitempty"`
}

func settingsKey(id string) string        { return "plugins." + id }
func settingsVersionKey(id string) string { return "plugins_version." + id }

// pluginWords are a plugin's own words, by language (en, ar).
func pluginWords(dir string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, lang := range []string{"en", "ar"} {
		out[lang] = map[string]string{}
		b, err := os.ReadFile(filepath.Join(dir, "i18n", lang+".json"))
		if err != nil {
			continue
		}
		var words map[string]any
		if json.Unmarshal(b, &words) != nil {
			continue
		}
		for k, v := range words {
			if s, ok := v.(string); ok {
				out[lang][k] = s
			}
		}
	}
	return out
}

// schemaOf is an installed plugin's settings schema (nil: it has none).
func schemaOf(in *installRow) (*pluginsettings.Schema, error) {
	var m struct {
		Settings *struct {
			Schema string `json:"schema"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(in.Manifest, &m); err != nil {
		return nil, err
	}
	if m.Settings == nil || m.Settings.Schema == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(in.Path, filepath.FromSlash(m.Settings.Schema)))
	if err != nil {
		return nil, err
	}
	return pluginsettings.Parse(raw, pluginWords(in.Path))
}

func storedValues(ctx context.Context, q querier, id string) (map[string]any, []string, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, settingsKey(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	vals := map[string]any{}
	_ = json.Unmarshal(raw, &vals)
	var ver struct {
		Reset []string `json:"reset"`
	}
	var vraw []byte
	if q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, settingsVersionKey(id)).Scan(&vraw) == nil {
		_ = json.Unmarshal(vraw, &ver)
	}
	return vals, ver.Reset, nil
}

// GetSettings is a plugin's settings: its schema, its values (the defaults for what was
// never saved) and its words for the form.
func (l *Lifecycle) GetSettings(ctx context.Context, id string) (*Settings, error) {
	in, err := loadInstall(ctx, l.DB, id, false)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, problem(http.StatusNotFound, CodeNotInstalled, "%s is not installed", id)
	}
	s, err := schemaOf(in)
	if err != nil {
		return nil, err
	}
	out := &Settings{Values: map[string]any{}, Strings: pluginWords(in.Path)}
	if s == nil {
		return out, nil
	}
	stored, reset, err := storedValues(ctx, l.DB, id)
	if err != nil {
		return nil, err
	}
	out.Schema = s
	out.Values, _ = s.Carry(stored)
	out.Reset = reset
	return out, nil
}

// PutSettings saves a plugin's settings (story 54.11 §2): checked against the installed
// version's schema (400 settings_invalid), stored with their version in one transaction,
// audited, and the plugin's desired row moved (its stages' fingerprints include them).
func (l *Lifecycle) PutSettings(ctx context.Context, id string, values map[string]any, actor svc.Actor) (*Settings, error) {
	in, err := loadInstall(ctx, l.DB, id, false)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, problem(http.StatusNotFound, CodeNotInstalled, "%s is not installed", id)
	}
	s, err := schemaOf(in)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, problem(http.StatusBadRequest, pluginsettings.CodeInvalid, "this plugin has no settings")
	}
	g, err := loadGrantSet(ctx, l.DB, id)
	if err != nil {
		return nil, err
	}
	var libs []string
	if g != nil && !g.AllLibraries {
		libs = g.Libraries
	}
	if libs == nil {
		libs = []string{}
	}
	if g != nil && g.AllLibraries {
		libs = nil // every library: not checked here
	}
	checked, err := s.Check(values, libs)
	var se *pluginsettings.Error
	if errors.As(err, &se) {
		p := problem(http.StatusBadRequest, se.Code, "%s", se.Detail)
		p.Params = map[string]any{"field": se.Field}
		return nil, p
	}
	if err != nil {
		return nil, err
	}
	if err := l.writeSettings(ctx, in, checked, nil, actor); err != nil {
		return nil, err
	}
	return l.GetSettings(ctx, id)
}

func (l *Lifecycle) writeSettings(ctx context.Context, in *installRow, values map[string]any, reset []string, actor svc.Actor) error {
	var m manifestOf
	_ = json.Unmarshal(in.Manifest, &m)
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	before, _, err := storedValues(ctx, tx, in.ID)
	if err != nil {
		return err
	}
	vb, _ := json.Marshal(values)
	ver, _ := json.Marshal(map[string]any{"version": in.Version, "data_version": m.DataVersion, "reset": reset})
	for _, kv := range [][2]string{{settingsKey(in.ID), string(vb)}, {settingsVersionKey(in.ID), string(ver)}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	if err := audit(ctx, tx, "plugin.settings", components.PluginPrefix+in.ID, actor, before, values, nil); err != nil {
		return err
	}
	if err := touch(ctx, tx, in.ID, actor.By); err != nil {
		return err
	}
	return tx.Commit()
}

// carrySettings brings saved settings to a new version's schema after an update (story
// 54.11 §2): new fields take their defaults, removed ones go, a changed type is reset and
// named. A plugin never saved keeps no row.
func (l *Lifecycle) carrySettings(ctx context.Context, id string, actor svc.Actor) {
	in, err := loadInstall(ctx, l.DB, id, false)
	if err != nil || in == nil {
		return
	}
	s, err := schemaOf(in)
	if err != nil || s == nil {
		return
	}
	stored, _, err := storedValues(ctx, l.DB, id)
	if err != nil || len(stored) == 0 {
		return
	}
	values, reset := s.Carry(stored)
	if err := l.writeSettings(ctx, in, values, reset, actor); err != nil {
		l.logger().Warn("plugin_settings_not_carried", "plugin", id, "err", err)
	}
}
