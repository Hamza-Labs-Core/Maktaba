package broadcast

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Routes serves the Broadcast page (Admin → Live TV → Broadcast), home admins only:
//
//	GET  /api/tv/broadcast                            the channels, the uplink, the plan, the settings
//	PUT  /api/tv/broadcast/channels/{id}              a channel's setup {slug, access, mode, rungs, countries}
//	POST /api/tv/broadcast/channels/{id}/publish      {confirm_rights}: the six checks, then publish
//	POST /api/tv/broadcast/channels/{id}/unpublish    stop it
//	POST /api/tv/broadcast/speedtest                  measure the upload now
//
// and the publishable sources (story 50.23):
//
//	GET  /api/tv/broadcast/publishable                         the switch, the permissions, each channel's share
//	POST /api/tv/broadcast/publishable/sources                 record a permission {kind, ref, proof_url, …}
//	POST /api/tv/broadcast/publishable/sources/{id}/withdraw   withdraw one {reason}
//	PUT  /api/tv/broadcast/publishable/items                   mark items own or cleared (or clear the mark)
//	PUT  /api/tv/broadcast/publishable/only                    {enabled, confirm}: off takes a typed confirmation
//
// Every route answers 403 to anyone but an admin, whatever livetv.broadcast's state (the
// router's ComponentRoutes answer 409 component_off to an admin while it is off).
type Routes struct {
	Service *Service
	// Names reads the admin's name for the rights confirmation (nil: the user id).
	Names func(ctx context.Context, userID string) string
}

// Paths are the routes, for the router's component table.
var Paths = []string{
	"GET /api/tv/broadcast",
	"PUT /api/tv/broadcast/channels/{id}",
	"POST /api/tv/broadcast/channels/{id}/publish",
	"POST /api/tv/broadcast/channels/{id}/unpublish",
	"POST /api/tv/broadcast/speedtest",
	"GET /api/tv/broadcast/publishable",
	"POST /api/tv/broadcast/publishable/sources",
	"POST /api/tv/broadcast/publishable/sources/{id}/withdraw",
	"PUT /api/tv/broadcast/publishable/items",
	"PUT /api/tv/broadcast/publishable/only",
}

// Mount wires the routes.
func (h *Routes) Mount(r chi.Router) {
	r.Get("/api/tv/broadcast", h.overview)
	r.Put("/api/tv/broadcast/channels/{id}", h.setup)
	r.Post("/api/tv/broadcast/channels/{id}/publish", h.publish)
	r.Post("/api/tv/broadcast/channels/{id}/unpublish", h.unpublish)
	r.Post("/api/tv/broadcast/speedtest", h.speedtest)
	r.Get("/api/tv/broadcast/publishable", h.publishable)
	r.Post("/api/tv/broadcast/publishable/sources", h.recordSource)
	r.Post("/api/tv/broadcast/publishable/sources/{id}/withdraw", h.withdrawSource)
	r.Put("/api/tv/broadcast/publishable/items", h.markItems)
	r.Put("/api/tv/broadcast/publishable/only", h.publishableOnly)
}

func admin(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, (&httperror.Error{Type: httperror.TypeAuthRequired,
			Title: "sign in", Status: http.StatusUnauthorized, Detail: "sign in as an admin"}).With("code", "auth_required"))
		return nil
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "only a home admin may broadcast"))
		return nil
	}
	return p
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// fail writes a Problem with its code, check and values, ErrNotFound as 404, else a 500.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	var p *Problem
	switch {
	case errors.As(err, &p):
		var e *httperror.Error
		switch p.Status {
		case http.StatusBadRequest:
			e = httperror.BadRequest(p.Refusal.Code)
		case http.StatusServiceUnavailable:
			e = httperror.Unavailable(30)
			w.Header().Set("Retry-After", "30")
		case http.StatusBadGateway:
			e = httperror.BadGateway(p.Refusal.Code)
		default:
			e = httperror.Conflict("", p.Refusal.Code)
		}
		e = e.With("code", "broadcast."+p.Refusal.Code)
		if p.Refusal.Check != "" {
			e = e.With("check", p.Refusal.Check)
		}
		if len(p.Refusal.Params) > 0 {
			e = e.With("params", p.Refusal.Params)
		}
		httperror.Write(w, r, e)
	case errors.Is(err, ErrNotFound):
		httperror.Write(w, r, httperror.NotFound("channel"))
	case errors.Is(err, ErrNoSource):
		httperror.Write(w, r, httperror.NotFound("permission"))
	default:
		httperror.Write(w, r, httperror.Internal("broadcast").Wrap(err))
	}
}

type overview struct {
	Channels []Channel `json:"channels"`
	Uplink   Uplink    `json:"uplink"`
	// Plan is the cloud's answer (nil: the cloud was not reached; Cloud says why).
	Plan     *Plan    `json:"plan"`
	Cloud    string   `json:"cloud"` // ok, unreachable, unclaimed
	Settings Settings `json:"settings"`
	// UplinkBudgetKbps and UplinkUsedKbps are the uplink rule's numbers now.
	UplinkBudgetKbps int      `json:"uplink_budget_kbps"`
	UplinkUsedKbps   int      `json:"uplink_used_kbps"`
	Rungs            []string `json:"rungs"`
	SourceHeight     int      `json:"source_height"`
	Checks           []string `json:"checks"`
	// Revocations is the home's copy of the broadcast revocation list (50.25; nil: not honoured).
	Revocations *RevocationStatus `json:"revocations,omitempty"`
}

func (h *Routes) overview(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	ctx := r.Context()
	st := h.Service.Store
	chs, err := st.List(ctx)
	if err != nil {
		fail(w, r, err)
		return
	}
	up, err := st.LatestUplink(ctx)
	if err != nil {
		fail(w, r, err)
		return
	}
	set := st.Settings(ctx)
	out := st.Output(ctx)
	o := overview{Channels: chs, Uplink: up, Cloud: "ok", Settings: set, Rungs: LadderNames(),
		SourceHeight: out.Height, Checks: CheckOrder}
	if h.Service.Cloud == nil {
		o.Cloud = "unclaimed"
	} else if plan, err := h.Service.Cloud.State(ctx); err != nil {
		o.Cloud = "unreachable"
	} else {
		o.Plan = &plan
	}
	var pub []Published
	for _, c := range chs {
		if c.Published() {
			pub = append(pub, Published{ChannelID: c.ID, Rungs: c.Rungs, Source: out.Source, Dropped: c.Dropped})
		}
	}
	if rv := h.Service.Revocations; rv.Enabled() {
		pubID, home := h.Service.identity(ctx)
		st := rv.Status(ctx, pubID, home)
		o.Revocations = &st
	}
	o.UplinkBudgetKbps = UplinkBudget(up.Kbps, set.UplinkShare)
	o.UplinkUsedKbps = UsedKbps(pub)
	writeJSON(w, http.StatusOK, o)
}

func (h *Routes) setup(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	var req SetupRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		httperror.Write(w, r, httperror.BadRequest("body must be {slug, access, mode, rungs, countries}"))
		return
	}
	ch, err := h.Service.SaveSetup(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *Routes) publish(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	var req struct {
		ConfirmRights bool `json:"confirm_rights"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req)
	if h.Service.Cloud == nil {
		fail(w, r, problem(http.StatusConflict, CheckPlan, "unclaimed", nil))
		return
	}
	name := p.UserID
	if h.Names != nil {
		if n := h.Names(r.Context(), p.UserID); n != "" {
			name = n
		}
	}
	ch, err := h.Service.Publish(r.Context(), chi.URLParam(r, "id"), req.ConfirmRights, Admin{UserID: p.UserID, Name: name})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *Routes) unpublish(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	if h.Service.Cloud == nil {
		fail(w, r, problem(http.StatusConflict, "", "unclaimed", nil))
		return
	}
	ch, err := h.Service.Unpublish(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *Routes) speedtest(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	if h.Service.Cloud == nil {
		fail(w, r, problem(http.StatusConflict, "", "unclaimed", nil))
		return
	}
	up, err := h.Service.SpeedTest(r.Context(), "publish")
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, up)
}

func (h *Routes) adminName(r *http.Request, p *principal.Principal) Admin {
	name := p.UserID
	if h.Names != nil {
		if n := h.Names(r.Context(), p.UserID); n != "" {
			name = n
		}
	}
	return Admin{UserID: p.UserID, Name: name}
}

func (h *Routes) publishable(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	v, err := h.Service.PublishableView(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Routes) recordSource(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	var req PermissionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		httperror.Write(w, r, httperror.BadRequest("body must be {kind, ref, proof_url, label, note, credit, non_commercial}"))
		return
	}
	src, err := h.Service.RecordSource(r.Context(), req, h.adminName(r, p))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, src)
}

func (h *Routes) withdrawSource(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req)
	src, err := h.Service.WithdrawSource(r.Context(), chi.URLParam(r, "id"), req.Reason)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (h *Routes) markItems(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	var req MarkRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&req); err != nil {
		httperror.Write(w, r, httperror.BadRequest("body must be {reason, video_ids | library_id+folder | series_id | collection_id}"))
		return
	}
	n, err := h.Service.MarkItems(r.Context(), req, h.adminName(r, p))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"changed": n})
}

func (h *Routes) publishableOnly(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	var req struct {
		Enabled *bool  `json:"enabled"`
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil || req.Enabled == nil {
		httperror.Write(w, r, httperror.BadRequest("body must be {enabled, confirm}"))
		return
	}
	on, err := h.Service.SetPublishableOnly(r.Context(), *req.Enabled, req.Confirm)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"publishable_only": on})
}
