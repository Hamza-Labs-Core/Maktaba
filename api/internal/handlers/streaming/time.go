package streaming

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Epic 57 story 57.14: a restricted account's playback time, at the four places a
// stream session passes through this handler. Each place is one call to a function
// of this file; nothing else here knows the rules. With no keeper (Handler.Time
// nil: a test, or a build with no parental controls) every call lets through.
//
//	OpenSession   timeAdmit   before the session is opened or renewed
//	OpenSession   timeOpened  once it has its id: the session gets its lease
//	PostProgress  timeBeat    the stream's heartbeat: counted, lease renewed; a
//	                          post refused for time still stores its position
//	CloseSession  timeClosed  counted up to the close, lease ended
//
// The stream URLs are signed with the session's id and say whether the account's
// time is kept (signedurl.Minter.KeepTime), so streaming serves a kept account
// only under the lease these calls keep alive.

// ProblemParentalTime is the 403 of a stream whose account's time is over.
var ProblemParentalTime = httperror.Type("parental-time")

// TimeGate is the time keeper (parental.TimeKeeper in production).
type TimeGate interface {
	Admit(ctx context.Context, p *principal.Principal, libraryID string) (parental.TimeState, error)
	Opened(ctx context.Context, userID, libraryID, videoID, sessionID string) error
	Beat(ctx context.Context, userID, sessionID string) (parental.TimeState, error)
	Closed(ctx context.Context, sessionID string) error
}

// writeTimeOver answers 403 parental-time with the reason and, when the rules
// allow playback again, when.
func writeTimeOver(w http.ResponseWriter, r *http.Request, reason string, next *time.Time) {
	e := httperror.Forbidden(ProblemParentalTime, "this account's watching time is over for now").
		With("code", "parental_time").With("reason", reason)
	if next != nil {
		e = e.With("next_allowed_at", next.UTC().Format(time.RFC3339))
	}
	httperror.Write(w, r, e)
}

// CodeTimeUnknown is the code of the 503 a stream, a progress post or a watch gets
// when its account's time could not be read or counted. It tells that 503 from any
// other (a busy transcoder, a part that is off), so an app says "can't check your
// watching time" only for this one.
const CodeTimeUnknown = "parental_time_unknown"

// writeTimeUnknown refuses a stream whose account's time could not be read or
// counted: never free time on an error. 503 with Retry-After and CodeTimeUnknown.
func writeTimeUnknown(w http.ResponseWriter, r *http.Request, what string, err error) {
	w.Header().Set("Retry-After", "5")
	mlog.From(r.Context()).Warn("stream refused: the account's playback time is not known",
		"at", what, "err", err.Error(), "event", "parental_time_unknown")
	httperror.Write(w, r, httperror.Unavailable(5).With("code", CodeTimeUnknown).Wrap(err))
}

// timeAdmit reports whether p may start or renew a stream in libraryID now; when
// not, the answer is written.
func (h *Handler) timeAdmit(w http.ResponseWriter, r *http.Request, p *principal.Principal, libraryID string) bool {
	if h.Time == nil {
		return true
	}
	st, err := h.Time.Admit(r.Context(), p, libraryID)
	if err != nil {
		writeTimeUnknown(w, r, "admit", err)
		return false
	}
	if !st.Allowed {
		writeTimeOver(w, r, st.Reason, st.NextAllowedAt)
		return false
	}
	return true
}

// timeOpened gives the session just opened or renewed its lease; when that fails
// the answer is written and the session's URLs are not handed out.
func (h *Handler) timeOpened(w http.ResponseWriter, r *http.Request, userID, libraryID, videoID, sessionID string) bool {
	if h.Time == nil || sessionID == "" {
		return true
	}
	if err := h.Time.Opened(r.Context(), userID, libraryID, videoID, sessionID); err != nil {
		writeTimeUnknown(w, r, "open", err)
		return false
	}
	return true
}

// timeBeat counts one progress post of ownerID's session and renews its lease;
// when the time is over, or the session cannot be counted, the answer is written
// and the lease runs out.
//
// atTimeStop (nil: nothing) runs before the 403 of a post refused because the
// account's time is over (limit_reached, outside_slot): PostProgress stores the
// position the post carries there. It does not run for a session the keeper does
// not know as the account's (no_heartbeat), nor when the time cannot be counted.
func (h *Handler) timeBeat(w http.ResponseWriter, r *http.Request, ownerID, sessionID string, atTimeStop func()) bool {
	if h.Time == nil {
		return true
	}
	st, err := h.Time.Beat(r.Context(), ownerID, sessionID)
	switch {
	case errors.Is(err, parental.ErrUnknownSession):
		writeTimeOver(w, r, parental.ReasonNoHeartbeat, nil)
		return false
	case err != nil:
		writeTimeUnknown(w, r, "progress", err)
		return false
	case !st.Allowed:
		if atTimeStop != nil {
			atTimeStop()
		}
		writeTimeOver(w, r, st.Reason, st.NextAllowedAt)
		return false
	}
	return true
}

// timeClosed counts the session up to its close. A failure is logged: the mark
// stays, and what it owes is counted with the account's next call.
func (h *Handler) timeClosed(ctx context.Context, sessionID string) {
	if h.Time == nil {
		return
	}
	if err := h.Time.Closed(ctx, sessionID); err != nil {
		mlog.From(ctx).Warn("stream session's time not counted at close",
			"session_id", sessionID, "err", err.Error(), "event", "parental_time_close_failed")
	}
}
