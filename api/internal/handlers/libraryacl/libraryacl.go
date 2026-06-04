// Package libraryacl implements the admin Library-ACL matrix surface
// (web-pages-batch2):
//
//	GET /api/admin/library-acl  → { users, libraries, grants }
//	PUT /api/admin/library-acl  → bulk upsert { grants:[{user_id,library_id,role}] }
//
// The GET returns the full universe (every user + every library) plus
// the sparse grant rows so the SPA can render a rows×cols matrix with a
// per-cell role dropdown. The PUT applies a batch: role "none" revokes
// the (user, library) row, any of read/write/admin upserts it via the
// authz ACL store internals (slot-0072 `role`).
//
// Admin-only, enforced in-handler (the global RequireAuthExcept gate
// already 401s anonymous callers; this re-checks IsAdmin defensively).
package libraryacl

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// validRoles is the closed vocabulary accepted on PUT. "none" is the
// revoke sentinel (delete the row); the rest map to the slot-0072 CHECK.
var validRoles = map[string]struct{}{
	"none":  {},
	"read":  {},
	"write": {},
	"admin": {},
}

// Handler owns the surface. DB powers the user/library universe queries;
// ACL is the authz store whose internals do the grant upsert/revoke.
type Handler struct {
	DB  *sql.DB
	ACL *authz.ACLStore
}

// New builds a Handler from a DB.
func New(db *sql.DB) *Handler {
	return &Handler{DB: db, ACL: &authz.ACLStore{DB: db}}
}

// Mount attaches the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/admin/library-acl", h.List)
	r.Put("/api/admin/library-acl", h.BulkUpsert)
}

type userRef struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type libraryRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type listResponse struct {
	Users     []userRef     `json:"users"`
	Libraries []libraryRef  `json:"libraries"`
	Grants    []authz.Grant `json:"grants"`
}

// List returns the full matrix universe + the sparse grants.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	users, err := h.listUsers(r)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list users"))
		return
	}
	libs, err := h.listLibraries(r)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list libraries"))
		return
	}
	grants, err := h.ACL.AllGrants(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list grants"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, listResponse{
		Users:     users,
		Libraries: libs,
		Grants:    grants,
	})
}

type bulkRequest struct {
	Grants []authz.Grant `json:"grants"`
}

// BulkUpsert applies a batch of (user, library, role) changes. Each
// entry is validated; "none" revokes, the rest upsert.
func (h *Handler) BulkUpsert(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req bulkRequest
	if e := common.ReadJSON(r, &req, 256<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	for _, g := range req.Grants {
		if g.UserID == "" || g.LibraryID == "" {
			httperror.Write(w, r, httperror.BadRequest("user_id and library_id are required"))
			return
		}
		if _, ok := validRoles[g.Role]; !ok {
			httperror.Write(w, r, httperror.BadRequest("role must be one of none, read, write, admin"))
			return
		}
	}
	for _, g := range req.Grants {
		var err error
		if g.Role == "none" {
			err = h.ACL.Revoke(r.Context(), g.UserID, g.LibraryID)
		} else {
			err = h.ACL.SetRole(r.Context(), g.UserID, g.LibraryID, g.Role)
		}
		if err != nil {
			httperror.Write(w, r, httperror.Internal("apply grant"))
			return
		}
	}
	// Echo the fresh state so the SPA can reconcile without a refetch.
	grants, err := h.ACL.AllGrants(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("reload grants"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"grants": grants})
}

func (h *Handler) listUsers(r *http.Request) ([]userRef, error) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT id, username FROM users WHERE pw_hash <> '<unsalted-disabled>' ORDER BY lower(username)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []userRef{}
	for rows.Next() {
		var u userRef
		if err := rows.Scan(&u.ID, &u.Username); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (h *Handler) listLibraries(r *http.Request) ([]libraryRef, error) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT id, name FROM libraries ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []libraryRef{}
	for rows.Next() {
		var l libraryRef
		if err := rows.Scan(&l.ID, &l.Name); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// requireAdmin writes the 401/403 and returns false when the caller is
// not an admin.
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/unauthorized",
			Title:  "unauthorized",
			Status: http.StatusUnauthorized,
		})
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin only"))
		return false
	}
	return true
}
