package serversettings

// The library database's backups (Epic 36 story 36.16), for the web's Backups
// section:
//
//	GET    /api/server/backups               the settings ([backups] in server.toml), the
//	                                         dumps there are, their disk's free space
//	PUT    /api/server/backups               a partial change, with move_dumps when the
//	                                         folder changes (422 {errors, dumps, from})
//	DELETE /api/server/backups/dumps/{name}  one dump
//
// They proxy the supervisor's /config/backups: the server reads [backups] before its
// database step. A change is kept in app_settings too, like every other rule
// (rules.backups; update_keep is rules.updates.backups_keep). Admins only (403 before
// the supervisor is called); every change is audited (category 'server').

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Audit actions of the backups.
const (
	ActionBackupsChanged = "backups-changed"
	ActionBackupDeleted  = "backup-deleted"
)

// BackupsSettingsKey is the app_settings row the backups' settings are kept in.
const BackupsSettingsKey = "rules.backups"

// updatesSettingsKey is rules.updates' row (its backups_keep is update_keep).
const updatesSettingsKey = "rules.updates"

func (h *Handler) mountBackups(r chi.Router) {
	r.Get("/api/server/backups", h.admin(h.storageGet("backups")))
	r.Put("/api/server/backups", h.admin(h.backupsPut))
	r.Delete("/api/server/backups/dumps/{name}", h.admin(h.backupDelete))
}

func (h *Handler) backupsPut(w http.ResponseWriter, r *http.Request) {
	if h.Supervisor == nil {
		httperror.Write(w, r, httperror.Conflict("no-supervisor", errNoSupervisor.Error()))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, common.MaxBodyBytes))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("body too large"))
		return
	}
	var change map[string]json.RawMessage
	if err := json.Unmarshal(body, &change); err != nil {
		httperror.Write(w, r, httperror.BadRequest("the settings are not a JSON object"))
		return
	}
	code, out, err := h.storageSupervisor().call(r.Context(), http.MethodPut, "backups", body)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("the server's supervisor did not answer").Wrap(err))
		return
	}
	if code == http.StatusOK {
		var doc struct {
			Saved map[string]any `json:"saved"`
		}
		if json.Unmarshal(out, &doc) == nil && doc.Saved != nil {
			h.keepSetting(r.Context(), BackupsSettingsKey, doc.Saved)
			if raw, ok := change["update_keep"]; ok {
				var n int
				if json.Unmarshal(raw, &n) == nil && n > 0 {
					h.mergeSetting(r.Context(), updatesSettingsKey, "backups_keep", n)
				}
			}
		}
		delete(change, "move_dumps")
		h.audit(r, ActionBackupsChanged, map[string]any{"changed": change})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(out)
}

func (h *Handler) backupDelete(w http.ResponseWriter, r *http.Request) {
	if h.Supervisor == nil {
		httperror.Write(w, r, httperror.Conflict("no-supervisor", errNoSupervisor.Error()))
		return
	}
	name := chi.URLParam(r, "name")
	code, out, err := h.storageSupervisor().call(r.Context(), http.MethodDelete, "backups/dumps/"+url.PathEscape(name), nil)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("the server's supervisor did not answer").Wrap(err))
		return
	}
	if code == http.StatusOK {
		h.audit(r, ActionBackupDeleted, map[string]any{"name": name})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(out)
}

// keepSetting writes v as JSON under key in app_settings (best effort: the file the
// server reads is the one that counts).
func (h *Handler) keepSetting(ctx context.Context, key string, v any) {
	if h.DB == nil {
		return
	}
	b, _ := json.Marshal(v)
	_, _ = h.DB.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		key, string(b), time.Now().UTC())
}

// mergeSetting sets field in the JSON object kept under key (made when missing).
func (h *Handler) mergeSetting(ctx context.Context, key, field string, v any) {
	if h.DB == nil {
		return
	}
	obj := map[string]any{}
	var raw string
	if err := h.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &obj)
	}
	obj[field] = v
	h.keepSetting(ctx, key, obj)
}
