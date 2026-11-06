package logs

import (
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// GET /api/admin/logs/users (Epic 53 story 53.11): each user's user_h under this home's
// log key, with their name, so the Logs page shows a name where a line has only the hash.
// The lines never carry the id; only an admin, who can list the users anyway, gets this.
func (h *Handler) users(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	if h.DB != nil {
		rows, err := h.DB.QueryContext(r.Context(), `SELECT id, username FROM users`)
		if err != nil {
			httperror.Write(w, r, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id, name string
			if rows.Scan(&id, &name) == nil {
				if hh := mlog.HashUser(id); hh != "" {
					out[hh] = name
				}
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"users": out})
}
