package parental

// Ask a parent (Epic 57 story 57.06) and a child's activity (story 57.08). Every
// role rule is decided here, on the server:
//
//	ask, list who can unlock, unlock with a PIN   a restricted account, for itself
//	read back what became of its own requests     a restricted account, its own only
//	list, approve, deny requests                  admin, that child's guardian
//	set a parent PIN                              admin, anyone's guardian (their own PIN)
//	read a child's activity                       admin, that child's guardian
//
// A request of kind `time` (story 57.14) asks for more minutes today; approving it
// gives the time and tells the time keeper, so a stream that was refused plays.
//
// A guardian of another child gets 404 for a request or an account that is not
// theirs, as for a missing one.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	parentalpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Problem types of these routes.
var (
	TypeNotRestricted   = httperror.Type("not-restricted")
	TypeTooManyRequests = httperror.Type("parental-too-many-requests")
	TypeRequestDecided  = httperror.Type("parental-request-decided")
	TypeRequestExpired  = httperror.Type("parental-request-expired")
	TypePINWrong        = httperror.Type("parental-pin-wrong")
	TypePINLocked       = httperror.Type("parental-pin-locked")
	TypePasswordWrong   = httperror.Type("password-wrong")
)

// The notices guardians get. Neither carries a title or the child's name.
const (
	NoticeRequest   = "parental_request"
	NoticePINLocked = "parental_pin_locked"
)

// AskHandler serves the routes of stories 57.06 and 57.08.
type AskHandler struct {
	Handler
	// Notify tells guardians of a new request and of a locked PIN (nil: no notice).
	Notify notify.Notifier
	// VerifyPassword checks the signed-in user's password again, before a PIN is
	// set. Nil refuses every PIN change.
	VerifyPassword func(ctx context.Context, userID, password string) error
	Logger         *slog.Logger
	// MinAnswer is the least time an unlock takes to answer, whatever it found
	// (0: DefaultMinAnswer; negative: none).
	MinAnswer time.Duration
	// Now is the clock of the per-address limits (nil: time.Now).
	Now func() time.Time

	once    sync.Once
	sources *limiter
	passes  *limiter
}

// DefaultMinAnswer is how long POST /api/me/parental/unlock takes at least.
const DefaultMinAnswer = 250 * time.Millisecond

// Mount attaches the routes.
func (h *AskHandler) Mount(r chi.Router) {
	r.Post("/api/me/parental/requests", h.Ask)
	r.Get("/api/me/parental/requests", h.Mine)
	r.Get("/api/me/parental/guardians", h.Unlockers)
	r.Post("/api/me/parental/unlock", h.Unlock)
	r.Get("/api/me/parental-pin", h.PIN)
	r.Put("/api/me/parental-pin", h.SetPIN)
	r.Get("/api/parental/requests", h.Requests)
	r.Post("/api/parental/requests/{id}/approve", h.Approve)
	r.Post("/api/parental/requests/{id}/deny", h.Deny)
	r.Get("/api/users/{id}/parental/activity", h.Activity)
}

func (h *AskHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *AskHandler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func notRestricted() *httperror.Error {
	return httperror.Forbidden(TypeNotRestricted, "only a restricted account can do this").With("code", "not_restricted")
}

// child lets only a restricted account through: no one gets 401, anyone else 403.
func (h *AskHandler) child(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := h.caller(w, r)
	if p == nil {
		return nil
	}
	restricted, err := h.Store.IsRestricted(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
		return nil
	}
	if !restricted {
		httperror.Write(w, r, notRestricted())
		return nil
	}
	return p
}

func (h *AskHandler) failAsk(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, parentalpkg.ErrNotRestricted):
		httperror.Write(w, r, notRestricted())
	case errors.Is(err, parentalpkg.ErrSubjectNotFound):
		httperror.Write(w, r, httperror.NotFound("subject"))
	case errors.Is(err, parentalpkg.ErrInvalid):
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "subject", Message: "not a subject that can be asked for"}}))
	case errors.Is(err, parentalpkg.ErrTooManyRequests):
		httperror.Write(w, r, (&httperror.Error{Type: TypeTooManyRequests, Title: "too many requests",
			Status: http.StatusTooManyRequests,
			Detail: "too many requests are waiting for an answer"}).With("code", "too_many_requests"))
	case errors.Is(err, parentalpkg.ErrRequestNotFound):
		httperror.Write(w, r, httperror.NotFound("request"))
	case errors.Is(err, parentalpkg.ErrRequestDecided):
		httperror.Write(w, r, httperror.Conflict(TypeRequestDecided,
			"this request was already answered").With("code", "request_decided"))
	case errors.Is(err, parentalpkg.ErrRequestExpired):
		httperror.Write(w, r, (&httperror.Error{Type: TypeRequestExpired, Title: "gone", Status: http.StatusGone,
			Detail: "this request expired, or what it asked for is no longer there"}).With("code", "request_expired"))
	default:
		h.fail(w, r, err)
	}
}

type askRequest struct {
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
}

type requestResponse struct {
	Request *parentalpkg.Request `json:"request"`
	// Rule is the allow rule an approval added.
	Rule *parentalpkg.Rule `json:"rule,omitempty"`
	// Grant is the time an approved `time` request gave.
	Grant *parentalpkg.TimeGrant `json:"grant,omitempty"`
}

// Ask implements POST /api/me/parental/requests: 201 for a new request, 200 with
// the waiting one when the same thing was already asked for.
func (h *AskHandler) Ask(w http.ResponseWriter, r *http.Request) {
	p := h.child(w, r)
	if p == nil {
		return
	}
	var req askRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !parentalpkg.ValidRequestKind(req.SubjectKind) {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "subject_kind",
			Message: "must be one of video, series, collection, library, channel, youtube_source, plugin, time"}}))
		return
	}
	if req.SubjectKind == parentalpkg.RequestTime {
		if _, ok := parentalpkg.AskedMinutes(req.SubjectID); !ok {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "subject_id",
				Message: "for time, the minutes asked for: 15, 30 or 60"}}))
			return
		}
	}
	out, created, err := h.Store.Ask(r.Context(), p.UserID, p.AccessAllLibraries, p.Libraries, req.SubjectKind, req.SubjectID)
	if err != nil {
		h.failAsk(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		h.tell(r.Context(), p.UserID, NoticeRequest, "notify.parental.request", "info", "request:"+out.ID, nil)
	}
	common.WriteJSON(w, r, status, requestResponse{Request: out})
}

type ownRequestsResponse struct {
	Requests []parentalpkg.OwnRequest `json:"requests"`
}

// Mine implements GET /api/me/parental/requests: what became of the requests the
// caller made in the last seven days, newest first, 50 at most. For a restricted
// account only, as asking is; the list is the caller's own, by id: no title, and
// not who answered.
func (h *AskHandler) Mine(w http.ResponseWriter, r *http.Request) {
	p := h.child(w, r)
	if p == nil {
		return
	}
	list, err := h.Store.OwnRequests(r.Context(), p.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, ownRequestsResponse{Requests: list})
}

// tell sends one notice to each of childID's guardians, or to the admins when it
// has none. Only skip names a guardian left out. The notice says that something
// waits, never what or for whom: it may show on a locked screen.
func (h *AskHandler) tell(ctx context.Context, childID, kind, titleKey, severity, subject string, skip map[string]bool) {
	if h.Notify == nil {
		return
	}
	guardians, err := h.Store.Guardians(ctx, childID)
	if err != nil {
		h.log().Warn("parental: guardians for a notice", "event", "parental_notice_failed", "error", err.Error())
		return
	}
	audiences := []string{}
	for _, g := range guardians {
		if !skip[g.ID] {
			audiences = append(audiences, g.ID)
		}
	}
	if len(guardians) == 0 {
		audiences = append(audiences, notify.AudienceAdmins)
	}
	for _, a := range audiences {
		if err := h.Notify.Notify(ctx, kind, notify.Payload{Severity: severity, TitleKey: titleKey,
			Link: "/parental", Audience: a, Subject: subject, Every: -1}); err != nil {
			h.log().Warn("parental: notice", "event", "parental_notice_failed", "kind", kind, "error", err.Error())
		}
	}
}

type requestsResponse struct {
	Requests []parentalpkg.RequestItem `json:"requests"`
}

// Requests implements GET /api/parental/requests?status=pending: the requests of
// every child for an admin, of their own children for a guardian.
func (h *AskHandler) Requests(w http.ResponseWriter, r *http.Request) {
	p := h.caller(w, r)
	if p == nil {
		return
	}
	if !p.IsAdmin {
		mine, err := h.Store.HasChildren(r.Context(), p.UserID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
			return
		}
		if !mine {
			h.refuse(w, r, p)
			return
		}
	}
	status := r.URL.Query().Get("status")
	if status != "" && !parentalpkg.ValidRequestStatus(status) {
		httperror.Write(w, r, httperror.InvalidQuery("status must be one of pending, approved, denied, expired"))
		return
	}
	list, err := h.Store.Requests(r.Context(), p.UserID, p.IsAdmin, status)
	if err != nil {
		h.failAsk(w, r, err)
		return
	}
	for i := range list {
		list[i].Subject = list[i].Subject.For(p.AccessAllLibraries, p.Libraries)
	}
	common.WriteJSON(w, r, http.StatusOK, requestsResponse{Requests: list})
}

// decider lets an admin or the guardian of the request's child through. A request
// that is not there, or belongs to another guardian's child, is 404 to a guardian
// and an admin, and 403 to anyone else.
func (h *AskHandler) decider(w http.ResponseWriter, r *http.Request, id string) (*principal.Principal, bool) {
	p := h.caller(w, r)
	if p == nil {
		return nil, false
	}
	childID, err := h.Store.RequestChild(r.Context(), id)
	if errors.Is(err, parentalpkg.ErrRequestNotFound) {
		if p.IsAdmin {
			httperror.Write(w, r, httperror.NotFound("request"))
		} else {
			h.refuse(w, r, p)
		}
		return nil, false
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental store").Wrap(err))
		return nil, false
	}
	return h.manager(w, r, childID)
}

type approveRequest struct {
	// ExpiresAt, when set, is when the approval ends; left out, it does not end.
	// Not for a `time` request (422).
	ExpiresAt *time.Time `json:"expires_at"`
	// Until, in place of ExpiresAt, names an end the home computes: `day_end`,
	// the end of the home's day. Not for a `time` request (422).
	Until string `json:"until"`
	// Minutes, for a `time` request only (422 on any other), is how many minutes
	// to give, 1 to 480; left out, the minutes asked for.
	Minutes *int `json:"minutes"`
}

// approveInvalid is the 422 of an approval whose body does not fit: err names the
// field (parental.ErrInvalid: <field>).
func approveInvalid(err error, until bool) *httperror.Error {
	field := strings.TrimPrefix(err.Error(), parentalpkg.ErrInvalid.Error()+": ")
	msg := "is not valid"
	switch field {
	case "expires_at":
		msg = "must be in the future, and left out for a request for more time"
		if until {
			// the end came from `until`: that is the field the caller sent
			field, msg = "until", "must be left out for a request for more time"
		}
	case "minutes":
		msg = "must be from 1 to 480, and only for a request for more time"
	default:
		field = "request"
	}
	return httperror.Unprocessable([]httperror.FieldError{{Field: field, Message: msg}})
}

// Approve implements POST /api/parental/requests/{id}/approve.
func (h *AskHandler) Approve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.decider(w, r, id)
	if !ok {
		return
	}
	var req approveRequest
	if r.ContentLength != 0 {
		if e := common.ReadJSON(r, &req, 4<<10); e != nil {
			httperror.Write(w, r, e)
			return
		}
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(h.Store.Clock()) {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "expires_at", Message: "must be in the future"}}))
		return
	}
	if req.Minutes != nil && (*req.Minutes < 1 || *req.Minutes > parentalpkg.MaxGrantMinutes) {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "minutes", Message: "must be from 1 to 480"}}))
		return
	}
	expiresAt, bad := h.ending(r.Context(), req.ExpiresAt, req.Until)
	if bad != nil {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{*bad}))
		return
	}
	out, err := h.Store.ApproveWith(r.Context(), p.UserID, id, parentalpkg.Approval{ExpiresAt: expiresAt, Minutes: req.Minutes})
	if err != nil {
		if errors.Is(err, parentalpkg.ErrInvalid) {
			httperror.Write(w, r, approveInvalid(err, req.Until != ""))
			return
		}
		h.failAsk(w, r, err)
		return
	}
	h.changed(out.Request.ChildID)
	if out.Grant != nil {
		// the grant is committed: a stream running (or refused) now learns of it at once
		h.refreshed(r, out.Request.ChildID)
	}
	common.WriteJSON(w, r, http.StatusOK, requestResponse{Request: out.Request, Rule: out.Rule, Grant: out.Grant})
}

// Deny implements POST /api/parental/requests/{id}/deny.
func (h *AskHandler) Deny(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.decider(w, r, id)
	if !ok {
		return
	}
	out, err := h.Store.Deny(r.Context(), p.UserID, id)
	if err != nil {
		h.failAsk(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, requestResponse{Request: out.Request})
}

func tooMany(typ, code, detail string, retry time.Duration) *httperror.Error {
	secs := int(retry / time.Second)
	if secs < 1 {
		secs = 1
	}
	return (&httperror.Error{Type: typ, Title: "too many requests", Status: http.StatusTooManyRequests,
		Detail: detail}).With("code", code).With("retry_after", secs)
}

func writeTooMany(w http.ResponseWriter, r *http.Request, e *httperror.Error) {
	if secs, ok := e.Extras["retry_after"].(int); ok {
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	httperror.Write(w, r, e)
}
