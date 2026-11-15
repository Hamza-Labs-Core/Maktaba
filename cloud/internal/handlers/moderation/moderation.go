// Package moderation serves the takedown and report routes (Epic 50 story 50.24):
//
// Public (no account; Turnstile and a per-IP rate limit):
//
//	POST /v1/moderation/notices    a copyright notice from maktaba.tv/legal/copyright: 202 {notice_id}
//	POST /v1/moderation/reports    a viewer's report from a watch surface: 202 {report_id}
//
// A home (basic auth server_id:secret, through its relay agent); each sees only its own records:
//
//	GET  /v1/broadcast/takedowns                    the home's pending instructions, signed (mkbc1.…)
//	POST /v1/broadcast/takedowns/{id}/ack           {result, detail, item_ref, programme, airing_until}
//	GET  /v1/broadcast/moderation                   its statements, reports about its content, its standing
//	POST /v1/broadcast/moderation/counter-notices   a counter-notice to one of its takedowns
//
// The operator console (staff with service_admin, read from the database on each request; the
// actions that take something down or put it back also need a fresh MFA check, staffauth.Sensitive):
//
//	GET  /v1/admin/moderation                                the queue
//	POST /v1/admin/moderation/notices                        enter an emailed notice
//	POST /v1/admin/moderation/actions                        take down (on a notice, a report, an order, knowledge)
//	POST /v1/admin/moderation/actions/{id}/restore           restore
//	POST /v1/admin/moderation/notices/{id}/reject            {reason, unfounded}
//	POST /v1/admin/moderation/notices/{id}/withdraw          the claimant withdrew it
//	POST /v1/admin/moderation/reports/{id}/dismiss           {reason}
//	POST /v1/admin/moderation/counter-notices/{id}/lawsuit   the claimant went to court
//	POST /v1/admin/moderation/strikes/{id}/withdraw          {reason}
//	POST /v1/admin/moderation/publishers/{id}/reinstate      {reason}
//	POST /v1/admin/moderation/notifiers/suspend              {email, reason}
//	GET  /v1/admin/moderation/export?format=csv|json         every record, for a lawyer
//
// Every staff action carries its reason and is a moderation_audit row naming the staff account.
package moderation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	mod "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/moderation"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/turnstile"
)

// FormOrigins are the only web origins the public forms answer (the copyright page and the watch
// site's report form post from maktaba.tv and watch.maktaba.tv).
var FormOrigins = []string{"https://maktaba.tv", "https://watch.maktaba.tv"}

// PublicPaths are the public routes, for the per-IP rate limit (burst 5, then one a minute).
var PublicPaths = []string{"/v1/moderation/notices", "/v1/moderation/reports"}

// Agents checks a home's credential.
type Agents interface {
	VerifySecret(ctx context.Context, serverID, secret string) error
}

// Deps wires the routes.
type Deps struct {
	Store  *mod.Store
	Agents Agents
	// Key signs a home's instructions (the broadcast signing key; nil: 503 broadcast_disabled).
	Key *bc.Key
	// Turnstile verifies the forms' bot check (nil: off, dev and CI). Unlike the auth pages,
	// every request is asked for a token while it is on: the forms have no native client.
	Turnstile turnstile.Verifier
	// IPSalt keys the stored hash of a sender's address (never the address itself).
	IPSalt []byte
	// Gate is the staff gate's service_admin requirement (nil: every admin route is 403).
	Gate   func(http.Handler) http.Handler
	Logger *slog.Logger
	Now    func() time.Time
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Deps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

// MountPublic registers the public forms on the user listener.
func MountPublic(r chi.Router, d *Deps) {
	r.Post("/v1/moderation/notices", d.PostNotice)
	r.Post("/v1/moderation/reports", d.PostReport)
}

// MountHome registers a home's routes on the user listener (its own credential, no account).
func MountHome(r chi.Router, d *Deps) {
	r.Get("/v1/broadcast/takedowns", d.home(d.Takedowns))
	r.Post("/v1/broadcast/takedowns/{id}/ack", d.home(d.Ack))
	r.Get("/v1/broadcast/moderation", d.home(d.HomeView))
	r.Post("/v1/broadcast/moderation/counter-notices", d.home(d.CounterNotice))
}

// MountAdmin registers the console's routes behind the staff gate.
func MountAdmin(r chi.Router, d *Deps) {
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
		g.Get("/v1/admin/moderation", d.Queue)
		g.Post("/v1/admin/moderation/notices", d.EnterNotice)
		g.Post("/v1/admin/moderation/actions", d.Takedown)
		g.Post("/v1/admin/moderation/actions/{id}/restore", d.Restore)
		g.Post("/v1/admin/moderation/notices/{id}/reject", d.Reject)
		g.Post("/v1/admin/moderation/notices/{id}/withdraw", d.WithdrawNotice)
		g.Post("/v1/admin/moderation/reports/{id}/dismiss", d.Dismiss)
		g.Post("/v1/admin/moderation/counter-notices/{id}/lawsuit", d.Lawsuit)
		g.Post("/v1/admin/moderation/strikes/{id}/withdraw", d.WithdrawStrike)
		g.Post("/v1/admin/moderation/publishers/{id}/reinstate", d.Reinstate)
		g.Post("/v1/admin/moderation/notifiers/suspend", d.SuspendNotifier)
		g.Get("/v1/admin/moderation/export", d.Export)
	})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}

func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, limit)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "the body must be JSON")
		return false
	}
	return true
}

// fail maps the store's errors to answers.
func (d *Deps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, mod.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "")
	case errors.Is(err, mod.ErrClosed):
		writeErr(w, http.StatusConflict, "already_decided", "")
	case errors.Is(err, mod.ErrNoTarget):
		writeErr(w, http.StatusConflict, "no_target", "name the channel (or the publisher) this applies to")
	case errors.Is(err, mod.ErrEdgeUnknown):
		writeErr(w, http.StatusConflict, "edge_channel_unknown",
			"the channel has not asked for an ingest token since this release: use the channel or publisher scope, or no slate")
	case errors.Is(err, mod.ErrRestoreClosed):
		writeErr(w, http.StatusConflict, "restore_window_closed", "no counter-notice whose window has passed without a lawsuit")
	case errors.Is(err, mod.ErrBadRequest), errors.Is(err, bc.ErrBadEntry):
		writeErr(w, http.StatusBadRequest, "bad_request", strings.TrimPrefix(err.Error(), mod.ErrBadRequest.Error()+": "))
	default:
		d.log().Error("moderation: "+what, "err", err, "path", r.URL.Path)
		writeErr(w, http.StatusInternalServerError, "internal", "")
	}
}

// ipHash is a keyed hash of the sender's address (rate limits and abuse patterns; never the address).
func (d *Deps) ipHash(r *http.Request) string {
	ip := cmw.GetClientIP(r.Context())
	if ip == "" || len(d.IPSalt) == 0 {
		return ""
	}
	m := hmac.New(sha256.New, d.IPSalt)
	_, _ = m.Write([]byte(ip))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// formGuard refuses another site's page (any Origin but the forms' own) and checks the bot token.
func (d *Deps) formGuard(w http.ResponseWriter, r *http.Request, token string) bool {
	if o := strings.TrimSuffix(r.Header.Get("Origin"), "/"); o != "" {
		ok := false
		for _, f := range FormOrigins {
			ok = ok || o == f
		}
		if !ok {
			writeErr(w, http.StatusForbidden, "origin_not_allowed", "")
			return false
		}
	}
	if d.Turnstile == nil {
		return true
	}
	err := d.Turnstile.Verify(r.Context(), token, cmw.GetClientIP(r.Context()))
	switch {
	case err == nil:
		return true
	case errors.Is(err, turnstile.ErrUnavailable):
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusServiceUnavailable, turnstile.CodeUnavailable, "")
	default:
		writeErr(w, http.StatusForbidden, turnstile.CodeFailed, "")
	}
	return false
}

type noticeBody struct {
	mod.Notice
	Turnstile string `json:"turnstile_token"`
}

// PostNotice is the public notice form: 422 with what is missing, 422 not_on_maktaba for a notice
// about another site, 403 for a suspended notifier, else 202 with the notice's id.
func (d *Deps) PostNotice(w http.ResponseWriter, r *http.Request) {
	var b noticeBody
	if !decode(w, r, 64<<10, &b) || !d.formGuard(w, r, b.Turnstile) {
		return
	}
	d.addNotice(w, r, b.Notice, "")
}

func (d *Deps) addNotice(w http.ResponseWriter, r *http.Request, n mod.Notice, enteredBy string) {
	if missing := n.Missing(); len(missing) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "notice_incomplete", "missing": missing})
		return
	}
	if _, ok := mod.OnMaktaba(n.URLs); !ok {
		writeErr(w, http.StatusUnprocessableEntity, "not_on_maktaba",
			"no URL names a channel on watch.maktaba.tv: Maktaba can act only on what it hosts")
		return
	}
	if enteredBy == "" {
		blocked, err := d.Store.NotifierBlocked(r.Context(), n.ClaimantEmail)
		if err != nil {
			d.fail(w, r, "notifier check", err)
			return
		}
		if blocked {
			writeErr(w, http.StatusForbidden, "notifier_suspended", "write to copyright@maktaba.tv")
			return
		}
	}
	id, err := d.Store.AddNotice(r.Context(), n, d.ipHash(r), enteredBy)
	if err != nil {
		d.fail(w, r, "add notice", err)
		return
	}
	d.log().Info("moderation: notice received", "event", "moderation_notice", "notice_id", id, "entered_by", enteredBy)
	writeJSON(w, http.StatusAccepted, map[string]string{"notice_id": id})
}

type reportBody struct {
	mod.Report
	Turnstile string `json:"turnstile_token"`
}

// PostReport is a viewer's report: 202 with its id.
func (d *Deps) PostReport(w http.ResponseWriter, r *http.Request) {
	var b reportBody
	if !decode(w, r, 16<<10, &b) || !d.formGuard(w, r, b.Turnstile) {
		return
	}
	rep := b.Report
	switch {
	case !mod.ValidCategory(rep.Category):
		writeErr(w, http.StatusBadRequest, "bad_request", "category must be copyright, illegal, abuse or other")
		return
	case rep.About != "" && rep.About != "content" && rep.About != "publisher":
		writeErr(w, http.StatusBadRequest, "bad_request", "about must be content or publisher")
		return
	case len(rep.Body) > 4000 || len(rep.Email) > 320 || len(rep.Programme) > 300:
		writeErr(w, http.StatusBadRequest, "bad_request", "a field is too long")
		return
	}
	id, err := d.Store.AddReport(r.Context(), rep, d.ipHash(r))
	if err != nil {
		d.fail(w, r, "add report", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"report_id": id})
}

// home wraps a home's route: its basic auth (server_id:secret) or 401.
func (d *Deps) home(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id == "" || d.Agents == nil || d.Agents.VerifySecret(r.Context(), id, secret) != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="maktaba-server"`)
			writeErr(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}
		next(w, r, id)
	}
}

// Takedowns answers the home's pending instructions, signed.
func (d *Deps) Takedowns(w http.ResponseWriter, r *http.Request, serverID string) {
	if d.Key == nil {
		writeErr(w, http.StatusServiceUnavailable, "broadcast_disabled", "")
		return
	}
	list, err := d.Store.Instructions(r.Context(), serverID)
	if err != nil {
		d.fail(w, r, "instructions", err)
		return
	}
	text, err := mod.SignInstructions(*d.Key, serverID, list, d.now())
	if err != nil {
		d.fail(w, r, "sign instructions", err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, text)
}

// Ack records the home's answer to one instruction.
func (d *Deps) Ack(w http.ResponseWriter, r *http.Request, serverID string) {
	var a mod.Ack
	if !decode(w, r, 4<<10, &a) {
		return
	}
	if !mod.ValidAck(a) {
		writeErr(w, http.StatusBadRequest, "bad_request", "result must be applied, not_found or failed")
		return
	}
	if err := d.Store.Acknowledge(r.Context(), serverID, chi.URLParam(r, "id"), a); err != nil {
		d.fail(w, r, "ack", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HomeView answers the home's statements, the reports about its content and its standing.
func (d *Deps) HomeView(w http.ResponseWriter, r *http.Request, serverID string) {
	v, err := d.Store.Home(r.Context(), serverID)
	if err != nil {
		d.fail(w, r, "home view", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// CounterNotice files the publisher's counter-notice.
func (d *Deps) CounterNotice(w http.ResponseWriter, r *http.Request, serverID string) {
	var c mod.CounterNotice
	if !decode(w, r, 16<<10, &c) {
		return
	}
	if missing := c.Missing(); len(missing) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "counter_notice_incomplete", "missing": missing})
		return
	}
	id, after, err := d.Store.FileCounterNotice(r.Context(), serverID, c)
	if err != nil {
		d.fail(w, r, "counter-notice", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "restore_after": after})
}

// actor is the signed-in service admin; false writes 403.
func actor(w http.ResponseWriter, r *http.Request) (bc.Actor, bool) {
	o, ok := authmw.ServiceAdmin(r.Context())
	if !ok || o.ID == "" {
		writeErr(w, http.StatusForbidden, "admin_access_denied", "")
		return bc.Actor{}, false
	}
	return bc.Actor{ID: o.ID, Email: o.Email}, true
}

// reasonBody is the body of the decisions that take only a reason.
type reasonBody struct {
	Reason    string `json:"reason"`
	Unfounded bool   `json:"unfounded"`
	Email     string `json:"email"`
}

// reasoned reads a decision's body; every staff action needs its reason.
func reasoned(w http.ResponseWriter, r *http.Request) (reasonBody, bool) {
	var b reasonBody
	if !decode(w, r, 8<<10, &b) {
		return b, false
	}
	b.Reason = strings.TrimSpace(b.Reason)
	if b.Reason == "" || len(b.Reason) > 1000 {
		writeErr(w, http.StatusBadRequest, "reason_required", "every action needs its reason (at most 1000 characters)")
		return b, false
	}
	return b, true
}

// Queue is the console's page.
func (d *Deps) Queue(w http.ResponseWriter, r *http.Request) {
	q, err := d.Store.Queue(r.Context())
	if err != nil {
		d.fail(w, r, "queue", err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// EnterNotice enters a notice that came by email to the agent.
func (d *Deps) EnterNotice(w http.ResponseWriter, r *http.Request) {
	by, ok := actor(w, r)
	if !ok {
		return
	}
	var n mod.Notice
	if !decode(w, r, 64<<10, &n) {
		return
	}
	d.addNotice(w, r, n, by.ID)
}

// Takedown carries out an action.
func (d *Deps) Takedown(w http.ResponseWriter, r *http.Request) {
	by, ok := actor(w, r)
	if !ok {
		return
	}
	var req mod.TakedownRequest
	if !decode(w, r, 16<<10, &req) {
		return
	}
	a, err := d.Store.Takedown(r.Context(), req, by)
	if err != nil {
		d.fail(w, r, "takedown", err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// Restore restores a takedown.
func (d *Deps) Restore(w http.ResponseWriter, r *http.Request) {
	by, ok := actor(w, r)
	if !ok {
		return
	}
	b, ok := reasoned(w, r)
	if !ok {
		return
	}
	a, err := d.Store.Restore(r.Context(), chi.URLParam(r, "id"), b.Reason, by)
	if err != nil {
		d.fail(w, r, "restore", err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// decide runs a decision that answers 204.
func (d *Deps) decide(what string, f func(ctx context.Context, id string, b reasonBody, by bc.Actor) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		by, ok := actor(w, r)
		if !ok {
			return
		}
		b, ok := reasoned(w, r)
		if !ok {
			return
		}
		if err := f(r.Context(), chi.URLParam(r, "id"), b, by); err != nil {
			d.fail(w, r, what, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Reject rejects a notice.
func (d *Deps) Reject(w http.ResponseWriter, r *http.Request) {
	d.decide("reject", func(ctx context.Context, id string, b reasonBody, by bc.Actor) error {
		return d.Store.RejectNotice(ctx, id, b.Reason, b.Unfounded, by)
	})(w, r)
}

// WithdrawNotice records the claimant's withdrawal.
func (d *Deps) WithdrawNotice(w http.ResponseWriter, r *http.Request) {
	d.decide("withdraw notice", func(ctx context.Context, id string, b reasonBody, by bc.Actor) error {
		return d.Store.WithdrawNotice(ctx, id, b.Reason, by)
	})(w, r)
}

// Dismiss dismisses a report.
func (d *Deps) Dismiss(w http.ResponseWriter, r *http.Request) {
	d.decide("dismiss", func(ctx context.Context, id string, b reasonBody, by bc.Actor) error {
		return d.Store.DismissReport(ctx, id, b.Reason, by)
	})(w, r)
}

// Lawsuit records that the claimant went to court.
func (d *Deps) Lawsuit(w http.ResponseWriter, r *http.Request) {
	d.decide("lawsuit", func(ctx context.Context, id string, b reasonBody, by bc.Actor) error {
		return d.Store.Lawsuit(ctx, id, b.Reason, by)
	})(w, r)
}

// WithdrawStrike withdraws a strike.
func (d *Deps) WithdrawStrike(w http.ResponseWriter, r *http.Request) {
	d.decide("withdraw strike", func(ctx context.Context, id string, b reasonBody, by bc.Actor) error {
		return d.Store.WithdrawStrike(ctx, id, b.Reason, by)
	})(w, r)
}

// Reinstate reinstates a terminated publisher.
func (d *Deps) Reinstate(w http.ResponseWriter, r *http.Request) {
	d.decide("reinstate", func(ctx context.Context, id string, b reasonBody, by bc.Actor) error {
		return d.Store.Reinstate(ctx, id, b.Reason, by)
	})(w, r)
}

// SuspendNotifier suspends a notifier from the form.
func (d *Deps) SuspendNotifier(w http.ResponseWriter, r *http.Request) {
	d.decide("suspend notifier", func(ctx context.Context, _ string, b reasonBody, by bc.Actor) error {
		return d.Store.SuspendNotifier(ctx, b.Email, b.Reason, by)
	})(w, r)
}

// Export answers every record as JSON (default) or CSV.
func (d *Deps) Export(w http.ResponseWriter, r *http.Request) {
	by, ok := actor(w, r)
	if !ok {
		return
	}
	recs, err := d.Store.Records(r.Context())
	if err != nil {
		d.fail(w, r, "export", err)
		return
	}
	if err := d.Store.Audit(r.Context(), by, "export", "records", r.URL.Query().Get("format"), "records for counsel",
		map[string]any{"rows": len(recs)}); err != nil {
		d.fail(w, r, "export audit", err)
		return
	}
	d.log().Warn("moderation: records exported", "event", "moderation_export", "rows", len(recs), "staff_id", by.ID)
	stamp := d.now().UTC().Format("20060102")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="maktaba-moderation-`+stamp+`.csv"`)
		_ = mod.WriteCSV(w, recs)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="maktaba-moderation-`+stamp+`.json"`)
	writeJSON(w, http.StatusOK, map[string]any{"exported_at": d.now().UTC(), "records": recs})
}
