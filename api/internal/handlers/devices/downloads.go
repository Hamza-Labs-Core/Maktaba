// Story 12.11 — downloaded-flag sync.
//
//	POST   /api/videos/{video_id}/downloaded   set the flag for the
//	                                            calling device
//	DELETE /api/videos/{video_id}/downloaded   clear it
//	PATCH  /api/videos/{video_id}/downloaded   update metadata
//	GET    /api/videos/{video_id}/downloaded   list devices that hold
//	                                            an offline copy
//
// These are metadata-only: the server never stores the media bytes. The
// flag set is keyed by the registered device making the call, identified
// by the `X-Device-ID` header (the device row created via
// POST /api/devices/register). Story 12.11's `403 not-a-device-session`
// AC ideally keys off a device-bound auth token; this repo's auth model
// (api/internal/auth/principal) has no device-session concept, so the
// device identity is taken from the header + verified to belong to the
// authenticated user. See the gap report for the deferred auth-model
// dependency.
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

// allowedQualities is the closed quality vocabulary for a downloaded
// copy. Empty quality is allowed (caller did not specify).
var allowedQualities = map[string]bool{
	"":       true,
	"audio":  true,
	"sd":     true,
	"hd":     true,
	"fhd":    true,
	"source": true,
}

// DownloadedRequest is the POST/PATCH body. All fields optional —
// presence of a device session + video is what records the flag.
type DownloadedRequest struct {
	Quality   string `json:"quality,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Checksum  string `json:"checksum,omitempty"`
}

// validateDownloadedRequest enforces the closed quality vocabulary and a
// non-negative size. Pure + exported for unit testing without a DB.
func validateDownloadedRequest(req DownloadedRequest) error {
	q := strings.ToLower(strings.TrimSpace(req.Quality))
	if !allowedQualities[q] {
		return errors.New("unknown quality: " + req.Quality)
	}
	if req.SizeBytes < 0 {
		return errors.New("size_bytes must be non-negative")
	}
	return nil
}

// deviceIDFromRequest extracts and validates the device identity for a
// downloaded-flag call. Returns ("", false) when the caller is not a
// device session (Story 12.11 `403 not-a-device-session`).
func deviceIDFromRequest(r *http.Request) (string, bool) {
	id := strings.TrimSpace(r.Header.Get("X-Device-ID"))
	if id == "" {
		return "", false
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

// MountDownloads registers the Story 12.11 routes. Wired separately from
// Mount so deployments without slot 0063 can still serve device
// registration.
func (h *Handler) MountDownloads(r chi.Router) {
	r.Post("/api/videos/{video_id}/downloaded", h.SetDownloaded)
	r.Patch("/api/videos/{video_id}/downloaded", h.SetDownloaded)
	r.Delete("/api/videos/{video_id}/downloaded", h.ClearDownloaded)
	r.Get("/api/videos/{video_id}/downloaded", h.ListDownloaded)
}

// requireDeviceSession resolves the calling device, verifying it belongs
// to the authenticated principal and is not revoked. Writes the error
// response and returns ("", false) on failure.
func (h *Handler) requireDeviceSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return "", false
	}
	devID, ok := deviceIDFromRequest(r)
	if !ok {
		httperror.Write(w, r, httperror.Forbidden("not-a-device-session",
			"X-Device-ID header required (registered device session)"))
		return "", false
	}
	var owner string
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT user_id FROM devices WHERE id=$1 AND revoked_at IS NULL`, devID).
		Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != p.UserID) {
		httperror.Write(w, r, httperror.Forbidden("not-a-device-session",
			"device not registered to this user"))
		return "", false
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("device lookup"))
		return "", false
	}
	return devID, true
}

func videoID(r *http.Request) (string, bool) {
	id := chi.URLParam(r, "video_id")
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

// SetDownloaded upserts the (device, video) downloaded flag (POST/PATCH).
func (h *Handler) SetDownloaded(w http.ResponseWriter, r *http.Request) {
	devID, ok := h.requireDeviceSession(w, r)
	if !ok {
		return
	}
	vid, ok := videoID(r)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("malformed video_id"))
		return
	}
	var req DownloadedRequest
	if r.ContentLength != 0 {
		if e := common.ReadJSON(r, &req, 2<<10); e != nil {
			httperror.Write(w, r, e)
			return
		}
	}
	if err := validateDownloadedRequest(req); err != nil {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "quality", Message: err.Error()},
		}))
		return
	}
	now := h.now()
	_, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO device_downloads (device_id, video_id, quality, size_bytes, checksum, created_at, updated_at, revoked)
		VALUES ($1, $2, $3, $4, $5, $6, $6, FALSE)
		ON CONFLICT (device_id, video_id) DO UPDATE
		   SET quality=EXCLUDED.quality, size_bytes=EXCLUDED.size_bytes,
		       checksum=EXCLUDED.checksum, updated_at=EXCLUDED.updated_at,
		       revoked=FALSE
	`, devID, vid, nullIfEmpty(strings.ToLower(strings.TrimSpace(req.Quality))),
		req.SizeBytes, nullIfEmpty(req.Checksum), now)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("set downloaded: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"video_id": vid, "device_id": devID, "downloaded": true,
	})
}

// ClearDownloaded removes the flag (DELETE) → 204.
func (h *Handler) ClearDownloaded(w http.ResponseWriter, r *http.Request) {
	devID, ok := h.requireDeviceSession(w, r)
	if !ok {
		return
	}
	vid, ok := videoID(r)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("malformed video_id"))
		return
	}
	_, err := h.DB.ExecContext(r.Context(),
		`DELETE FROM device_downloads WHERE device_id=$1 AND video_id=$2`, devID, vid)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("clear downloaded"))
		return
	}
	common.WriteNoContent(w)
}

// DownloadEntry is the GET row shape (metadata only).
type DownloadEntry struct {
	DeviceID  string    `json:"device_id"`
	Quality   *string   `json:"quality,omitempty"`
	SizeBytes *int64    `json:"size_bytes,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	Revoked   bool      `json:"revoked"`
}

// ListDownloaded returns the devices (owned by the caller) that hold an
// offline copy of the video. Revoked-device rows are retained and
// surfaced with revoked=true (Story 12.11 retention AC).
func (h *Handler) ListDownloaded(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	vid, ok := videoID(r)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("malformed video_id"))
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT dd.device_id, dd.quality, dd.size_bytes, dd.updated_at,
		       (dd.revoked OR d.revoked_at IS NOT NULL) AS revoked
		FROM device_downloads dd
		JOIN devices d ON d.id = dd.device_id
		WHERE dd.video_id = $1 AND d.user_id = $2
		ORDER BY dd.updated_at DESC
	`, vid, p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list downloaded: "+err.Error()))
		return
	}
	defer rows.Close()
	items := []DownloadEntry{}
	for rows.Next() {
		var e DownloadEntry
		var q sql.NullString
		var sz sql.NullInt64
		if err := rows.Scan(&e.DeviceID, &q, &sz, &e.UpdatedAt, &e.Revoked); err != nil {
			continue
		}
		if q.Valid {
			e.Quality = &q.String
		}
		if sz.Valid {
			e.SizeBytes = &sz.Int64
		}
		items = append(items, e)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}
