package parental

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	parentalpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Epic 57 story 57.14: a restricted account's time rules, its count and more time
// for today. Every role rule is decided here, on the server:
//
//	GET  /api/parental/users/{id}/time-rules   admin, that child's guardian
//	PUT  /api/parental/users/{id}/time-rules   admin, that child's guardian
//	GET  /api/parental/users/{id}/time-usage   admin, that child's guardian
//	POST /api/parental/users/{id}/time-grants  admin, that child's guardian
//	GET  /api/me/parental/time                 anyone signed in, for themselves
//
// A viewer and a restricted account (the child included) get 403 on the first
// four; a guardian of another child gets 404, as on every other parental route, so
// a child's existence is not shown; a sign-in through Maktaba Cloud that may only
// play reads and does not write. An account that is not restricted is 404.

// TypeParentalTime is the 403 a stream gets when the account's time is over. It
// carries `reason` (outside_slot, limit_reached, no_heartbeat) and, when the rules
// allow playback again, `next_allowed_at`.
const TypeParentalTime = "https://maktaba.tv/problems/parental-time"

// MountTime attaches the story's routes. h.Time must be set.
func (h *Handler) MountTime(r chi.Router) {
	r.Get("/api/parental/users/{id}/time-rules", h.TimeRules)
	r.Put("/api/parental/users/{id}/time-rules", h.SetTimeRules)
	r.Get("/api/parental/users/{id}/time-usage", h.TimeUsage)
	r.Post("/api/parental/users/{id}/time-grants", h.GrantTime)
	r.Get("/api/me/parental/time", h.MyTime)
}

// timeAccess lets an admin or the child's guardian through. Anyone else is refused
// as on a write (refuse): 403, or 404 for a guardian of another child. write also
// refuses a play-only Maktaba Cloud sign-in.
func (h *Handler) timeAccess(w http.ResponseWriter, r *http.Request, id string, write bool) (*principal.Principal, bool) {
	if write {
		return h.manager(w, r, id)
	}
	p := h.caller(w, r)
	if p == nil {
		return nil, false
	}
	access, err := h.Store.AccessTo(r.Context(), p, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
		return nil, false
	}
	if access == parentalpkg.AccessNone {
		h.refuse(w, r, p)
		return nil, false
	}
	return p, true
}

func (h *Handler) timeFail(w http.ResponseWriter, r *http.Request, field string, err error) {
	if errors.Is(err, parentalpkg.ErrInvalid) {
		msg := strings.TrimPrefix(err.Error(), parentalpkg.ErrInvalid.Error()+": ")
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: field, Message: msg}}))
		return
	}
	h.fail(w, r, err)
}

type timeRulesBody struct {
	Rules []parentalpkg.TimeRule `json:"rules"`
}

type timeRulesResponse struct {
	Rules    []parentalpkg.TimeRule `json:"rules"`
	TimeZone string                 `json:"time_zone"`
}

func (h *Handler) writeRules(w http.ResponseWriter, r *http.Request, rules []parentalpkg.TimeRule) {
	if rules == nil {
		rules = []parentalpkg.TimeRule{}
	}
	loc, err := h.Store.HomeZone(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, timeRulesResponse{Rules: rules, TimeZone: loc.String()})
}

// TimeRules implements GET /api/parental/users/{id}/time-rules.
func (h *Handler) TimeRules(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.timeAccess(w, r, id, false); !ok {
		return
	}
	rules, err := h.Store.TimeRules(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeRules(w, r, rules)
}

// refreshed tells streaming of id's new time at once, so a stream running now ends
// (or goes on) by the new rules without waiting for its next heartbeat. When
// streaming cannot be told the change still holds: a running stream is served only
// under a lease, and the next call that would renew it reads the new rules.
func (h *Handler) refreshed(r *http.Request, id string) {
	if h.Time == nil {
		return
	}
	_ = h.Time.Refresh(r.Context(), id)
}

// SetTimeRules implements PUT /api/parental/users/{id}/time-rules: the body's
// rules replace every rule the account has (none: no time limit).
func (h *Handler) SetTimeRules(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.timeAccess(w, r, id, true)
	if !ok {
		return
	}
	var body timeRulesBody
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if body.Rules == nil {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "rules", Message: "required (an empty list removes every rule)"}}))
		return
	}
	rules, err := h.Store.SetTimeRules(r.Context(), p.UserID, id, body.Rules)
	if err != nil {
		h.timeFail(w, r, "rules", err)
		return
	}
	h.refreshed(r, id)
	h.writeRules(w, r, rules)
}

// TimeUsage implements GET /api/parental/users/{id}/time-usage?from&to (home days,
// YYYY-MM-DD, both included; neither: the last seven days).
func (h *Handler) TimeUsage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.timeAccess(w, r, id, false); !ok {
		return
	}
	q := r.URL.Query()
	usage, err := h.Store.TimeUsage(r.Context(), id, q.Get("from"), q.Get("to"))
	if err != nil {
		h.timeFail(w, r, "from", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, usage)
}

type timeGrantBody struct {
	Minutes int `json:"minutes"`
}

// GrantTime implements POST /api/parental/users/{id}/time-grants: minutes more for
// the current home day only.
func (h *Handler) GrantTime(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.timeAccess(w, r, id, true)
	if !ok {
		return
	}
	var body timeGrantBody
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	grant, err := h.Store.GrantTime(r.Context(), p.UserID, id, body.Minutes)
	if err != nil {
		h.timeFail(w, r, "minutes", err)
		return
	}
	h.refreshed(r, id)
	common.WriteJSON(w, r, http.StatusCreated, grant)
}

// myTimeResponse is GET /api/me/parental/time. RemainingSeconds is null when no
// rule limits the account now.
type myTimeResponse struct {
	Allowed          bool       `json:"allowed"`
	Limited          bool       `json:"limited"`
	Reason           string     `json:"reason,omitempty"`
	RemainingSeconds *int       `json:"remaining_seconds"`
	SlotEndsAt       *time.Time `json:"slot_ends_at"`
	NextAllowedAt    *time.Time `json:"next_allowed_at"`
	Now              time.Time  `json:"now"`
}

// MyTime implements GET /api/me/parental/time[?library_id=]: the caller's own time
// now, for the player's countdown. It only reads: it counts nothing and renews no
// stream's lease, so asking it does not keep a stream alive.
func (h *Handler) MyTime(w http.ResponseWriter, r *http.Request) {
	p := h.caller(w, r)
	if p == nil {
		return
	}
	if h.Time == nil {
		httperror.Write(w, r, httperror.Unavailable(30))
		return
	}
	lib := r.URL.Query().Get("library_id")
	if lib != "" && !isUUID(lib) {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "library_id", Message: "not a valid id"}}))
		return
	}
	st, err := h.Time.Time(r.Context(), p, lib)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental time").Wrap(err))
		return
	}
	out := myTimeResponse{Allowed: st.Allowed, Limited: st.Limited, Reason: st.Reason,
		SlotEndsAt: utc(st.SlotEndsAt), NextAllowedAt: utc(st.NextAllowedAt), Now: st.Now.UTC()}
	if st.Now.IsZero() {
		out.Now = time.Now().UTC()
	}
	if st.Limited || !st.Allowed {
		secs := int(st.Remaining / time.Second)
		out.RemainingSeconds = &secs
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}
