package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// AgentPath is the processes' endpoint (public: authentication is in the
// protocol; it must be on the auth allowlist).
const AgentPath = "/api/cluster/agent"

// Mount wires the hub's routes:
//
//	GET  /api/cluster/agent                     public (protocol auth), 30 handshakes/min per IP: processes
//	GET  /api/cluster/ws                        signed-in users: browsers
//	GET  /api/cluster/processes                 admin: the processes snapshot
//	GET  /api/cluster/files                     admin
//	GET  /api/cluster/files/{ip}/{kind}         admin
//	POST /api/cluster/processes/{session}/cmd   admin
//	GET  /api/cluster/llm/v1/models             admins (admin token or session) and personal access tokens
//	POST /api/cluster/llm/v1/chat/completions   same
//	POST /api/cluster/llm/v1/embeddings         same
func (h *Hub) Mount(r chi.Router) {
	r.Get(AgentPath, h.ServeAgent)
	r.Get("/api/cluster/ws", h.ServeBrowser)
	r.Get("/api/cluster/processes", h.ProcessesHTTP)
	r.Get("/api/cluster/files", h.Files)
	r.Get("/api/cluster/files/{ip}/{kind}", h.History)
	r.With(mw.BodyLimit(64<<10)).Post("/api/cluster/processes/{session}/cmd", h.CmdHTTP)
	r.Get("/api/cluster/llm/v1/models", h.LLMModels)
	r.Post("/api/cluster/llm/v1/chat/completions", h.LLMChat)
	r.Post("/api/cluster/llm/v1/embeddings", h.LLMEmbeddings)
}

// ProcessesHTTP implements GET /api/cluster/processes.
func (h *Hub) ProcessesHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	common.WriteJSON(w, r, http.StatusOK, h.processesSnapshot())
}

func errUnauthorized() *httperror.Error {
	return &httperror.Error{Type: "https://maktaba.dev/problems/unauthorized", Title: "unauthorized",
		Status: http.StatusUnauthorized, Detail: "authentication required"}
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

var sessionRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// CmdRequest is the body of POST /api/cluster/processes/{session}/cmd.
type CmdRequest struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// CmdHTTP implements POST /api/cluster/processes/{session}/cmd: 200 with
// the process's {id, ok, detail}; 404 no such session; 400 unknown
// command; 504 no answer within 10 s.
func (h *Hub) CmdHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	session := chi.URLParam(r, "session")
	if !sessionRe.MatchString(session) {
		httperror.Write(w, r, httperror.NotFound("process"))
		return
	}
	var req CmdRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	by := "system"
	if p := principal.FromContext(r.Context()); p != nil && p.UserID != "" {
		by = "user:" + p.UserID
	}
	res, err := h.Command(r.Context(), session, req.Name, req.Args, by)
	switch {
	case errors.Is(err, ErrNoSession):
		httperror.Write(w, r, httperror.NotFound("process"))
	case errors.Is(err, ErrBadCommand):
		httperror.Write(w, r, httperror.BadRequest("name must be drain, undrain, reload_stages, log_level (args.level) or disconnect"))
	case errors.Is(err, ErrCmdTimeout):
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/cmd-timeout", Title: "no answer",
			Status: http.StatusGatewayTimeout, Detail: "the process did not answer within 10 s"})
	case err != nil:
		httperror.Write(w, r, httperror.Internal("command failed"))
	default:
		common.WriteJSON(w, r, http.StatusOK, res)
	}
}
