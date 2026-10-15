package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Extensions (Plex, dizqueTV) are optional add-ons, not core. Their
// state lives in extensions(name PK, enabled, settings JSONB,
// updated_at); the catalogue below (version, description, settings
// schema) is code, because the table only stores operator choices.
// Secret settings are write-only: they are never returned, only
// reported as "set" (see SecretMask / Extension.SecretsSet).

// SettingField describes one setting of an extension.
type SettingField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // url | text | secret | json
	Required bool   `json:"required"`
}

type extensionDef struct {
	Name        string
	Version     string
	Description string
	Fields      []SettingField
}

var extensionCatalog = []extensionDef{
	{
		Name: "dizquetv", Version: "1", Description: "Publish Maktaba shows and channels to a dizqueTV server.",
		Fields: []SettingField{
			{Key: "url", Label: "dizqueTV URL", Type: "url", Required: true},
		},
	},
	{
		Name: "plex", Version: "1", Description: "Create and keep Plex collections in sync with detected seasons.",
		Fields: []SettingField{
			{Key: "url", Label: "Plex URL", Type: "url", Required: true},
			{Key: "token", Label: "Plex token", Type: "secret", Required: true},
			{Key: "path_map", Label: "Path map (Plex path to local path, JSON)", Type: "json"},
		},
	},
}

// SecretMask is shown in place of a stored secret and ignored on write
// (so a client can round-trip a form without clearing the secret).
const SecretMask = "********"

// Extension is the GET /api/extensions item.
type Extension struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Enabled     bool           `json:"enabled"`
	Configured  bool           `json:"configured"`
	Schema      []SettingField `json:"settings_schema"`
	Settings    map[string]any `json:"settings"`
	SecretsSet  []string       `json:"secrets_set"`
	UpdatedAt   *string        `json:"updated_at"`
}

const extensionsSQL = `SELECT name, enabled, settings, updated_at FROM extensions`

type extRow struct {
	enabled  bool
	settings map[string]any
	updated  *string
}

const tsLayout = "2006-01-02T15:04:05Z07:00"

func (h *Handler) loadExtensionRows(ctx context.Context) (map[string]extRow, error) {
	rows, err := h.DB.QueryContext(ctx, extensionsSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]extRow{}
	for rows.Next() {
		var name string
		var r extRow
		var raw []byte
		var at sql.NullTime
		if err := rows.Scan(&name, &r.enabled, &raw, &at); err != nil {
			return nil, err
		}
		r.settings = map[string]any{}
		_ = json.Unmarshal(raw, &r.settings)
		if r.settings == nil {
			r.settings = map[string]any{}
		}
		if at.Valid {
			s := at.Time.UTC().Format(tsLayout)
			r.updated = &s
		}
		out[name] = r
	}
	return out, rows.Err()
}

func isSet(v any) bool {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	return v != nil
}

// buildExtension merges the catalogue entry with its stored row. Only
// schema keys are echoed (anything else stored might be a secret), and
// secret values are replaced by the mask.
func buildExtension(def extensionDef, row extRow) Extension {
	e := Extension{
		Name: def.Name, Version: def.Version, Description: def.Description,
		Enabled: row.enabled, Schema: def.Fields, Settings: map[string]any{},
		SecretsSet: []string{}, UpdatedAt: row.updated, Configured: true,
	}
	for _, f := range def.Fields {
		v, has := row.settings[f.Key]
		set := has && isSet(v)
		if f.Required && !set {
			e.Configured = false
		}
		switch {
		case f.Type == "secret":
			if set {
				e.Settings[f.Key] = SecretMask
				e.SecretsSet = append(e.SecretsSet, f.Key)
			} else {
				e.Settings[f.Key] = ""
			}
		case set:
			e.Settings[f.Key] = v
		default:
			e.Settings[f.Key] = ""
		}
	}
	return e
}

func findExtension(name string) (extensionDef, bool) {
	for _, d := range extensionCatalog {
		if d.Name == name {
			return d, true
		}
	}
	return extensionDef{}, false
}

// Extensions implements GET /api/extensions.
func (h *Handler) Extensions(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	rows, err := h.loadExtensionRows(r.Context())
	if err != nil {
		writeErr(w, r, "extensions", err)
		return
	}
	out := make([]Extension, 0, len(extensionCatalog))
	for _, d := range extensionCatalog {
		out = append(out, buildExtension(d, rows[d.Name]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"extensions": out})
}

type putExtensionBody struct {
	Enabled  *bool                      `json:"enabled"`
	Settings map[string]json.RawMessage `json:"settings"`
}

const upsertExtensionSQL = `
INSERT INTO extensions (name, enabled, settings, updated_at)
VALUES ($1, $2, $3::jsonb, $4)
ON CONFLICT (name) DO UPDATE
   SET enabled = EXCLUDED.enabled, settings = EXCLUDED.settings, updated_at = EXCLUDED.updated_at`

// PutExtension implements PUT /api/extensions/{name}. Settings are
// merged key by key: omitted keys keep their value; the mask string
// keeps a secret; null or "" clears a key.
func (h *Handler) PutExtension(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	def, ok := findExtension(name)
	if !ok {
		httperror.Write(w, r, httperror.NotFound("extension "+name))
		return
	}
	var body putExtensionBody
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if body.Enabled == nil && body.Settings == nil {
		httperror.Write(w, r, httperror.BadRequest("nothing to change: send enabled and/or settings"))
		return
	}
	fields := map[string]SettingField{}
	for _, f := range def.Fields {
		fields[f.Key] = f
	}
	rows, err := h.loadExtensionRows(r.Context())
	if err != nil {
		writeErr(w, r, "extensions", err)
		return
	}
	cur := rows[name]
	if cur.settings == nil {
		cur.settings = map[string]any{}
	}
	for k, raw := range body.Settings {
		f, known := fields[k]
		if !known {
			httperror.Write(w, r, httperror.BadRequest("unknown setting "+k))
			return
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			httperror.Write(w, r, httperror.BadRequest("setting "+k+" is not valid JSON"))
			return
		}
		if v == nil {
			delete(cur.settings, k)
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			httperror.Write(w, r, httperror.BadRequest("setting "+k+" must be a string"))
			return
		}
		if f.Type == "secret" && s == SecretMask {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			delete(cur.settings, k)
			continue
		}
		if f.Type == "url" && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
			httperror.Write(w, r, httperror.BadRequest("setting "+k+" must be an http(s) URL"))
			return
		}
		if f.Type == "json" && !json.Valid([]byte(s)) {
			httperror.Write(w, r, httperror.BadRequest("setting "+k+" must be valid JSON"))
			return
		}
		cur.settings[k] = s
	}
	enabled := cur.enabled
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	blob, _ := json.Marshal(cur.settings)
	now := h.now()
	if _, err := h.DB.ExecContext(r.Context(), upsertExtensionSQL, name, enabled, string(blob), now); err != nil {
		writeErr(w, r, "save extension", err)
		return
	}
	ts := now.Format(tsLayout)
	common.WriteJSON(w, r, http.StatusOK,
		buildExtension(def, extRow{enabled: enabled, settings: cur.settings, updated: &ts}))
}

// TestExtension implements POST /api/extensions/{name}/test. The
// connection check is not implemented yet (the worker owns the client
// code); it answers 501 so the UI can show a disabled "Test".
func (h *Handler) TestExtension(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	if _, ok := findExtension(name); !ok {
		httperror.Write(w, r, httperror.NotFound("extension "+name))
		return
	}
	httperror.Write(w, r, &httperror.Error{
		Type: "https://maktaba.dev/problems/not-implemented", Title: "not implemented",
		Status: http.StatusNotImplemented, Detail: "connection test is not implemented yet",
	})
}
