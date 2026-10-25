package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// maxBody bounds the request bodies of the update endpoints.
const maxBody = 64 << 10

// Handler serves the home's update endpoints (README "APIs"; admin only):
//
//	GET  /api/cluster/updates            policy, plan (next planned update), active run, catalog
//	PUT  /api/cluster/updates/settings   rules.updates keys (validated: pin, window, deferral)
//	POST /api/cluster/updates/run        {version?, confirm:true}  Update now -> 202 {run_id}
//	POST /api/cluster/updates/skip       {version}
//	POST /api/cluster/updates/defer      {until}  (RFC 3339; "" clears)
//	POST /api/cluster/updates/check      check GitHub now
//	GET  /api/cluster/updates/runs       history (?limit=)
//	GET  /api/cluster/updates/backups    the home's backups and kept releases (story 35.5 AC8)
//	GET  /api/cluster/updates/backups/{name}/restore   what restoring it does
//	POST /api/cluster/updates/backups/{name}/restore   {confirm:"<name>"}  -> 202 (the home restarts)
type Handler struct {
	Svc *Service
	// Driver runs the runs (nil: an API that cannot install; the run actions answer 503).
	Driver *Driver
	// RulesPatch is the rules handler's PATCH /api/rules/{section}: settings are stored
	// where every other rule is (app_settings rules.updates), with its validation.
	RulesPatch http.HandlerFunc
}

// Mount attaches the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/cluster/updates", h.Get)
	r.Put("/api/cluster/updates/settings", h.PutSettings)
	r.Post("/api/cluster/updates/run", h.Run)
	r.Post("/api/cluster/updates/skip", h.Skip)
	r.Post("/api/cluster/updates/defer", h.Defer)
	r.Post("/api/cluster/updates/check", h.Check)
	r.Post("/api/cluster/updates/unblock", h.Unblock)
	r.Get("/api/cluster/updates/runs", h.Runs)
	r.Get("/api/cluster/updates/runs/{id}", h.RunView)
	r.Get("/api/cluster/updates/runs/{id}/log", h.RunLog)
	r.Post("/api/cluster/updates/runs/{id}/cancel", h.Cancel)
	r.Post("/api/cluster/updates/runs/{id}/resume", h.Resume)
	r.Post("/api/cluster/updates/runs/{id}/targets/{node}/{action}", h.TargetAction)
	r.Get("/api/cluster/updates/backups", h.BackupsList)
	r.Get("/api/cluster/updates/backups/{name}/restore", h.RestorePlanView)
	r.Post("/api/cluster/updates/backups/{name}/restore", h.Restore)
}

func admin(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return nil
	}
	return p
}

func bad(w http.ResponseWriter, r *http.Request, code, detail string) {
	httperror.Write(w, r, (&httperror.Error{Type: "about:blank#" + code, Title: "bad request",
		Status: http.StatusBadRequest, Detail: detail}).With("code", code))
}

// Overview is GET /api/cluster/updates.
type Overview struct {
	Current              string     `json:"current_version"`
	Watermark            string     `json:"watermark"`
	Policy               Policy     `json:"policy"`
	AutoInstallAvailable bool       `json:"auto_install_available"`
	Plan                 Plan       `json:"plan"`
	Run                  *Run       `json:"run,omitempty"`
	Catalog              []Release  `json:"catalog"`
	LastCheckOK          *time.Time `json:"last_check_ok,omitempty"`
	LastCheckError       string     `json:"last_check_error,omitempty"`
	CheckStale           bool       `json:"check_stale"`
	TimeZone             string     `json:"time_zone"`
	// Machines are the home and every joined machine (story 35.12).
	Machines []Machine `json:"machines"`
	// Last is the newest finished run (nil: none yet).
	Last *Run `json:"last_run,omitempty"`
	// LastTick is the scheduler's last decision: why a due release does not start (a
	// blocker, a retry's pause, a failure a person must mend). nil: no tick yet here.
	LastTick *TickInfo `json:"last_tick,omitempty"`
	// Stuck is a release the home has not managed to install for several windows in a
	// row for the same reason (the TV watched every night, say): an admin may install it
	// now (nil: none).
	Stuck *Stuck `json:"stuck,omitempty"`
}

// Machine is one row of the Updates page's machines table.
type Machine struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Version  string `json:"version"`
	State    string `json:"state"`
	Platform string `json:"platform,omitempty"`
	// Kind is the machine's install kind (layout, runtime, appbundle, flat).
	Kind string `json:"kind,omitempty"`
	// Target is the release the active run brings it to (the one its app shows while it
	// waits for a click).
	Target string `json:"target,omitempty"`
	// Attempts are its last update attempts, every failure with its reason: its own (the
	// desktop app's updater, its releases' starts) and its last part in a run, newest
	// first (update-attempts.md).
	Attempts []Attempt `json:"attempts,omitempty"`
	// HomeDB is a node's login to the home's database (nil: it has not said): "this
	// machine can't reach the home's database: <reason>" when not ok.
	HomeDB *HomeDB `json:"home_db,omitempty"`
	// Convert is what becomes of a worker-only machine (story 35.8 AC8): next_update (its
	// next update makes it a full node) or installer ("worker-only — re-run the installer
	// once": its pipeline is too old for that).
	Convert string `json:"convert,omitempty"`
}

// Overview builds GET /api/cluster/updates.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	pl, pol, wm, err := s.Plan(ctx)
	if err != nil {
		return Overview{}, err
	}
	cat, err := s.Store.Catalog(ctx)
	if err != nil {
		return Overview{}, err
	}
	run, err := s.Store.ActiveRun(ctx)
	if err != nil {
		return Overview{}, err
	}
	st := s.Store.LoadState(ctx)
	if cat == nil {
		cat = []Release{}
	}
	o := Overview{Current: NormalizeVersion(s.Current), Watermark: wm, Policy: pol,
		AutoInstallAvailable: AutoInstallAvailable(), Plan: pl, Run: run, Catalog: cat,
		LastCheckOK: st.LastCheckOK, LastCheckError: st.LastCheckErr, TimeZone: s.loc().String(),
		LastTick: s.LastTick()}
	if pl.Target != nil && run == nil {
		o.Stuck = s.stuck(ctx, pl.Target.Version)
	}
	if st.LastCheckOK != nil {
		o.CheckStale = s.now().Sub(*st.LastCheckOK) >= StaleAfter
	}
	state := "up_to_date"
	switch {
	case run != nil:
		state = "updating"
	case pl.Target != nil:
		state = "behind"
	}
	o.Machines = []Machine{{ID: "home", Name: "home", Role: "home", Version: o.Current, State: state}}
	return o, nil
}

// Get serves GET /api/cluster/updates.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	o, err := h.Svc.Overview(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("updates: "+err.Error()))
		return
	}
	if h.Driver != nil && len(o.Machines) > 0 {
		if o.Run != nil {
			if kind, det, err := h.Svc.Store.RunDetailOf(r.Context(), o.Run.ID); err == nil {
				o.Machines[0].Kind = kind
				if !det.NoHome {
					o.Machines[0].Target = NormalizeVersion(o.Run.Version)
				}
				// the home's desktop app waits for a click there (owner decision D1)
				if o.Run.State == RunHomeApplying && det.Home != nil {
					switch det.Home.State {
					case MAwaitingClick:
						o.Machines[0].State = TargetWaitingClick
					case MClicked:
						o.Machines[0].State = TargetClicked
					}
				}
			}
		}
		o.Machines = h.Driver.Machines(r.Context(), o.Machines[0])
	}
	if runs, err := h.Svc.Store.Runs(r.Context(), 5); err == nil {
		for i := range runs {
			if runs[i].FinishedAt != nil {
				o.Last = &runs[i]
				break
			}
		}
	}
	common.WriteJSON(w, r, http.StatusOK, o)
}

// policyErrCode maps a Policy.Validate error to its 400 code.
func policyErrCode(err error) string {
	for _, e := range []error{ErrPinBelowInstalled, ErrBadPin, ErrBadWindow, ErrBadChannel, ErrBadDeferral} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "bad_settings"
}

// installed is the floor a pin may not go below: the watermark, else the running
// version.
func (s *Service) installed(ctx context.Context) (string, error) {
	wm, err := s.Store.Watermark(ctx)
	if err != nil {
		return "", err
	}
	return maxVersion(wm, NormalizeVersion(s.Current)), nil
}

// PutSettings serves PUT /api/cluster/updates/settings: rules.updates keys, validated as
// a whole (pin below the installed version: 400 pin_below_installed), then stored by the
// rules handler and audited.
func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	var set map[string]json.RawMessage
	if e := common.ReadJSON(r, &set, maxBody); e != nil {
		httperror.Write(w, r, e)
		return
	}
	h.save(w, r, p, set, "settings_changed")
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request, p *principal.Principal, set map[string]json.RawMessage, action string) {
	ctx := r.Context()
	cur, err := h.Svc.Policy(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("updates policy: "+err.Error()))
		return
	}
	if raw, ok := set["deferred_until"]; ok {
		var v string
		if json.Unmarshal(raw, &v) != nil {
			bad(w, r, ErrBadDeferral.Error(), "deferred_until: an RFC 3339 instant or \"\"")
			return
		}
		if _, err := ParseInstant(v); err != nil {
			bad(w, r, ErrBadDeferral.Error(), "deferred_until: "+err.Error())
			return
		}
	}
	next := cur.ApplyOverrides(set)
	inst, err := h.Svc.installed(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("updates watermark: "+err.Error()))
		return
	}
	if err := next.Validate(inst); err != nil {
		bad(w, r, policyErrCode(err), err.Error())
		return
	}
	if h.RulesPatch == nil {
		httperror.Write(w, r, httperror.Internal("updates: no settings store"))
		return
	}
	body, _ := json.Marshal(map[string]any{"set": set})
	req := r.Clone(ctx)
	req.Method = http.MethodPatch
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	rc := chi.NewRouteContext()
	rc.URLParams.Add("section", Section)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rec := &capture{header: http.Header{}}
	h.RulesPatch(rec, req)
	if rec.code == 0 {
		rec.code = http.StatusOK
	}
	if rec.code >= 200 && rec.code < 300 {
		names := make([]string, 0, len(set))
		for k := range set {
			names = append(names, k)
		}
		sort.Strings(names)
		payload := map[string]any{"set": names}
		for k, v := range set {
			payload[k] = json.RawMessage(v)
		}
		h.Svc.Store.Audit(ctx, p.UserID, "admin", action, "", payload)
	}
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.code)
	_, _ = w.Write(rec.body.Bytes())
}

// Run serves POST /api/cluster/updates/run (Update now).
func (h *Handler) Run(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	var req struct {
		Version string `json:"version"`
		Confirm bool   `json:"confirm"`
	}
	if e := common.ReadJSON(r, &req, maxBody); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !req.Confirm {
		bad(w, r, "confirm_required", "Update now needs confirm:true")
		return
	}
	if req.Version != "" && !ValidVersion(req.Version) {
		bad(w, r, "bad_version", "version: want 1.2.3")
		return
	}
	h.runNow(w, r, p, req.Version)
}

func (h *Handler) runNow(w http.ResponseWriter, r *http.Request, p *principal.Principal, version string) {
	run, err := h.Svc.RunNow(r.Context(), p.UserID, version)
	var blk *BlockedError
	var wd *withdrawnError
	switch {
	case err == nil:
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"run_id": run.ID, "version": run.Version})
	case errors.Is(err, ErrNoTarget):
		httperror.Write(w, r, httperror.Conflict("about:blank#no_update", "no verified release to install").With("code", "no_update"))
	case errors.Is(err, ErrRunActive):
		httperror.Write(w, r, httperror.Conflict("about:blank#run_active", "an update is running").With("code", BlockActiveRun))
	case errors.As(err, &blk):
		httperror.Write(w, r, httperror.Conflict("about:blank#blocked", "an update cannot start now: "+blk.Reason).With("code", blk.Reason))
	case errors.As(err, &wd):
		httperror.Write(w, r, httperror.Conflict("about:blank#withdrawn", "the release was withdrawn: "+wd.reason).With("code", "release_withdrawn"))
	case errors.Is(err, ErrAutoInstallNotBuilt):
		httperror.Write(w, r, (&httperror.Error{Type: "about:blank#auto_install_not_built", Title: "service unavailable",
			Status: http.StatusServiceUnavailable, Detail: AutoInstallPendingMessage}).
			With("code", ErrAutoInstallNotBuilt.Error()))
	case errors.Is(err, ErrOrchestratorUnavailable):
		httperror.Write(w, r, (&httperror.Error{Type: "about:blank#orchestrator_unavailable", Title: "service unavailable",
			Status: http.StatusServiceUnavailable, Detail: "this build cannot install updates by itself yet"}).
			With("code", ErrOrchestratorUnavailable.Error()).With("run_id", run.ID))
	default:
		httperror.Write(w, r, httperror.Internal("update now: "+err.Error()))
	}
}

// Unblock serves POST /api/cluster/updates/unblock {version}: an admin's retry of a
// release blocked after its failures (or a rollback): the next window installs it, or
// Update now naming it does at once.
func (h *Handler) Unblock(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if e := common.ReadJSON(r, &req, maxBody); e != nil {
		httperror.Write(w, r, e)
		return
	}
	v := NormalizeVersion(req.Version)
	if !ValidVersion(v) {
		bad(w, r, "bad_version", "version: want 1.2.3")
		return
	}
	done, err := h.Svc.Unblock(r.Context(), p.UserID, v)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("unblock: "+err.Error()))
		return
	}
	if !done {
		httperror.Write(w, r, httperror.Conflict("about:blank#not_blocked",
			"the release is not blocked, or it is withdrawn or unverified").With("code", "not_blocked"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"unblocked": true, "version": v})
}

// Skip serves POST /api/cluster/updates/skip {version}.
func (h *Handler) Skip(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if e := common.ReadJSON(r, &req, maxBody); e != nil {
		httperror.Write(w, r, e)
		return
	}
	v := NormalizeVersion(req.Version)
	if !ValidVersion(v) {
		bad(w, r, "bad_version", "version: want 1.2.3")
		return
	}
	pol, err := h.Svc.Policy(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("updates policy: "+err.Error()))
		return
	}
	list := append([]string{}, pol.SkippedVersions...)
	if !pol.Skipped(v) {
		list = append(list, v)
	}
	raw, _ := json.Marshal(list)
	h.save(w, r, p, map[string]json.RawMessage{"skipped_versions": raw}, "release_skipped")
}

// Defer serves POST /api/cluster/updates/defer {until}.
func (h *Handler) Defer(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	var req struct {
		Until string `json:"until"`
	}
	if e := common.ReadJSON(r, &req, maxBody); e != nil {
		httperror.Write(w, r, e)
		return
	}
	raw, _ := json.Marshal(strings.TrimSpace(req.Until))
	h.save(w, r, p, map[string]json.RawMessage{"deferred_until": raw}, "updates_deferred")
}

// Check serves POST /api/cluster/updates/check (check now).
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	if h.Svc.Discoverer == nil {
		httperror.Write(w, r, httperror.Conflict("about:blank#disabled", "update checks are off").With("code", "disabled"))
		return
	}
	res, err := h.Svc.Discoverer.Check(r.Context())
	if err != nil {
		if errors.Is(err, ErrRateLimited) {
			w.Header().Set("Retry-After", "60")
			httperror.Write(w, r, httperror.Unavailable(60))
			return
		}
		httperror.Write(w, r, httperror.BadGateway("check GitHub: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}

// Runs serves GET /api/cluster/updates/runs.
func (h *Handler) Runs(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	limit, e := common.QueryInt(r, "limit", 50)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	runs, err := h.Svc.Store.Runs(r.Context(), limit)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("update runs: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": runs})
}

// AdminUpdateHandler is POST /api/admin/system/update (Epic 28.3) mapped to Update now
// on the home (story 35.3 AC7): {run_id}. Used once this build can install by itself.
func (h *Handler) AdminUpdateHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := admin(w, r)
		if p == nil {
			return
		}
		h.runNow(w, r, p, "")
	})
}

// ExtendStatus wraps GET /api/system/updates (Epic 28.2) with verified, policy and
// next_run_at (story 35.3 AC10).
func (h *Handler) ExtendStatus(base http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &capture{header: http.Header{}}
		base.ServeHTTP(rec, r)
		var doc map[string]any
		if (rec.code != 0 && rec.code != http.StatusOK) || json.Unmarshal(rec.body.Bytes(), &doc) != nil {
			for k, v := range rec.header {
				w.Header()[k] = v
			}
			if rec.code == 0 {
				rec.code = http.StatusOK
			}
			w.WriteHeader(rec.code)
			_, _ = w.Write(rec.body.Bytes())
			return
		}
		ctx := r.Context()
		if pl, pol, _, err := h.Svc.Plan(ctx); err == nil {
			doc["policy"] = map[string]any{"auto_install": pol.EffectiveAutoInstall(), "channel": pol.Channel,
				"window": pol.Window, "min_release_age_hours": pol.MinReleaseAgeHours, "pinned_version": pol.PinnedVersion}
			doc["next_run_at"] = pl.NextRunAt
			if pl.Held {
				doc["held"] = true
			}
		}
		verified := false
		if v, _ := doc["latest_version"].(string); v != "" {
			if rel, err := h.Svc.Store.Get(ctx, v); err == nil {
				verified = rel.VerifyState == VerifyVerified
			}
		}
		doc["verified"] = verified
		common.WriteJSON(w, r, http.StatusOK, doc)
	})
}

// capture is an in-memory ResponseWriter for the delegated handlers.
type capture struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (c *capture) Header() http.Header { return c.header }
func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}
func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}
