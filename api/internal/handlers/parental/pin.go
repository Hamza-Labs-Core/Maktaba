package parental

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	parentalpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// The limits kept in memory, beside the stored ones (per guardian, per child).
const (
	// SourceMaxWrong wrong PINs from one address inside SourceWindow close PIN
	// entry from that address for SourceHold.
	SourceMaxWrong = 20
	SourceWindow   = time.Hour
	SourceHold     = time.Hour
	// PasswordMaxWrong wrong passwords from one user at PUT /api/me/parental-pin
	// inside an hour close that route to them for an hour.
	PasswordMaxWrong = 10
)

func (h *AskHandler) limits() (sources, passes *limiter) {
	h.once.Do(func() {
		h.sources = newLimiter(SourceMaxWrong, SourceWindow, SourceHold)
		h.passes = newLimiter(PasswordMaxWrong, time.Hour, time.Hour)
	})
	return h.sources, h.passes
}

// sourceOf is the address a request came from, as the router resolved it.
func sourceOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type unlockersResponse struct {
	Guardians []parentalpkg.ChildRef `json:"guardians"`
}

// Unlockers implements GET /api/me/parental/guardians: who can open something for
// the caller with a PIN (their guardians and the admins, those with a PIN set).
func (h *AskHandler) Unlockers(w http.ResponseWriter, r *http.Request) {
	p := h.child(w, r)
	if p == nil {
		return
	}
	list, err := h.Store.PINGuardians(r.Context(), p.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, unlockersResponse{Guardians: list})
}

// pinOwner lets through who may have a parent PIN: an admin, or a guardian of
// someone. A restricted account gets 403 `restricted`, anyone else 403.
func (h *AskHandler) pinOwner(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := h.caller(w, r)
	if p == nil {
		return nil
	}
	if p.IsAdmin {
		return p
	}
	mine, err := h.Store.HasChildren(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
		return nil
	}
	if mine {
		return p
	}
	restricted, err := h.Store.IsRestricted(r.Context(), p.UserID)
	if err == nil && restricted {
		httperror.Write(w, r, Restricted())
		return nil
	}
	httperror.Write(w, r, httperror.Forbidden("", "only an admin or a guardian has a parent PIN"))
	return nil
}

type pinResponse struct {
	// Set reports whether the caller has a parent PIN.
	Set bool `json:"set"`
}

// PIN implements GET /api/me/parental-pin: whether the caller has set a PIN. The
// PIN itself is never sent.
func (h *AskHandler) PIN(w http.ResponseWriter, r *http.Request) {
	p := h.pinOwner(w, r)
	if p == nil {
		return
	}
	set, err := h.Store.HasPIN(r.Context(), p.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, pinResponse{Set: set})
}

type setPINRequest struct {
	PIN      string `json:"pin"`
	Password string `json:"password"`
}

// SetPIN implements PUT /api/me/parental-pin: the caller's own PIN, six digits,
// with their password typed again.
func (h *AskHandler) SetPIN(w http.ResponseWriter, r *http.Request) {
	p := h.pinOwner(w, r)
	if p == nil {
		return
	}
	if p.CloudScope == principal.CloudScopePlay {
		httperror.Write(w, r, httperror.Forbidden(TypeCloudSession,
			"this sign-in through Maktaba Cloud can only play; sign in on the home to set a PIN"))
		return
	}
	var req setPINRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !parentalpkg.ValidPIN(req.PIN) {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "pin", Message: "must be six digits"}}))
		return
	}
	_, passes := h.limits()
	now := h.now()
	if wait := passes.closedFor(p.UserID, now); wait > 0 {
		writeTooMany(w, r, tooMany(TypePasswordWrong, "too_many_tries", "too many wrong passwords; try again later", wait))
		return
	}
	if req.Password == "" || h.VerifyPassword == nil || h.VerifyPassword(r.Context(), p.UserID, req.Password) != nil {
		passes.wrong(p.UserID, now)
		httperror.Write(w, r, httperror.Forbidden(TypePasswordWrong, "the password is not right").With("code", "password_wrong"))
		return
	}
	passes.clear(p.UserID)
	if err := h.Store.SetPIN(r.Context(), p.UserID, req.PIN); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type unlockRequest struct {
	GuardianID  string `json:"guardian_id"`
	PIN         string `json:"pin"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
}

type unlockResponse struct {
	// Rule is the allow rule added; its expires_at is when the day ends. Absent
	// for kind `time`.
	Rule *parentalpkg.Rule `json:"rule,omitempty"`
	// Grant is the time given, for kind `time`.
	Grant *parentalpkg.TimeGrant `json:"grant,omitempty"`
}

// settle waits until an unlock has taken MinAnswer since start, so a right PIN, a
// wrong one and a refusal take the same time to answer.
func (h *AskHandler) settle(ctx context.Context, start time.Time) {
	min := h.MinAnswer
	if min == 0 {
		min = DefaultMinAnswer
	}
	left := min - time.Since(start)
	if left <= 0 {
		return
	}
	t := time.NewTimer(left)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func pinLocked(retry time.Duration) *httperror.Error {
	return tooMany(TypePINLocked, "pin_locked", "too many wrong tries; PIN entry is closed for now", retry)
}

// Unlock implements POST /api/me/parental/unlock: a guardian types their PIN on
// the child's device and one thing is allowed until the day ends. A wrong PIN, a
// user who is not this child's guardian and a guardian with no PIN all answer
// 403 `pin_wrong`. The PIN is never logged.
func (h *AskHandler) Unlock(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	p := h.child(w, r)
	if p == nil {
		return
	}
	var req unlockRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	var errs []httperror.FieldError
	if !parentalpkg.ValidPIN(req.PIN) {
		errs = append(errs, httperror.FieldError{Field: "pin", Message: "must be six digits"})
	}
	if req.GuardianID == "" {
		errs = append(errs, httperror.FieldError{Field: "guardian_id", Message: "is required"})
	}
	if !parentalpkg.ValidRequestKind(req.SubjectKind) {
		errs = append(errs, httperror.FieldError{Field: "subject_kind",
			Message: "must be one of video, series, collection, library, channel, youtube_source, plugin, time"})
	}
	if req.SubjectID == "" {
		errs = append(errs, httperror.FieldError{Field: "subject_id", Message: "is required"})
	}
	if len(errs) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(errs))
		return
	}
	ctx := r.Context()
	defer h.settle(ctx, start)

	sources, _ := h.limits()
	source, now := sourceOf(r), h.now()
	if wait := sources.closedFor(source, now); wait > 0 {
		writeTooMany(w, r, pinLocked(wait))
		return
	}
	out, err := h.Store.Unlock(ctx, p.UserID, p.AccessAllLibraries, p.Libraries, req.GuardianID, req.PIN, req.SubjectKind, req.SubjectID)
	if err != nil {
		if errors.Is(err, parentalpkg.ErrInvalid) {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "subject_id", Message: "is not valid"}}))
			return
		}
		h.failAsk(w, r, err)
		return
	}
	switch out.Outcome {
	case parentalpkg.UnlockOpened:
		h.changed(p.UserID)
		if out.Grant != nil {
			// the grant is committed: a stream running (or refused) now learns of it at once
			h.refreshed(r, p.UserID)
		}
		common.WriteJSON(w, r, http.StatusOK, unlockResponse{Rule: out.Rule, Grant: out.Grant})
		return
	case parentalpkg.UnlockLocked:
		if out.LockedGuardian != "" || out.LockedAccount {
			sources.wrong(source, now)
			h.tell(ctx, p.UserID, NoticePINLocked, "notify.parental.pin_locked", "warn", "pin:"+p.UserID, nil)
		}
		writeTooMany(w, r, pinLocked(out.RetryAfter))
		return
	}
	if sources.wrong(source, now) {
		if err := h.Store.RecordSourceLocked(ctx, p.UserID); err != nil {
			h.log().Warn("parental: pin_locked event", "event", "parental_event_failed", "error", err.Error())
		}
		h.tell(ctx, p.UserID, NoticePINLocked, "notify.parental.pin_locked", "warn", "pin:"+p.UserID, nil)
		writeTooMany(w, r, pinLocked(SourceHold))
		return
	}
	httperror.Write(w, r, httperror.Forbidden(TypePINWrong, "the PIN is not right").With("code", "pin_wrong"))
}
