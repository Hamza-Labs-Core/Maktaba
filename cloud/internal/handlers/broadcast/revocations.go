// The broadcast kill switch's routes (Epic 50 story 50.25):
//
//	GET  /v1/broadcast/revocations                    the signed list (text/plain, mkbc1.…), for
//	                                                  the edge (its signed request) and homes
//	                                                  (basic auth server_id:secret, via the agent)
//	GET  /v1/admin/broadcast/revocations              the list, entries by kind, and the audit
//	POST /v1/admin/broadcast/revocations              {kind, ref, action, reason, note, until}: add
//	POST /v1/admin/broadcast/revocations/{id}/lift    {note}: lift
//
// The admin routes are the operator console's, behind the staff gate's service_admin role (read
// from the database on each request) and a fresh MFA check (staffauth.Sensitive). Every add and
// lift is a broadcast_revocation_audit row naming the staff account, and moves the list's
// version; the new list is pushed to the edge at once, and the edge pulls it every 60 s too.
package broadcast

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

// RevocationDeps wires the kill switch's routes.
type RevocationDeps struct {
	Rev *bc.Revocations
	// Store is the console's entries and audit (the same table Rev signs from).
	Store *bc.RevocationStore
	// Agents checks a home's basic auth on the pull.
	Agents Agents
	// EdgeKey is broadcast.edge_public_key: the edge's signed pull (nil: homes only).
	EdgeKey ed25519.PublicKey
	// Gate is the staff gate's service_admin requirement (nil: every admin route is 403).
	Gate   func(http.Handler) http.Handler
	Logger *slog.Logger
	Now    func() time.Time
	// pushed is called after each background push (tests wait on it).
	pushed func(error)
}

func (d *RevocationDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// RevocationsPath is the pull route; the edge signs its GET for exactly this path.
const RevocationsPath = "/v1/broadcast/revocations"

// MountRevocations registers the pull (no user token: the edge signs, a home sends its secret).
func MountRevocations(r chi.Router, d *RevocationDeps) {
	r.Get(RevocationsPath, d.Pull)
}

// MountRevocationsAdmin registers the admin routes on the operator console's listener.
func MountRevocationsAdmin(r chi.Router, d *RevocationDeps) {
	gate := d.Gate
	if gate == nil {
		gate = func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeErr(w, http.StatusForbidden, "admin_access_denied", "")
			})
		}
	}
	r.Group(func(g chi.Router) {
		g.Use(gate)
		g.Get("/v1/admin/broadcast/revocations", d.AdminList)
		g.Post("/v1/admin/broadcast/revocations", d.AdminAdd)
		g.Post("/v1/admin/broadcast/revocations/{id}/lift", d.AdminLift)
	})
}

// Pull answers the signed list to the edge or a home.
func (d *RevocationDeps) Pull(w http.ResponseWriter, r *http.Request) {
	if d.Rev == nil || d.Rev.Key == nil {
		writeErr(w, http.StatusServiceUnavailable, "broadcast_disabled", "")
		return
	}
	if !d.pullAllowed(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="maktaba-server"`)
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	text, l, err := d.Rev.Signed(r.Context())
	if err != nil {
		d.fail(w, r, "sign revocations", err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Maktaba-Revocations-Version", strconv.FormatInt(l.Version, 10))
	_, _ = io.WriteString(w, text)
}

func (d *RevocationDeps) pullAllowed(r *http.Request) bool {
	if r.Header.Get(bc.HeaderEdgeSignature) != "" {
		return d.EdgeKey != nil && bc.VerifyEdgeRequest(d.EdgeKey, r.Method, r.URL.Path, r.Header, nil, d.now()) == nil
	}
	id, secret, ok := r.BasicAuth()
	return ok && id != "" && d.Agents != nil && d.Agents.VerifySecret(r.Context(), id, secret) == nil
}

type adminView struct {
	Version   int64                `json:"version"`
	ChangedAt time.Time            `json:"changed_at"`
	Entries   []bc.RevocationRow   `json:"entries"`
	Audit     []bc.RevocationAudit `json:"audit"`
	// Signing is whether the cloud holds the broadcast signing key (off: nothing reaches the edge).
	Signing bool `json:"signing"`
}

// AdminList is the list for the console: entries by kind, who added them, when and why.
func (d *RevocationDeps) AdminList(w http.ResponseWriter, r *http.Request) {
	v, changed, rows, audit, err := d.Store.Overview(r.Context())
	if err != nil {
		d.fail(w, r, "revocation overview", err)
		return
	}
	if rows == nil {
		rows = []bc.RevocationRow{}
	}
	if audit == nil {
		audit = []bc.RevocationAudit{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, adminView{Version: v, ChangedAt: changed, Entries: rows, Audit: audit, Signing: d.Rev.Key != nil})
}

type addReq struct {
	Kind   string     `json:"kind"`
	Ref    string     `json:"ref"`
	Action string     `json:"action"`
	Reason string     `json:"reason"`
	Note   string     `json:"note"`
	Until  *time.Time `json:"until"`
}

func actor(r *http.Request) (bc.Actor, bool) {
	o, ok := authmw.ServiceAdmin(r.Context())
	return bc.Actor{ID: o.ID, Email: o.Email}, ok && o.ID != ""
}

// AdminAdd adds an entry; 201 with it and the list's new version.
func (d *RevocationDeps) AdminAdd(w http.ResponseWriter, r *http.Request) {
	by, ok := actor(r)
	if !ok {
		writeErr(w, http.StatusForbidden, "admin_access_denied", "")
		return
	}
	var req addReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {kind, ref, action, reason, note, until}")
		return
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > 500 {
		writeErr(w, http.StatusBadRequest, "bad_request", "the note is at most 500 characters")
		return
	}
	e := bc.Entry{Kind: req.Kind, Ref: strings.TrimSpace(req.Ref), Action: req.Action, Reason: req.Reason, Until: req.Until}
	if e.Until != nil && !e.Until.After(d.now()) {
		writeErr(w, http.StatusBadRequest, "bad_entry", "until must be in the future")
		return
	}
	if err := e.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_entry", err.Error())
		return
	}
	row, v, err := d.Store.Add(r.Context(), e, note, by)
	if err != nil {
		d.fail(w, r, "add revocation", err)
		return
	}
	d.log().Warn("broadcast: revocation added", "event", "broadcast_revocation_added", "id", row.ID,
		"kind", row.Kind, "action", row.Action, "reason", row.Reason, "version", v, "staff_id", by.ID)
	d.pushLater()
	writeJSON(w, http.StatusCreated, map[string]any{"entry": row, "version": v})
}

// AdminLift lifts an entry; 200 with the list's new version.
func (d *RevocationDeps) AdminLift(w http.ResponseWriter, r *http.Request) {
	by, ok := actor(r)
	if !ok {
		writeErr(w, http.StatusForbidden, "admin_access_denied", "")
		return
	}
	row, ok := bc.ParseEntryID(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	// the note is optional; an empty body is a lift without one
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {note}")
		return
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > 500 {
		writeErr(w, http.StatusBadRequest, "bad_request", "the note is at most 500 characters")
		return
	}
	v, err := d.Store.Lift(r.Context(), row, note, by)
	if errors.Is(err, bc.ErrRevocationNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		d.fail(w, r, "lift revocation", err)
		return
	}
	d.log().Warn("broadcast: revocation lifted", "event", "broadcast_revocation_lifted", "id", bc.EntryID(row),
		"version", v, "staff_id", by.ID)
	d.pushLater()
	writeJSON(w, http.StatusOK, map[string]any{"id": bc.EntryID(row), "version": v})
}

// pushLater sends the new list to the edge without holding the admin's request.
func (d *RevocationDeps) pushLater() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := d.Rev.Push(ctx)
		if d.pushed != nil {
			d.pushed(err)
		}
	}()
}

func (d *RevocationDeps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

func (d *RevocationDeps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	(&Deps{Logger: d.log()}).fail(w, r, what, err)
}
