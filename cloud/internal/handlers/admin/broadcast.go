package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
)

type broadcastReq struct {
	// Plan is "broadcaster" or "" (guest sharing on a Pro home, nothing elsewhere).
	Plan string `json:"plan"`
	// TestPublisher lets the home broadcast while the broadcasting gate is shut (staging).
	TestPublisher bool   `json:"test_publisher"`
	Reason        string `json:"reason"`
}

// SetBroadcast is a service admin's grant of a home's broadcast plan (story 50.20 rollout: test
// publishers on staging while the gate is shut, before any price is set). A reason is required
// and audited in the same transaction. The Broadcaster add-on still needs Pro to grant anything
// (billing.BroadcastFor); a provider-billed add-on is changed at the provider, not here.
func (d *Deps) SetBroadcast(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req broadcastReq
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
	if req.Plan != "" && req.Plan != billingpkg.BroadcastPlanBroadcaster {
		writeErr(w, http.StatusUnprocessableEntity, "bad_plan", `plan must be "broadcaster" or ""`)
		return
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
	var owner string
	var prev sql.NullString
	var prevTest bool
	err = tx.QueryRowContext(ctx, `SELECT owner_user_id::text, broadcast_plan, broadcast_test_publisher FROM servers WHERE id = $1 FOR UPDATE`, id).
		Scan(&owner, &prev, &prevTest)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		d.fail(w, r, "load home", err)
		return
	}
	var plan any
	if req.Plan != "" {
		plan = req.Plan
	} else if prev.Valid {
		if err := billingpkg.Downgrade(ctx, tx, id); err != nil {
			d.fail(w, r, "downgrade", err)
			return
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE servers SET broadcast_plan = $2, broadcast_test_publisher = $3 WHERE id = $1`,
		id, plan, req.TestPublisher); err != nil {
		d.fail(w, r, "set broadcast", err)
		return
	}
	e := d.entry(r, audit.ActionAdminBroadcast, id, map[string]any{"reason": reason, "plan": req.Plan,
		"test_publisher": req.TestPublisher, "previous_plan": prev.String, "previous_test_publisher": prevTest, "owner_id": owner})
	e.TargetKind = "server"
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
