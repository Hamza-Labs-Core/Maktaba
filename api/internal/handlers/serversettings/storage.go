package serversettings

// Where the server keeps its data, and its move to another disk (Epic 36 story
// 36.15), for the web's Settings → Storage page:
//
//	GET  /api/server/storage         each folder's place and size, the disks' free
//	                                 space, the last move ({available: false} without
//	                                 a supervisor)
//	GET  /api/server/storage/disks   the disks a move may go to
//	POST /api/server/storage/check   {target}: what a move there would do, and why not
//	POST /api/server/storage/move    {target}: 202, the server stops every process,
//	                                 copies, verifies, points server.toml there and
//	                                 starts again (422 with the plan's problems)
//
// They proxy the supervisor's /config/storage. Admins only (403 before the
// supervisor is called); a move asked for is audited (category 'server').

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// ActionStorageMoveRequested is the audit action of a move asked for.
const ActionStorageMoveRequested = "storage-move-requested"

// storageTimeout: the supervisor adds up every folder's size for the page.
const storageTimeout = 2 * time.Minute

// mountStorage wires the storage routes.
func (h *Handler) mountStorage(r chi.Router) {
	r.Get("/api/server/storage", h.admin(h.storageGet("storage")))
	r.Get("/api/server/storage/disks", h.admin(h.storageGet("storage/disks")))
	r.Post("/api/server/storage/check", h.admin(h.storagePost("storage/check")))
	r.Post("/api/server/storage/move", h.admin(h.storagePost("storage/move")))
}

func (h *Handler) storageSupervisor() *Supervisor {
	s := *h.Supervisor
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: storageTimeout}
	}
	return &s
}

func (h *Handler) storageGet(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Supervisor == nil {
			common.WriteJSON(w, r, http.StatusOK, map[string]any{"available": false, "reason": errNoSupervisor.Error()})
			return
		}
		code, body, err := h.storageSupervisor().call(r.Context(), http.MethodGet, path, nil)
		if err == nil && code != http.StatusOK {
			err = &supervisorAnswer{code: code, body: body}
		}
		if err != nil {
			common.WriteJSON(w, r, http.StatusOK, map[string]any{"available": false, "reason": err.Error()})
			return
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			httperror.Write(w, r, httperror.BadGateway("the supervisor's answer").Wrap(err))
			return
		}
		out["available"] = true
		common.WriteJSON(w, r, http.StatusOK, out)
	}
}

type supervisorAnswer struct {
	code int
	body []byte
}

func (e *supervisorAnswer) Error() string {
	return "the supervisor answered " + http.StatusText(e.code) + ": " + strings.TrimSpace(string(e.body))
}

func (h *Handler) storagePost(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Supervisor == nil {
			httperror.Write(w, r, httperror.Conflict("no-supervisor", errNoSupervisor.Error()))
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, common.MaxBodyBytes))
		if err != nil {
			httperror.Write(w, r, httperror.BadRequest("body too large"))
			return
		}
		var in struct {
			Target string `json:"target"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			httperror.Write(w, r, httperror.BadRequest("the body must be {\"target\": \"<folder>\"}"))
			return
		}
		by := "an admin"
		if p := principal.FromContext(r.Context()); p != nil && p.UserID != "" {
			by = "the web (user " + p.UserID + ")"
		}
		body, _ := json.Marshal(map[string]string{"target": in.Target, "by": by})
		code, out, err := h.storageSupervisor().call(r.Context(), http.MethodPost, path, body)
		if err != nil {
			httperror.Write(w, r, httperror.BadGateway("the server's supervisor did not answer").Wrap(err))
			return
		}
		if path == "storage/move" && code == http.StatusAccepted {
			h.audit(r, ActionStorageMoveRequested, map[string]any{"target": in.Target})
		}
		// the plan (200, 422) and the request (202) go back as the supervisor gave them
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(out)
	}
}
