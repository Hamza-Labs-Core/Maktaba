package parental

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	parentalpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Activity implements GET /api/users/{id}/parental/activity?cursor=&kind=: a
// child's events, newest first, 50 a page, for an admin or that child's guardian;
// anyone else gets 404. A subject in a library the reader cannot open comes back
// with its kind and id only.
func (h *AskHandler) Activity(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.reader(w, r, id); !ok {
		return
	}
	p := h.caller(w, r)
	if p == nil {
		return
	}
	q := r.URL.Query()
	page, err := h.Store.Activity(r.Context(), id, q.Get("kind"), q.Get("cursor"))
	switch {
	case errors.Is(err, parentalpkg.ErrBadCursor):
		httperror.Write(w, r, httperror.InvalidQuery("cursor is not one this server gave"))
		return
	case errors.Is(err, parentalpkg.ErrInvalid):
		httperror.Write(w, r, httperror.InvalidQuery(
			"kind must be one of watched, blocked, asked, approved, denied, pin_locked, signal, time_up, time_granted"))
		return
	case err != nil:
		h.fail(w, r, err)
		return
	}
	for i := range page.Events {
		if s := page.Events[i].Subject; s != nil {
			seen := s.For(p.AccessAllLibraries, p.Libraries)
			page.Events[i].Subject = &seen
		}
	}
	common.WriteJSON(w, r, http.StatusOK, page)
}
