package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// Extensions (Plex, dizqueTV, Cloudflare Stream) are optional add-ons,
// not core. Their state lives in extensions(name PK, enabled, settings
// JSONB, updated_at); the catalogue below (version, description, settings
// schema) is code, because the table only stores operator choices.
//
// Secret settings are write-only: they are never returned, only reported
// as "set" (see SecretMask / Extension.SecretsSet). Since slot 0120 (story
// 32.6, README Q7) they are not in extensions.settings either: every
// `secret` field lives in home_secrets, sealed by the home's API
// (api/internal/secrets), which the machines' role cannot read. Code that
// needs one reads it through Handler.Secrets on the home.

// SettingField describes one setting of an extension.
type SettingField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // url | text | secret | json
	Required bool   `json:"required"`
	Help     string `json:"help,omitempty"`
	// HomeOnly: the value is used by the home's API only, never handed to
	// a job. Every secret is kept in home_secrets either way.
	HomeOnly bool `json:"home_only,omitempty"`
	// SecretName is the home_secrets name of a secret field; empty means
	// secrets.ExtensionName(extension, key).
	SecretName string `json:"-"`
}

type extensionDef struct {
	Name        string
	Label       string
	Version     string
	Description string
	Fields      []SettingField
}

// CloudflareExtension is the Cloudflare Stream integration's name.
const CloudflareExtension = "cloudflare_stream"

var extensionCatalog = []extensionDef{
	{
		Name: "dizquetv", Label: "dizqueTV", Version: "1", Description: "Publish Maktaba shows and channels to a dizqueTV server.",
		Fields: []SettingField{
			{Key: "url", Label: "dizqueTV URL", Type: "url", Required: true},
		},
	},
	{
		Name: "plex", Label: "Plex", Version: "1", Description: "Create and keep Plex collections in sync with detected seasons.",
		Fields: []SettingField{
			{Key: "url", Label: "Plex URL", Type: "url", Required: true},
			{Key: "token", Label: "Plex token", Type: "secret", Required: true},
			{Key: "path_map", Label: "Path map (Plex path to local path, JSON)", Type: "text"},
		},
	},
	{
		// Epic 32 story 32.6: the home's own Cloudflare account, for remote
		// viewing through Cloudflare Stream. The API token is used by the
		// home's API only (design §7.1); "Test connection" checks it.
		Name: CloudflareExtension, Label: "Cloudflare Stream", Version: "1",
		Description: "Remote viewing of Maktaba's channels through your own Cloudflare Stream account.",
		Fields: []SettingField{
			{Key: "account_id", Label: "Account ID", Type: "text", Required: true},
			{Key: "api_token", Label: "API token (Stream Write, Account Analytics Read)", Type: "secret",
				Required: true, HomeOnly: true, SecretName: "cf_api_token"},
			{Key: "customer_code", Label: "Customer subdomain code (customer-<code>.cloudflarestream.com)", Type: "text",
				Help: "Learned by Test connection when empty."},
		},
	},
}

// secretName is where a secret field of ext is kept in home_secrets.
func secretName(ext string, f SettingField) string {
	if f.SecretName != "" {
		return f.SecretName
	}
	return secrets.ExtensionName(ext, f.Key)
}

// ExtensionSecretFields is every extension's secret settings and their
// home_secrets names, for secrets.MigrateExtensionSecrets at start.
func ExtensionSecretFields() secrets.ExtensionSecretFields {
	out := secrets.ExtensionSecretFields{}
	for _, d := range extensionCatalog {
		for _, f := range d.Fields {
			if f.Type != "secret" {
				continue
			}
			if out[d.Name] == nil {
				out[d.Name] = map[string]string{}
			}
			out[d.Name][f.Key] = secretName(d.Name, f)
		}
	}
	return out
}

// ExtensionTester runs an extension's "Test connection"; its result is
// answered as is (it must carry ok and message).
type ExtensionTester func(ctx context.Context) (any, error)

// ExtensionValidator checks a settings change before it is saved (cur and
// next are the non-secret settings; secretChanged lists the secret keys
// being set or cleared). A non-nil error is answered (an *httperror.Error
// keeps its status).
type ExtensionValidator func(ctx context.Context, cur, next map[string]any, secretChanged []string) error

// SecretMask is shown in place of a stored secret and ignored on write
// (so a client can round-trip a form without clearing the secret).
const SecretMask = "********"

// Extension is the GET /api/extensions item.
type Extension struct {
	Name        string         `json:"name"`
	Label       string         `json:"label"`
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

// ExtensionSettings returns an extension's stored (non-secret) settings
// and whether it is enabled, for integrations living in other packages.
func (h *Handler) ExtensionSettings(ctx context.Context, name string) (map[string]any, bool, error) {
	rows, err := h.loadExtensionRows(ctx)
	if err != nil {
		return nil, false, err
	}
	r := rows[name]
	if r.settings == nil {
		r.settings = map[string]any{}
	}
	return r.settings, r.enabled, nil
}

// SaveExtensionSetting sets (or, with "", clears) one non-secret setting
// of an extension, keeping the rest (the Cloudflare test writes the
// customer code it learned).
func (h *Handler) SaveExtensionSetting(ctx context.Context, name, key, value string) error {
	h.extMu.Lock()
	defer h.extMu.Unlock()
	rows, err := h.loadExtensionRows(ctx)
	if err != nil {
		return err
	}
	cur := rows[name]
	if cur.settings == nil {
		cur.settings = map[string]any{}
	}
	if value == "" {
		delete(cur.settings, key)
	} else {
		cur.settings[key] = value
	}
	blob, _ := json.Marshal(cur.settings)
	_, err = h.DB.ExecContext(ctx, upsertExtensionSQL, name, cur.enabled, string(blob), h.now())
	return err
}

// ClearExtension removes an extension's settings and secrets and turns it
// off (removing the Cloudflare integration, story 32.6 AC5).
func (h *Handler) ClearExtension(ctx context.Context, name string) error {
	def, ok := findExtension(name)
	if !ok {
		return errors.New("unknown extension " + name)
	}
	h.extMu.Lock()
	defer h.extMu.Unlock()
	if h.Secrets != nil {
		for _, f := range def.Fields {
			if f.Type == "secret" {
				if err := h.Secrets.Delete(ctx, secretName(name, f)); err != nil {
					return err
				}
			}
		}
	}
	_, err := h.DB.ExecContext(ctx, upsertExtensionSQL, name, false, "{}", h.now())
	return err
}

func isSet(v any) bool {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	return v != nil
}

// buildExtension merges the catalogue entry with its stored row. Only
// schema keys are echoed (anything else stored might be a secret), and
// secret values are replaced by the mask. secretSet says which secret
// fields have a value in home_secrets; a value still in the settings
// (before the start-up move) counts too.
func buildExtension(def extensionDef, row extRow, secretSet map[string]bool) Extension {
	e := Extension{
		Name: def.Name, Label: def.Label, Version: def.Version, Description: def.Description,
		Enabled: row.enabled, Schema: def.Fields, Settings: map[string]any{},
		SecretsSet: []string{}, UpdatedAt: row.updated, Configured: true,
	}
	for _, f := range def.Fields {
		v, has := row.settings[f.Key]
		set := has && isSet(v)
		if f.Type == "secret" && secretSet[f.Key] {
			set = true
		}
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

// secretSet reports which of def's secret fields are in home_secrets.
func (h *Handler) secretSet(ctx context.Context, def extensionDef) map[string]bool {
	out := map[string]bool{}
	if h.Secrets == nil {
		return out
	}
	for _, f := range def.Fields {
		if f.Type != "secret" {
			continue
		}
		if ok, err := h.Secrets.Has(ctx, secretName(def.Name, f)); err == nil && ok {
			out[f.Key] = true
		}
	}
	return out
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
		out = append(out, buildExtension(d, rows[d.Name], h.secretSet(r.Context(), d)))
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
// keeps a secret; null or "" clears a key. Secret values go to
// home_secrets, never to the settings row.
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
	h.extMu.Lock()
	defer h.extMu.Unlock()
	rows, err := h.loadExtensionRows(r.Context())
	if err != nil {
		writeErr(w, r, "extensions", err)
		return
	}
	cur := rows[name]
	if cur.settings == nil {
		cur.settings = map[string]any{}
	}
	before := map[string]any{}
	for k, v := range cur.settings {
		before[k] = v
	}
	// secret changes: key -> new value; nil clears. Applied to home_secrets.
	secretChanges := map[string]*string{}
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
			if f.Type == "secret" {
				secretChanges[k] = nil
			}
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
			if f.Type == "secret" {
				secretChanges[k] = nil
			}
			continue
		}
		if f.Type == "url" && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
			httperror.Write(w, r, httperror.BadRequest("setting "+k+" must be an http(s) URL"))
			return
		}
		if f.Type == "secret" {
			val := s
			secretChanges[k] = &val
			continue
		}
		cur.settings[k] = s
	}
	enabled := cur.enabled
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	// a secret never stays in the settings row: one left there from before
	// the start-up move goes to home_secrets with this save
	for _, f := range def.Fields {
		if f.Type != "secret" {
			continue
		}
		legacy, ok := cur.settings[f.Key]
		if !ok {
			continue
		}
		delete(cur.settings, f.Key)
		if _, changing := secretChanges[f.Key]; changing || h.Secrets == nil {
			continue
		}
		if ls, _ := legacy.(string); strings.TrimSpace(ls) != "" {
			if has, _ := h.Secrets.Has(r.Context(), secretName(name, f)); !has {
				val := strings.TrimSpace(ls)
				secretChanges[f.Key] = &val
			}
		}
	}
	if len(secretChanges) > 0 && h.Secrets == nil {
		httperror.Write(w, r, &httperror.Error{Status: http.StatusServiceUnavailable,
			Title: "Service Unavailable", Detail: "the secrets store is not available on this server"})
		return
	}
	if v := h.Validators[name]; v != nil {
		changed := make([]string, 0, len(secretChanges))
		for k := range secretChanges {
			changed = append(changed, k)
		}
		sort.Strings(changed)
		if err := v(r.Context(), before, cur.settings, changed); err != nil {
			httperror.Write(w, r, err)
			return
		}
	}
	by := ""
	if p := principal.FromContext(r.Context()); p != nil {
		by = p.UserID
	}
	for _, f := range def.Fields {
		val, ok := secretChanges[f.Key]
		if !ok {
			continue
		}
		var serr error
		if val == nil {
			serr = h.Secrets.Delete(r.Context(), secretName(name, f))
		} else {
			serr = h.Secrets.Put(r.Context(), secretName(name, f), []byte(*val), by)
		}
		if serr != nil {
			writeErr(w, r, "save secret", serr)
			return
		}
	}
	blob, _ := json.Marshal(cur.settings)
	now := h.now()
	if _, err := h.DB.ExecContext(r.Context(), upsertExtensionSQL, name, enabled, string(blob), now); err != nil {
		writeErr(w, r, "save extension", err)
		return
	}
	ts := now.Format(tsLayout)
	common.WriteJSON(w, r, http.StatusOK,
		buildExtension(def, extRow{enabled: enabled, settings: cur.settings, updated: &ts}, h.secretSet(r.Context(), def)))
}

// TestExtension implements POST /api/extensions/{name}/test. An extension
// with a tester (Cloudflare Stream, story 32.6) runs it; the others
// answer {ok:false, message:"not_implemented..."} (the worker owns their
// client code).
func (h *Handler) TestExtension(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	if _, ok := findExtension(name); !ok {
		httperror.Write(w, r, httperror.NotFound("extension "+name))
		return
	}
	if t := h.Testers[name]; t != nil {
		res, err := t(r.Context())
		if err != nil {
			h.auditTest(r, name, map[string]any{"ok": false})
			common.WriteJSON(w, r, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		h.auditTest(r, name, res)
		common.WriteJSON(w, r, http.StatusOK, res)
		return
	}
	// 200 {ok:false} (queueApi.ts testExtension) rather than a 501 so
	// the UI can show the message inline.
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"ok": false, "message": "not_implemented: connection test is not available yet"})
}

// auditTest records a Test connection run in audit_log (category config):
// who ran it, the outcome and each failed check's key and code. Only those
// fields are copied out of the result, so no detail text (and never a
// secret) reaches the row. Best effort: a miss is ignored.
func (h *Handler) auditTest(r *http.Request, name string, res any) {
	if h.DB == nil {
		return
	}
	blob, err := json.Marshal(res)
	if err != nil {
		return
	}
	var out struct {
		OK    bool `json:"ok"`
		Lines []struct {
			Key      string `json:"key"`
			OK       bool   `json:"ok"`
			Optional bool   `json:"optional"`
			Skipped  bool   `json:"skipped"`
			Code     string `json:"code"`
		} `json:"lines"`
	}
	_ = json.Unmarshal(blob, &out)
	failed := []map[string]any{}
	warned := []string{}
	for _, l := range out.Lines {
		switch {
		case l.OK || l.Skipped:
		case l.Optional:
			warned = append(warned, l.Key)
		default:
			failed = append(failed, map[string]any{"key": l.Key, "code": l.Code})
		}
	}
	payload, _ := json.Marshal(map[string]any{"extension": name, "ok": out.OK, "failed": failed, "warned": warned})
	var actor any
	if p := principal.FromContext(r.Context()); p != nil {
		if _, perr := uuid.Parse(p.UserID); perr == nil {
			actor = p.UserID
		}
	}
	_, _ = h.DB.ExecContext(r.Context(), `INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
		VALUES ('config', 'extension_test', $1, $2, $3)`, actor, name, string(payload))
}
