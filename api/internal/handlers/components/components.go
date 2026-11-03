// Package components serves the admin routes of the components (Epic 51, story 51.08):
//
//	GET    /api/components                 every component, the revision and the bundle line
//	GET    /api/components/{id}            one component, its parts per machine and its last 20 audit rows
//	PUT    /api/components/{id}            {want, generation?, with_conflicts?}
//	DELETE /api/components/{id}            want absent (?purge=true: and its settings reset)
//	POST   /api/components/{id}/settings/reset  its rules sections back to the defaults
//	POST   /api/components/{id}/install    want installed, when it is absent
//	POST   /api/components/{id}/stop       hold {by, at} until start; a body with until is 400 hold_has_no_until
//	POST   /api/components/{id}/start      clear the hold
//	POST   /api/components/preview         {changes, with_conflicts?, bundle?}: the effect, changing nothing
//	POST   /api/components/apply           {changes, generation?, with_conflicts?, bundle?}: all in one transaction, or none
//	POST   /api/components/offline         build an offline file on this machine (story 51.16, packs.go)
//	GET    /api/components/offline/{name}  its progress, then the file
//	POST   /api/components/import          the offline file as the body: install what it holds
//
// The lists carry each asset's install or removal progress from this machine's supervisor.
// Every route is a home admin's (an admin's cloud link with scope manage included): no
// sign-in is 401, anyone else 403. The checks and the audit rows are the components
// service's, the same the CLI uses. Errors are problem+json with a top-level code.
package components

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Handler serves /api/components*.
type Handler struct {
	Service *svc.Service
	// DB names the admin in a hold and an audit row (users.username); nil: user:<id>.
	DB     *sql.DB
	Logger *slog.Logger
	// Packs is this machine's pack installer (its supervisor); nil: none, so no progress
	// and the offline and import routes answer 503.
	Packs *Packs
}

// Mount wires the routes behind adminOnly.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(adminOnly)
		r.Get("/api/components", h.list)
		r.Post("/api/components/preview", h.preview)
		r.Post("/api/components/apply", h.apply)
		r.Post("/api/components/offline", h.offlineMake)
		r.Get("/api/components/offline/{name}", h.offlineGet)
		r.Post("/api/components/import", h.importFile)
		r.Get("/api/components/{id}", h.show)
		r.Put("/api/components/{id}", h.put)
		r.Delete("/api/components/{id}", h.remove)
		r.Post("/api/components/{id}/install", h.install)
		r.Post("/api/components/{id}/stop", h.stop)
		r.Post("/api/components/{id}/start", h.start)
		r.Post("/api/components/{id}/settings/reset", h.resetSettings)
	})
}

// adminOnly refuses anyone but a home admin before any handler of the package runs: no
// sign-in is 401; a viewer, a cloud play sign-in (ApplyCloudScope clears IsAdmin), a
// token without the admin scope and a machine are 403.
func adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principal.FromContext(r.Context())
		if p == nil {
			httperror.Write(w, r, problem(http.StatusUnauthorized, "auth_required", "sign in", "sign in as an admin"))
			return
		}
		if !p.IsAdmin {
			httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func problem(status int, code, title, detail string) *httperror.Error {
	return (&httperror.Error{
		Type:   "https://maktaba.tv/problems/" + strings.ReplaceAll(code, "_", "-"),
		Title:  title,
		Status: status,
		Detail: detail,
	}).With("code", code)
}

func (h *Handler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// fail writes err: a refusal as its problem, anything else as 503 (the database) or 500.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if ref, ok := svc.AsRefusal(err); ok {
		e := problem(ref.Status, ref.Code, strings.ReplaceAll(ref.Code, "_", " "), ref.Detail)
		if ref.Code == svc.CodeLicence {
			// the licence's own problem (story 51.19): clients that read
			// license-required keep working; code and component added
			e = subscriptions.ComponentRefusal(ref.ID, subscriptions.Feature(ref.Licence), ref.Tier)
			if ref.Components != nil {
				e = e.With("components", ref.Components)
			}
		}
		if ref.ID != "" {
			e = e.With("id", ref.ID)
		}
		if ref.RequiredBy != nil {
			e = e.With("required_by", ref.RequiredBy)
		}
		if ref.Conflicts != nil {
			e = e.With("conflicts", ref.Conflicts)
		}
		if ref.Licence != "" {
			e = e.With("licence", ref.Licence)
		}
		if ref.Current != nil {
			e = e.With("current", ref.Current)
		}
		if ref.Revision != nil {
			e = e.With("revision", *ref.Revision)
		}
		httperror.Write(w, r, e)
		return
	}
	if errors.Is(err, svc.ErrUnknown) {
		httperror.Write(w, r, problem(http.StatusNotFound, svc.CodeUnknown, "unknown component", "no such component"))
		return
	}
	h.log().ErrorContext(r.Context(), "components: request failed", "err", err, "event", "components_error")
	if isDBDown(err) {
		httperror.Write(w, r, httperror.Unavailable(5))
		return
	}
	httperror.Write(w, r, httperror.Internal("components"))
}

func isDBDown(err error) bool {
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "bad connection") ||
		strings.Contains(s, "database system is")
}

// actor is who asks: the admin's name, the users.id and where from (remote for a cloud
// link, web otherwise).
func (h *Handler) actor(r *http.Request) svc.Actor {
	p := principal.FromContext(r.Context())
	a := svc.Actor{By: "admin", Source: "web"}
	if p == nil {
		return a
	}
	if p.ViaCloud() {
		a.Source = "remote"
	}
	a.UserID = p.UserID
	if p.UserID != "" {
		a.By = "user:" + p.UserID
		if h.DB != nil {
			var name string
			if err := h.DB.QueryRowContext(r.Context(), `SELECT username FROM users WHERE id::text = $1`, p.UserID).Scan(&name); err == nil && name != "" {
				a.By = name
			}
		}
	}
	return a
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	v, err := h.Service.List(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	views := make([]*svc.View, len(v.Components))
	for i := range v.Components {
		views[i] = &v.Components[i]
	}
	WithProgress(r.Context(), h.Packs, views...)
	common.WriteJSON(w, r, http.StatusOK, v)
}

func (h *Handler) show(w http.ResponseWriter, r *http.Request) {
	d, err := h.Service.Show(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WithProgress(r.Context(), h.Packs, &d.View)
	common.WriteJSON(w, r, http.StatusOK, d)
}

// known answers 404 for an id the catalog does not have.
func (h *Handler) known(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if !h.Service.Known(id) {
		h.fail(w, r, svc.UnknownRefusal(id))
		return "", false
	}
	return id, true
}

// one applies a single-component request and answers the component as it now is.
func (h *Handler) one(w http.ResponseWriter, r *http.Request, id string, req svc.Request) {
	if _, err := h.Service.Apply(r.Context(), req, h.actor(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	d, err := h.Service.Show(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, d)
}

type putBody struct {
	Want          string `json:"want"`
	Generation    *int64 `json:"generation,omitempty"`
	WithConflicts bool   `json:"with_conflicts,omitempty"`
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	id, ok := h.known(w, r)
	if !ok {
		return
	}
	var b putBody
	if e := common.ReadJSON(r, &b, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	h.one(w, r, id, svc.Request{WithConflicts: b.WithConflicts,
		Changes: []svc.Change{{ID: id, Want: b.Want, Generation: b.Generation}}})
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := h.known(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("purge") == "true" {
		// story 51.13: the settings part of a purge. Removed first (a refusal leaves the
		// settings as they are), then its settings reset; its data (caches, models) is
		// story 51.16's.
		if _, err := h.Service.Apply(r.Context(), svc.Request{Changes: []svc.Change{{ID: id, Want: components.WantAbsent}}}, h.actor(r)); err != nil {
			h.fail(w, r, err)
			return
		}
		if _, err := h.Service.ResetSettings(r.Context(), id, h.actor(r)); err != nil {
			h.fail(w, r, err)
			return
		}
		h.show(w, r)
		return
	}
	h.one(w, r, id, svc.Request{Changes: []svc.Change{{ID: id, Want: components.WantAbsent}}})
}

// install puts a component that is absent on disk, off (installed); one that is there
// already is answered as it is. The supervisor's reconciler does the download.
func (h *Handler) install(w http.ResponseWriter, r *http.Request) {
	id, ok := h.known(w, r)
	if !ok {
		return
	}
	d, err := h.Service.Show(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if d.Want != components.WantAbsent {
		common.WriteJSON(w, r, http.StatusOK, d)
		return
	}
	gen := d.Generation
	h.one(w, r, id, svc.Request{Changes: []svc.Change{{ID: id, Want: components.WantInstalled, Generation: &gen}}})
}

// stop holds a component until start. A hold has no end time: a body that asks for one
// is refused, so an older client never thinks it set a timed hold.
func (h *Handler) stop(w http.ResponseWriter, r *http.Request) {
	id, ok := h.known(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("body too large"))
		return
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		var body map[string]json.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil {
			httperror.Write(w, r, httperror.BadRequest("stop takes no body"))
			return
		}
		if _, has := body["until"]; has {
			httperror.Write(w, r, problem(http.StatusBadRequest, svc.CodeNoUntil, "a hold has no end time",
				"a stopped component stays stopped until it is started"))
			return
		}
	}
	held := true
	h.one(w, r, id, svc.Request{Changes: []svc.Change{{ID: id, Hold: &held}}})
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	id, ok := h.known(w, r)
	if !ok {
		return
	}
	held := false
	h.one(w, r, id, svc.Request{Changes: []svc.Change{{ID: id, Hold: &held}}})
}

func (h *Handler) readRequest(w http.ResponseWriter, r *http.Request) (svc.Request, bool) {
	var req svc.Request
	if e := common.ReadJSON(r, &req, 256<<10); e != nil {
		httperror.Write(w, r, e)
		return req, false
	}
	return req, true
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	req, ok := h.readRequest(w, r)
	if !ok {
		return
	}
	p, err := h.Service.Preview(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, p)
}

func (h *Handler) apply(w http.ResponseWriter, r *http.Request) {
	req, ok := h.readRequest(w, r)
	if !ok {
		return
	}
	res, err := h.Service.Apply(r.Context(), req, h.actor(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}
