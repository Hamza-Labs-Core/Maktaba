package plugins

// The store's catalog on the home (story 54.17 §5), admin only: a viewer never triggers a
// store fetch (the route answers 403 before anything runs).

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/catalog"
)

func (h *Admin) catalogReady(w http.ResponseWriter, r *http.Request) bool {
	if h.Catalog == nil {
		httperror.Write(w, r, problem(http.StatusServiceUnavailable, "catalog_unavailable", "this server has no store catalog"))
		return false
	}
	return true
}

func (h *Admin) catalogFail(w http.ResponseWriter, r *http.Request, err error) {
	var p *catalog.Problem
	if errors.As(err, &p) {
		httperror.Write(w, r, problem(p.Status, p.Code, p.Msg))
		return
	}
	h.fail(w, r, err)
}

func (h *Admin) catalogView(w http.ResponseWriter, r *http.Request) {
	if !h.catalogReady(w, r) {
		return
	}
	v, err := h.Catalog.Browse(r.Context(), h.Server, h.Platform, r.URL.Query().Get("all") == "true")
	if err != nil {
		h.catalogFail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, v)
}

func (h *Admin) trust(w http.ResponseWriter, r *http.Request) {
	if !h.catalogReady(w, r) {
		return
	}
	common.WriteJSON(w, r, http.StatusOK, h.Catalog.Trust(r.Context()))
}

func (h *Admin) autoUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.catalogReady(w, r) {
		return
	}
	var b struct {
		On bool `json:"on"`
	}
	if e := common.ReadJSON(r, &b, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if err := h.Catalog.SetAutoUpdate(r.Context(), chi.URLParam(r, "id"), b.On); err != nil {
		h.catalogFail(w, r, err)
		return
	}
	h.show(w, r)
}

func (h *Admin) installFromStore(w http.ResponseWriter, r *http.Request, expect string) {
	if !h.catalogReady(w, r) {
		return
	}
	var b struct {
		Store struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"store"`
		Grants       []string `json:"grants"`
		Libraries    []string `json:"libraries"`
		AllLibraries bool     `json:"all_libraries"`
		Enable       bool     `json:"enable"`
	}
	if e := common.ReadJSON(r, &b, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if b.Store.ID == "" || b.Store.Version == "" || (expect != "" && b.Store.ID != expect) {
		httperror.Write(w, r, problem(http.StatusBadRequest, "plugin_install_form", "store: {id, version}"))
		return
	}
	req := &pl.InstallRequest{ExpectID: expect, Enable: b.Enable}
	if b.Grants != nil || b.Libraries != nil || b.AllLibraries {
		req.Grants, req.Libraries, req.AllLibraries, req.GrantsSet = b.Grants, b.Libraries, b.AllLibraries, true
	}
	res, err := h.Catalog.InstallFromStore(r.Context(), h.L, b.Store.ID, b.Store.Version, req, h.actor(r))
	if err != nil {
		h.catalogFail(w, r, err)
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
