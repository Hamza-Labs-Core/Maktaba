package plugins

// The admin routes of Epic 54 Phase 1 (stories 54.07, 54.08 §3, 54.10, 54.11):
//
//	GET    /api/plugins                      installed plugins
//	GET    /api/plugins/policy               the home's policy (allow_unreviewed)
//	GET    /api/plugins/graph                the home's plugin graph and its findings (54.39, 54.41)
//	GET    /api/plugins/{id}/downstream      other plugins' stages that go idle when it is off
//	GET    /api/plugins/{id}/flow            its flow: the whole set, each member's state, the missing (54.41)
//	PUT    /api/plugins/policy               change it
//	POST   /api/plugins/install              multipart: file, grants, libraries, all_libraries, enable, confirm_name
//	GET    /api/plugins/{id}                 one plugin's detail
//	PUT    /api/plugins/{id}/grants          {capabilities, libraries, all_libraries}
//	POST   /api/plugins/{id}/enable          want enabled (clears keep-off)
//	POST   /api/plugins/{id}/disable         want installed (sets keep-off)
//	POST   /api/plugins/{id}/start, /stop    the Epic 51 hold
//	POST   /api/plugins/{id}/update          multipart: a new version of this plugin
//	POST   /api/plugins/{id}/rollback        ?clear_data=true
//	DELETE /api/plugins/{id}                 ?purge=true
//	GET    /api/plugins/{id}/writes          ?before=&limit=
//	POST   /api/plugins/{id}/writes/revert   {ids: [...]}
//	…/api/plugins/store-account              linking to a store account (store.go, 54.15 §5)
//	GET    /api/plugins/catalog              the store's catalog for this server (catalog.go, 54.17)
//	GET    /api/plugins/trust                the signed lists' state (withdrawn plugins checked when)
//	POST   /api/plugins/install (JSON)       {store: {id, version}}: install from the store
//	PUT    /api/plugins/{id}/auto-update     {on}
//
// Every one is admin-only: no sign-in 401; a viewer, a cloud-linked play sign-in (its
// scope clears IsAdmin) and a token without the admin scope 403. Switches go through the
// components service, so they write the rows and audit actions of /api/components/plugin.<id>.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/catalog"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/storelink"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// maxPackage is the largest package an install takes (54.01: 50 MB packed), and a margin
// for the form's fields.
const maxPackage = 50<<20 + 1<<20

// Admin serves the routes.
type Admin struct {
	L      *pl.Lifecycle
	Logger *slog.Logger
	// VerifyPassword checks the signed-in admin's password again (story 54.10 §2: turning
	// on Allow unreviewed plugins asks for it); nil: none is asked.
	VerifyPassword func(ctx context.Context, userID, password string) error
	// Store links this home to a plugin store account (story 54.15 §5; nil: 503).
	Store *storelink.Link
	// Catalog is the store's catalog and the kill switch (story 54.17; nil: 503).
	Catalog *catalog.Service
	// Server and Platform are this server's version and platform (Browse's filter).
	Server   string
	Platform catalog.Platform
}

// Mount adds the routes to r.
func (h *Admin) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(adminOnly)
		r.Get("/api/plugins", h.list)
		r.Get("/api/plugins/policy", h.getPolicy)
		r.Get("/api/plugins/graph", h.graph)
		r.Put("/api/plugins/policy", h.putPolicy)
		r.Post("/api/plugins/install", h.install)
		r.Get("/api/plugins/panel-order", h.getPanelOrder)
		r.Put("/api/plugins/panel-order", h.putPanelOrder)
		r.Get("/api/plugins/catalog", h.catalogView)
		r.Get("/api/plugins/trust", h.trust)
		r.Put("/api/plugins/{id}/auto-update", h.autoUpdate)
		r.Get("/api/plugins/store-account", h.storeStatus)
		r.Post("/api/plugins/store-account/link", h.storeLink)
		r.Post("/api/plugins/store-account/poll", h.storePoll)
		r.Delete("/api/plugins/store-account", h.storeUnlink)
		r.Get("/api/plugins/{id}", h.show)
		r.Put("/api/plugins/{id}/grants", h.grants)
		r.Put("/api/plugins/{id}/report-logs", h.reportLogs)
		r.Post("/api/plugins/{id}/enable", h.switchTo(components.WantEnabled))
		r.Post("/api/plugins/{id}/disable", h.switchTo(components.WantInstalled))
		r.Post("/api/plugins/{id}/start", h.hold(false))
		r.Post("/api/plugins/{id}/stop", h.hold(true))
		r.Post("/api/plugins/{id}/update", h.update)
		r.Post("/api/plugins/{id}/rollback", h.rollback)
		r.Delete("/api/plugins/{id}", h.remove)
		r.Get("/api/plugins/{id}/writes", h.writes)
		r.Get("/api/plugins/{id}/downstream", h.downstream)
		r.Get("/api/plugins/{id}/flow", h.flow)
		r.Get("/api/plugins/{id}/settings", h.getSettings)
		r.Put("/api/plugins/{id}/settings", h.putSettings)
		r.Post("/api/plugins/{id}/writes/revert", h.revert)
	})
}

func adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principal.FromContext(r.Context())
		if p == nil {
			httperror.Write(w, r, problem(http.StatusUnauthorized, "auth_required", "sign in as an admin"))
			return
		}
		if !p.IsAdmin {
			httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func problem(status int, code, detail string) *httperror.Error {
	return (&httperror.Error{
		Type:   "https://maktaba.tv/problems/" + strings.ReplaceAll(code, "_", "-"),
		Title:  strings.ReplaceAll(code, "_", " "),
		Status: status,
		Detail: detail,
	}).With("code", code)
}

func (h *Admin) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// fail writes err: a lifecycle or components refusal as its problem, else 500.
func (h *Admin) fail(w http.ResponseWriter, r *http.Request, err error) {
	var p *pl.Problem
	if errors.As(err, &p) {
		e := problem(p.Status, p.Code, p.Detail)
		for k, v := range p.Params {
			e = e.With(k, v)
		}
		httperror.Write(w, r, e)
		return
	}
	if ref, ok := svc.AsRefusal(err); ok {
		e := problem(ref.Status, ref.Code, ref.Detail)
		if ref.RequiredBy != nil {
			e = e.With("required_by", ref.RequiredBy)
		}
		if ref.ID != "" {
			e = e.With("id", ref.ID)
		}
		httperror.Write(w, r, e)
		return
	}
	h.log().ErrorContext(r.Context(), "plugins: request failed", "err", err, "event", "plugins_error")
	httperror.Write(w, r, httperror.Internal("plugins"))
}

// actor is who asks, as the components routes name them.
func (h *Admin) actor(r *http.Request) svc.Actor {
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
		var name string
		if err := h.L.DB.QueryRowContext(r.Context(), `SELECT username FROM users WHERE id::text = $1`, p.UserID).Scan(&name); err == nil && name != "" {
			a.By = name
		}
	}
	return a
}

func (h *Admin) list(w http.ResponseWriter, r *http.Request) {
	v, err := h.L.List(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	pol, err := pl.LoadPolicy(r.Context(), h.L.DB)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"plugins": v, "policy": pol, "built_in": builtIn()})
}

// BuiltIn is one of Maktaba's own first-party plugins as the Plugins page lists it under
// "Built into Maktaba" (story 54.22 §4): its name in both languages, its component (the
// Components page's switch is its switch: no grants dialog), its stages, and what it can do
// (fp.trusted.<capability> words with their parameter).
type BuiltIn struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	NameAr       string              `json:"name_ar"`
	Summary      string              `json:"summary"`
	SummaryAr    string              `json:"summary_ar"`
	Component    string              `json:"component"`
	Stages       []string            `json:"stages"`
	Capabilities []map[string]string `json:"capabilities"`
}

func builtIn() []BuiltIn {
	list, err := firstparty.Plugins()
	if err != nil {
		return []BuiltIn{}
	}
	out := make([]BuiltIn, 0, len(list))
	for _, fp := range list {
		strs, _ := fp.Manifest["strings"].(map[string]any)
		word := func(lang, key string) string {
			k, _ := strs[key].(string)
			return fp.Strings[lang][k]
		}
		out = append(out, BuiltIn{ID: fp.ID, Name: word("en", "name"), NameAr: word("ar", "name"),
			Summary: word("en", "summary"), SummaryAr: word("ar", "summary"), Component: fp.Trusted.Component,
			Stages: fp.Stages, Capabilities: fp.Capabilities()})
	}
	return out
}

func (h *Admin) graph(w http.ResponseWriter, r *http.Request) {
	g, err := h.L.Graph(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, g)
}

func (h *Admin) downstream(w http.ResponseWriter, r *http.Request) {
	d, err := h.L.Downstream(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"stages": d})
}

func (h *Admin) flow(w http.ResponseWriter, r *http.Request) {
	v, err := h.L.Flow(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, v)
}

func (h *Admin) show(w http.ResponseWriter, r *http.Request) {
	d, err := h.L.Show(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, d)
}

func (h *Admin) getPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := pl.LoadPolicy(r.Context(), h.L.DB)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, p)
}

func (h *Admin) putPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		pl.Policy
		Password string `json:"password"`
	}
	if e := common.ReadJSON(r, &body, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	p := body.Policy
	if p.AllowUnreviewed && h.VerifyPassword != nil {
		// a sensitive change: the admin's password again (401 reauth_required)
		cur, err := pl.LoadPolicy(r.Context(), h.L.DB)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if !cur.AllowUnreviewed {
			pr := principal.FromContext(r.Context())
			if body.Password == "" || pr == nil || h.VerifyPassword(r.Context(), pr.UserID, body.Password) != nil {
				httperror.Write(w, r, problem(http.StatusUnauthorized, "reauth_required", "type your password again to allow unreviewed plugins"))
				return
			}
		}
	}
	out, err := h.L.SetPolicy(r.Context(), p, h.actor(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// installForm reads a multipart install: the package's part streams to the supervisor.
func (h *Admin) installForm(w http.ResponseWriter, r *http.Request, expect string) (*pl.InstallRequest, func(), bool) {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "multipart/form-data" {
		httperror.Write(w, r, problem(http.StatusBadRequest, "plugin_install_form", "a multipart form with the package as file"))
		return nil, nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPackage)
	mr, err := r.MultipartReader()
	if err != nil {
		httperror.Write(w, r, problem(http.StatusBadRequest, "plugin_install_form", err.Error()))
		return nil, nil, false
	}
	req := &pl.InstallRequest{ExpectID: expect}
	// the fields come before the file: the file's part is the stream the supervisor reads
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			httperror.Write(w, r, problem(http.StatusBadRequest, "plugin_install_form", err.Error()))
			return nil, nil, false
		}
		if part.FormName() == "file" {
			req.Package = part
			return req, func() { _ = part.Close() }, true
		}
		b, _ := io.ReadAll(io.LimitReader(part, 64<<10))
		v := strings.TrimSpace(string(b))
		switch part.FormName() {
		case "grants":
			req.GrantsSet = true
			if err := json.Unmarshal([]byte(v), &req.Grants); err != nil && v != "" {
				httperror.Write(w, r, problem(http.StatusBadRequest, "plugin_install_form", "grants is a JSON array"))
				return nil, nil, false
			}
		case "libraries":
			req.GrantsSet = true
			if err := json.Unmarshal([]byte(v), &req.Libraries); err != nil && v != "" {
				httperror.Write(w, r, problem(http.StatusBadRequest, "plugin_install_form", "libraries is a JSON array"))
				return nil, nil, false
			}
		case "all_libraries":
			req.GrantsSet = true
			req.AllLibraries, _ = strconv.ParseBool(v)
		case "enable":
			req.Enable, _ = strconv.ParseBool(v)
		case "confirm_name":
			req.ConfirmName = v
		}
		_ = part.Close()
	}
	httperror.Write(w, r, problem(http.StatusBadRequest, "plugin_install_form", "no file part (send the fields first, the file last)"))
	return nil, nil, false
}

func (h *Admin) install(w http.ResponseWriter, r *http.Request) {
	h.installAs(w, r, "")
}

func (h *Admin) update(w http.ResponseWriter, r *http.Request) {
	h.installAs(w, r, chi.URLParam(r, "id"))
}

func (h *Admin) installAs(w http.ResponseWriter, r *http.Request, expect string) {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "application/json" {
		// {store: {id, version}, grants…}: install or update from the store (54.17 §5)
		h.installFromStore(w, r, expect)
		return
	}
	req, done, ok := h.installForm(w, r, expect)
	if !ok {
		return
	}
	defer done()
	res, err := h.L.Install(r.Context(), *req, h.actor(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d, err := h.L.Show(r.Context(), res.PluginID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if res.Already || res.Updated {
		status = http.StatusOK
	}
	common.WriteJSON(w, r, status, map[string]any{"result": res, "plugin": d})
}

// reportLogs is PUT /api/plugins/{id}/report-logs {include}: whether problem reports carry
// the plugin's log lines (story 54.09 §3).
func (h *Admin) reportLogs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Include *bool `json:"include"`
	}
	if e := common.ReadJSON(r, &body, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if body.Include == nil {
		httperror.Write(w, r, problem(http.StatusBadRequest, "plugin_invalid", "include is required"))
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.L.SetReportLogs(r.Context(), id, *body.Include, h.actor(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"plugin_id": id, "report_logs": *body.Include})
}

func (h *Admin) grants(w http.ResponseWriter, r *http.Request) {
	var g pl.GrantSet
	if e := common.ReadJSON(r, &g, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	out, err := h.L.SetGrants(r.Context(), chi.URLParam(r, "id"), g, h.actor(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// apply applies one change of plugin.<id> through the components service and answers the
// plugin as it now is.
func (h *Admin) apply(w http.ResponseWriter, r *http.Request, ch svc.Change) {
	id := chi.URLParam(r, "id")
	ch.ID = components.PluginPrefix + id
	if _, err := h.L.Show(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	if _, err := h.L.Components.Apply(r.Context(), svc.Request{Changes: []svc.Change{ch}}, h.actor(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	h.show(w, r)
}

func (h *Admin) switchTo(want string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h.apply(w, r, svc.Change{Want: want}) }
}

func (h *Admin) hold(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		held := on
		h.apply(w, r, svc.Change{Hold: &held})
	}
}

func (h *Admin) rollback(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.L.Rollback(r.Context(), id, r.URL.Query().Get("clear_data") == "true", h.actor(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	h.show(w, r)
}

// remove is want absent (the supervisor removes the files once the jobs drained), and with
// purge=true the plugin's settings, data and artifacts, and its writes reverted.
func (h *Admin) remove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cid := components.PluginPrefix + id
	if _, err := h.L.Show(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	queued := 0
	_ = h.L.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
		WHERE s.extension = $1 AND j.state = 'pending'`, "plugin:"+id).Scan(&queued)
	actor := h.actor(r)
	if _, err := h.L.Components.Apply(r.Context(), svc.Request{Changes: []svc.Change{{ID: cid, Want: components.WantAbsent}}}, actor); err != nil {
		h.fail(w, r, err)
		return
	}
	out := map[string]any{"id": id, "want": components.WantAbsent, "queued_jobs_dropped": queued}
	if r.URL.Query().Get("purge") == "true" {
		if _, err := h.L.Components.ResetSettings(r.Context(), cid, actor); err != nil {
			h.fail(w, r, err)
			return
		}
		n, err := h.L.Components.PurgePlugin(r.Context(), cid, actor)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		out["writes_reverted"] = n
	}
	common.WriteJSON(w, r, http.StatusAccepted, out)
}

func (h *Admin) writes(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := h.L.Writes(r.Context(), chi.URLParam(r, "id"), before, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"writes": out})
}

func (h *Admin) revert(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if e := common.ReadJSON(r, &in, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if len(in.IDs) == 0 {
		httperror.Write(w, r, problem(http.StatusBadRequest, "plugin_invalid", "name the writes to revert"))
		return
	}
	n, err := h.L.Revert(r.Context(), chi.URLParam(r, "id"), in.IDs, h.actor(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"reverted": n})
}

func (h *Admin) getSettings(w http.ResponseWriter, r *http.Request) {
	out, err := h.L.GetSettings(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Admin) putSettings(w http.ResponseWriter, r *http.Request) {
	var values map[string]any
	if e := common.ReadJSON(r, &values, 256<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	out, err := h.L.PutSettings(r.Context(), chi.URLParam(r, "id"), values, h.actor(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// GET /api/plugins/panel-order: the plugins with panels, in the order pages show them
// (story 54.20 §1), and the stored order.
func (h *Admin) getPanelOrder(w http.ResponseWriter, r *http.Request) {
	list, order, err := h.L.PanelPlugins(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"plugins": list, "order": order})
}

// PUT /api/plugins/panel-order {"order": [plugin ids]}.
func (h *Admin) putPanelOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Order []string `json:"order"`
	}
	if e := common.ReadJSON(r, &body, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	order, err := h.L.SetPanelOrder(r.Context(), body.Order, h.actor(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"order": order})
}
