package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	broadcasth "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/broadcast"
)

// Story 50.26: a service admin may see a home's viewer invites and revoke one (abuse, a
// takedown), never create an invite or change a channel's access: those are the publisher's.

// BroadcastInvites lists a home's invites: labels, limits and status, never a viewer identity.
// The links themselves are left out: staff never need to open a publisher's channels.
func (d *Deps) BroadcastInvites(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !billingpkg.IsUUID(id) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	list, err := broadcasth.ListInvites(r.Context(), d.DB, id, d.now())
	if err != nil {
		d.fail(w, r, "list invites", err)
		return
	}
	for i := range list {
		list[i].URL = ""
		list[i].ShortCode = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": list})
}

// RevokeBroadcastInvite revokes one invite, with a reason that goes in the audit log in the same
// transaction; the worker pushes the change to the edge within a minute.
func (d *Deps) RevokeBroadcastInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req struct {
		Reason string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeErr(w, http.StatusBadRequest, "reason_required", "give a reason; it goes in the audit log")
		return
	}
	if len(reason) > 1000 {
		reason = reason[:1000]
	}
	if !billingpkg.IsUUID(id) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		d.fail(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	var server string
	if err := tx.QueryRowContext(ctx, `SELECT server_id::text FROM broadcast_invites WHERE id = $1 AND revoked_at IS NULL FOR UPDATE`, id).Scan(&server); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	e := d.entry(r, audit.ActionAdminBroadcastInviteRevoke, id, map[string]any{"reason": reason, "server_id": server})
	e.TargetKind = "broadcast_invite"
	by := "staff"
	if e.ActorID != "" {
		by = "staff:" + e.ActorID
	}
	if _, err := broadcasth.RevokeInviteTx(ctx, tx, "", id, by); err != nil {
		d.fail(w, r, "revoke invite", err)
		return
	}
	if err := broadcasth.BumpAccess(ctx, tx, server); err != nil {
		d.fail(w, r, "bump", err)
		return
	}
	if err := audit.WriteTx(ctx, tx, e); err != nil {
		d.fail(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		d.fail(w, r, "commit", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
