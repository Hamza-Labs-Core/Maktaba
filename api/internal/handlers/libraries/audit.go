// Story 9.17 — surfaced library audit log + helpers shared by the
// destructive endpoints (Story 9.15 deletion, Story 9.1 settings PATCH,
// Story 9.11 speaker merge — owned in their respective handlers).
//
// The vocabulary mirrors the Python side
// (“maktaba_pipeline.library_mgmt.audit.LibraryAuditEvent“); a write
// emitted from a worker and a write emitted from the API both end up as
// the same “audit_log“ row with “category='library'“.
package libraries

import (
	"context"
	"database/sql"
	"encoding/json"
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

// LibraryAuditEvent enumerates the closed event vocabulary for
// “category='library'“. Keep in sync with the Python sibling.
const (
	EventScanTriggered      = "scan-triggered"
	EventSettingsChanged    = "settings-changed"
	EventVideoPurged        = "video-purged"
	EventLibraryDeleted     = "library-deleted"
	EventSpeakerMerged      = "speaker-merged"
	EventFilePurgeResults   = "file-purge-results"
	EventDuplicateDetected  = "duplicate-detected"
	EventRuntimeRootOverlap = "runtime-root-overlap"
	EventPathOutOfRoot      = "path-out-of-root"
	EventTopicRecluster     = "topic-recluster"
)

// AuditPayloadMaxBytes mirrors the Python AC EC: payloads larger than
// this are truncated to a sentinel so the table can never balloon from
// a single noisy actor.
const AuditPayloadMaxBytes = 8 * 1024

// AuditEntry is the AC-2 over-the-wire shape.
type AuditEntry struct {
	ID        int64           `json:"id"`
	Event     string          `json:"event"`
	ActorID   *string         `json:"actor_user_id,omitempty"`
	TargetID  *string         `json:"target_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"ts"`
}

// MountAudit registers “GET /api/libraries/{id}/audit“. Wired
// separately from :meth:`Handler.Mount` so deployments without the
// 0036/0044/0049 audit migrations applied can still serve the rest of
// the surface.
func (h *Handler) MountAudit(r chi.Router) {
	r.Get("/api/libraries/{id}/audit", h.Audit)
}

// Audit returns the AC-2 paginated audit feed (newest-first). The
// cursor is the integer audit_log.id of the boundary row — opaque to
// the client but cheap to encode/decode.
func (h *Handler) Audit(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	if _, err := h.loadLibrary(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library"))
		return
	}

	limit, _ := common.QueryInt(r, "limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cursor, _ := common.QueryInt(r, "cursor", 0)

	args := []any{id}
	where := "category = 'library' AND target_id = $1"
	if cursor > 0 {
		where += " AND id < $2"
		args = append(args, cursor)
	}
	q := "SELECT id, action, actor_user_id, target_id, payload, ts FROM audit_log WHERE " +
		where + " ORDER BY id DESC LIMIT " + itoa(limit)
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("audit list: "+err.Error()))
		return
	}
	defer rows.Close()
	items := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var actor sql.NullString
		var target sql.NullString
		var payload []byte
		if err := rows.Scan(&e.ID, &e.Event, &actor, &target, &payload, &e.Timestamp); err != nil {
			httperror.Write(w, r, httperror.Internal("audit scan: "+err.Error()))
			return
		}
		if actor.Valid {
			e.ActorID = &actor.String
		}
		if target.Valid {
			e.TargetID = &target.String
		}
		if len(payload) > 0 {
			e.Payload = payload
		} else {
			e.Payload = json.RawMessage("{}")
		}
		items = append(items, e)
	}
	resp := map[string]any{"items": items}
	if len(items) == limit {
		resp["next_cursor"] = items[len(items)-1].ID
	}
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// WriteAudit is the canonical insert helper. It is used by
// :meth:`Delete` (library deletion / purge), the settings PATCH path
// (Story 9.1 NOTIFY pre-write), and the worker bridge for
// pipeline-emitted events. Best-effort: a missing “audit_log“ table
// (e.g., a partial-migration test environment) silently swallows the
// error so the destructive operation is never blocked.
func WriteAudit(
	ctx context.Context,
	db *sql.DB,
	event string,
	actor *string,
	libraryID string,
	payload map[string]any,
) {
	if db == nil {
		return
	}
	encoded, _ := json.Marshal(payload)
	if len(encoded) > AuditPayloadMaxBytes {
		// Replace with a sentinel — keeps the row cheap and avoids
		// surfacing a single rogue payload via the audit feed.
		var keys []string
		for k := range payload {
			keys = append(keys, k)
		}
		encoded, _ = json.Marshal(map[string]any{
			"_truncated": true,
			"reason":     "payload exceeded limit",
			"keys":       keys,
		})
	}
	var actorParam any
	if actor != nil {
		actorParam = *actor
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
		VALUES ('library', $1, $2, $3, $4)
	`, event, actorParam, libraryID, string(encoded))
	if err != nil {
		// Log to stderr in production; here we swallow per AC EC.
		_ = err
	}
}

// AuditedDelete wraps the existing :meth:`Delete` with a best-effort
// audit record. Wiring is opt-in via :meth:`Handler.MountAudit` so the
// existing tests keep passing without an audit_log table.
func (h *Handler) AuditedDelete(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	id := chi.URLParam(r, "id")
	lib, _ := h.loadLibrary(r.Context(), id)

	purge := strings.EqualFold(r.URL.Query().Get("purge"), "true")

	// Defer the audit write so we capture the *outcome* — a deletion
	// that hit a 412 confirmation gate is still worth surfacing in the
	// audit feed because it tells operators a destructive call was
	// attempted.
	defer func() {
		var actor *string
		if p != nil {
			s := p.UserID
			actor = &s
		}
		payload := map[string]any{
			"name":  lib.Name,
			"roots": lib.Roots,
			"purge": purge,
		}
		WriteAudit(r.Context(), h.DB, EventLibraryDeleted, actor, id, payload)
	}()

	h.Delete(w, r)
}
