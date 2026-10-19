package notify

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Handler serves the notification centre (story 33.13 AC2):
//
//	GET  /api/notifications?limit=&unread=1   {items, unread}
//	POST /api/notifications/{id}/read         {unread}
//	POST /api/notifications/read-all          {unread}
//
// A user sees the notices addressed to them and, when an admin, the admins' notices;
// read is per user.
type Handler struct {
	Store *Store
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/notifications", h.List)
	r.Post("/api/notifications/read-all", h.ReadAll)
	r.Post("/api/notifications/{id}/read", h.Read)
}

func who(w http.ResponseWriter, r *http.Request) (*principal.Principal, bool) {
	p := principal.FromContext(r.Context())
	if p == nil || p.UserID == "" {
		httperror.Write(w, r, httperror.Forbidden("", "sign in"))
		return nil, false
	}
	return p, true
}

// List serves GET /api/notifications.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p, ok := who(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	unreadOnly := r.URL.Query().Get("unread") == "1" || r.URL.Query().Get("unread") == "true"
	items, err := h.Store.List(r.Context(), p.UserID, p.IsAdmin, limit, unreadOnly)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list notifications: "+err.Error()))
		return
	}
	n, err := h.Store.Unread(r.Context(), p.UserID, p.IsAdmin)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("count notifications: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "unread": n})
}

// Read serves POST /api/notifications/{id}/read.
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	p, ok := who(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.Store.MarkRead(r.Context(), p.UserID, p.IsAdmin, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			httperror.Write(w, r, httperror.NotFound("notification "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("mark read: "+err.Error()))
		return
	}
	h.unread(w, r, p)
}

// ReadAll serves POST /api/notifications/read-all.
func (h *Handler) ReadAll(w http.ResponseWriter, r *http.Request) {
	p, ok := who(w, r)
	if !ok {
		return
	}
	if _, err := h.Store.MarkAllRead(r.Context(), p.UserID, p.IsAdmin); err != nil {
		httperror.Write(w, r, httperror.Internal("mark all read: "+err.Error()))
		return
	}
	h.unread(w, r, p)
}

func (h *Handler) unread(w http.ResponseWriter, r *http.Request, p *principal.Principal) {
	n, err := h.Store.Unread(r.Context(), p.UserID, p.IsAdmin)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("count notifications: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"unread": n})
}
