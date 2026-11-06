package logs

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/serversettings"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// The Retention page and "Detailed logs for one hour" (Epic 53 story 53.11): the API
// proxies them to the supervisor's /config/logs (server.toml is the supervisor's: the API
// never writes it), admins only, each change audited (category logs).
//
//	GET  /api/admin/logs/retention  how long each kind is kept, the cap, what each uses now
//	PUT  /api/admin/logs/retention  {logs_days, errors_days, reports_days, total_bytes}
//	POST /api/admin/logs/level      {service, level: debug|info, minutes}

// Audit actions of these routes.
const (
	EventRetentionChanged = "retention-changed"
	EventDetailedLogs     = "detailed-logs"
)

func (h *Handler) supervisor() *serversettings.Supervisor {
	if h.Supervisor != nil {
		return h.Supervisor
	}
	return serversettings.SupervisorFromEnv()
}

// proxyConfig sends the request to the supervisor's path; a change that took is audited.
func (h *Handler) proxyConfig(path, event string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sup := h.supervisor()
		if sup == nil {
			httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.tv/problems/no-supervisor",
				Title: "these settings are the server's", Status: http.StatusConflict,
				Detail: "this API runs without maktaba-server's supervisor, which keeps them",
				Extras: map[string]any{"code": "no_supervisor"}})
			return
		}
		var body []byte
		if r.Method != http.MethodGet {
			b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
			if err != nil {
				httperror.Write(w, r, httperror.BadRequest("body too large"))
				return
			}
			body = b
		}
		ct := ""
		if body != nil {
			ct = "application/json"
		}
		resp, err := sup.Do(r.Context(), r.Method, path, bytes.NewReader(body), ct)
		if err != nil {
			httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.tv/problems/supervisor-unreachable",
				Title: "the server's supervisor did not answer", Status: http.StatusBadGateway,
				Extras: map[string]any{"code": "supervisor_unreachable"}})
			return
		}
		defer resp.Body.Close()
		if r.Method != http.MethodGet && resp.StatusCode == http.StatusOK {
			var change map[string]any
			_ = json.Unmarshal(body, &change)
			h.auditAs(r.Context(), principal.FromContext(r.Context()), event, "", change)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}
