// Package devices implements Story 7.22 device-registration routes:
//
//	POST   /api/devices/register
//	DELETE /api/devices/{id}
//	GET    /api/devices
//
// Tokens are stored as opaque strings; the APNs/FCM bridge consumes
// them via Notify (Story 18.x). On a failed delivery the bridge soft-
// revokes by setting “revoked_at“ rather than deleting the row.
package devices

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// RegisterRequest is the POST body.
type RegisterRequest struct {
	Platform   string `json:"platform"`
	PushToken  string `json:"push_token"`
	BundleID   string `json:"bundle_id"`
	AppVersion string `json:"app_version,omitempty"`
	Locale     string `json:"locale,omitempty"`
}

// Device is the API shape.
type Device struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Platform     string    `json:"platform"`
	PushToken    string    `json:"push_token"`
	BundleID     string    `json:"bundle_id"`
	AppVersion   *string   `json:"app_version,omitempty"`
	Locale       *string   `json:"locale,omitempty"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// Handler bundles deps.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
}

func (h *Handler) Mount(r chi.Router) {
	r.Post("/api/devices/register", h.Register)
	r.Delete("/api/devices/{id}", h.Unregister)
	r.Get("/api/devices", h.List)
}

// Register implements AC-1 + AC-5: upsert by (user, platform, push_token);
// soft-revoke any prior row for the same (user, platform, bundle_id).
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req RegisterRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	if req.Platform != "ios" && req.Platform != "android" && req.Platform != "web" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "platform", Message: "must be one of ios, android, web"},
		}))
		return
	}
	if req.PushToken == "" || req.BundleID == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "push_token", Message: "required"},
			{Field: "bundle_id", Message: "required"},
		}))
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx"))
		return
	}
	defer func() { _ = tx.Rollback() }()

	now := h.now()
	// AC-5: revoke any prior row for the (user, platform, bundle_id) tuple
	// that does *not* match this push_token.
	_, _ = tx.ExecContext(r.Context(), `
		UPDATE devices SET revoked_at = $4
		WHERE user_id = $1 AND platform = $2 AND bundle_id = $3
		  AND push_token <> $5 AND revoked_at IS NULL
	`, p.UserID, req.Platform, req.BundleID, now, req.PushToken)

	// Check for existing row.
	var existingID string
	err = tx.QueryRowContext(r.Context(), `
		SELECT id FROM devices WHERE user_id=$1 AND platform=$2 AND push_token=$3
	`, p.UserID, req.Platform, req.PushToken).Scan(&existingID)
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		existingID = uuid.NewString()
		created = true
		_, err = tx.ExecContext(r.Context(), `
			INSERT INTO devices (id, user_id, platform, push_token, bundle_id, app_version, locale, registered_at, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
		`, existingID, p.UserID, req.Platform, req.PushToken, req.BundleID,
			nullIfEmpty(req.AppVersion), nullIfEmpty(req.Locale), now)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("insert device: "+err.Error()))
			return
		}
	} else if err != nil {
		httperror.Write(w, r, httperror.Internal("query device: "+err.Error()))
		return
	} else {
		_, err = tx.ExecContext(r.Context(), `
			UPDATE devices SET app_version=$1, locale=$2, last_seen_at=$3, revoked_at=NULL
			WHERE id = $4
		`, nullIfEmpty(req.AppVersion), nullIfEmpty(req.Locale), now, existingID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("update device: "+err.Error()))
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit"))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	common.WriteJSON(w, r, status, map[string]any{"id": existingID, "created": created})
}

// Unregister soft-revokes (AC-3).
func (h *Handler) Unregister(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "id")
	res, err := h.DB.ExecContext(r.Context(), `
		UPDATE devices SET revoked_at=$1 WHERE id=$2 AND user_id=$3 AND revoked_at IS NULL
	`, h.now(), id, p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("unregister"))
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httperror.Write(w, r, httperror.NotFound("device "+id))
		return
	}
	common.WriteNoContent(w)
}

// List returns active devices for the principal.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, user_id, platform, push_token, bundle_id, app_version, locale, registered_at, last_seen_at
		FROM devices WHERE user_id = $1 AND revoked_at IS NULL ORDER BY last_seen_at DESC
	`, p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list devices"))
		return
	}
	defer rows.Close()
	items := []Device{}
	for rows.Next() {
		var d Device
		var av, loc sql.NullString
		if err := rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.PushToken, &d.BundleID, &av, &loc, &d.RegisteredAt, &d.LastSeenAt); err != nil {
			continue
		}
		if av.Valid {
			d.AppVersion = &av.String
		}
		if loc.Valid {
			d.Locale = &loc.String
		}
		items = append(items, d)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}
