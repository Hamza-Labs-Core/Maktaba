package cluster

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Story 31.3: the shares behind the canonical media paths.
//
//	GET    /api/cluster/storage-servers           admin: the storage servers
//	PUT    /api/cluster/storage-servers/{name}    admin: {addresses, char_map?, exports?}
//	DELETE /api/cluster/storage-servers/{name}    admin
//	PUT    /api/cluster/home/path-map             admin: {path_map: {share: local} | null}
//
// GET /api/cluster carries the home's per-share storage check and its
// override as home.storage / home.path_map.

// maxServerAddresses bounds a server's address list.
const maxServerAddresses = 32

// charMapRE: a preset or a catia list "0x22:0xff02,0x3a:0xff1a".
var charMapRE = regexp.MustCompile(`^(?:none|fullwidth|sfm|0x[0-9a-fA-F]{1,6}:0x[0-9a-fA-F]{1,6}(?:\s*,\s*0x[0-9a-fA-F]{1,6}:0x[0-9a-fA-F]{1,6})*)$`)

// ServerRequest is the body of PUT /api/cluster/storage-servers/{name}.
type ServerRequest struct {
	Addresses []string          `json:"addresses"`
	CharMap   string            `json:"char_map"`
	Exports   map[string]string `json:"exports"`
}

// HomePathMapRequest is the body of PUT /api/cluster/home/path-map.
type HomePathMapRequest struct {
	PathMap json.RawMessage `json:"path_map"`
}

func (h *Handler) mountStorage(r chi.Router) {
	r.Get("/api/cluster/storage-servers", h.ListServers)
	r.Put("/api/cluster/storage-servers/{name}", h.PutServer)
	r.Delete("/api/cluster/storage-servers/{name}", h.DeleteServer)
	r.Put("/api/cluster/home/path-map", h.SetHomePathMap)
}

func (h *Handler) storageOr404(w http.ResponseWriter, r *http.Request) bool {
	if h.Storage == nil {
		httperror.Write(w, r, problem(http.StatusNotFound, "not_found", "storage servers unavailable", ""))
		return false
	}
	return true
}

// ListServers implements GET /api/cluster/storage-servers.
func (h *Handler) ListServers(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) || !h.storageOr404(w, r) {
		return
	}
	out, err := h.Storage.Servers(r.Context())
	if err != nil {
		errInternal(w, r, "list storage servers", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"servers": out})
}

// PutServer implements PUT /api/cluster/storage-servers/{name}: create or
// replace a server's addresses (an IP change is one edit; no stored path
// changes, they name the server).
func (h *Handler) PutServer(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) || !h.storageOr404(w, r) {
		return
	}
	var req ServerRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if len(req.Addresses) > maxServerAddresses {
		httperror.Write(w, r, httperror.BadRequest("too many addresses"))
		return
	}
	cm := strings.TrimSpace(req.CharMap)
	if cm != "" && !charMapRE.MatchString(cm) {
		httperror.Write(w, r, httperror.BadRequest("char_map: none, fullwidth, sfm or 0xDISK:0xCLIENT,..."))
		return
	}
	for path, share := range req.Exports {
		if !strings.HasPrefix(path, "/") || strings.TrimSpace(share) == "" || strings.Contains(share, "/") {
			httperror.Write(w, r, httperror.BadRequest(`exports: {"/export/path": "Share"}`))
			return
		}
	}
	s, err := h.Storage.PutServer(r.Context(), cluster.StorageServer{
		Name: chi.URLParam(r, "name"), Addresses: req.Addresses, CharMap: cm, Exports: req.Exports,
	})
	if errors.Is(err, cluster.ErrBadServer) {
		httperror.Write(w, r, httperror.BadRequest(err.Error()))
		return
	}
	if err != nil {
		errInternal(w, r, "save storage server", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, s)
}

// DeleteServer implements DELETE /api/cluster/storage-servers/{name}.
func (h *Handler) DeleteServer(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) || !h.storageOr404(w, r) {
		return
	}
	err := h.Storage.DeleteServer(r.Context(), chi.URLParam(r, "name"))
	if errors.Is(err, cluster.ErrNotFound) {
		httperror.Write(w, r, problem(http.StatusNotFound, "not_found", "no such storage server", ""))
		return
	}
	if err != nil {
		errInternal(w, r, "delete storage server", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetHomePathMap implements PUT /api/cluster/home/path-map: the home's
// "share -> local mount" override (null: back to its own mounts).
func (h *Handler) SetHomePathMap(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) || !h.storageOr404(w, r) {
		return
	}
	var req HomePathMapRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	var pm json.RawMessage
	switch {
	case req.PathMap == nil || string(req.PathMap) == "null":
	case validMap(req.PathMap):
		pm = req.PathMap
	default:
		httperror.Write(w, r, httperror.BadRequest("path_map must be an object of strings or null"))
		return
	}
	if err := h.Storage.SetHomePathMap(r.Context(), pm); err != nil {
		errInternal(w, r, "set the home's path map", err)
		return
	}
	id, err := h.identity(r.Context())
	if err != nil {
		errInternal(w, r, "read identity", err)
		return
	}
	home := h.home(r.Context(), id)
	h.homeStorage(r, &home)
	common.WriteJSON(w, r, http.StatusOK, home)
}

func validMap(raw json.RawMessage) bool {
	var m map[string]string
	if json.Unmarshal(raw, &m) != nil || m == nil || len(m) > 64 {
		return false
	}
	for k, v := range m {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return false
		}
	}
	return true
}

// homeStorage fills home.storage / home.path_map.
func (h *Handler) homeStorage(r *http.Request, out *Home) {
	if h.Storage == nil {
		return
	}
	st, pm, err := h.Storage.HomeStorage(r.Context())
	if err != nil {
		return
	}
	out.Storage, out.PathMap = st, pm
}
