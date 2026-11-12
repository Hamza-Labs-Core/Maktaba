package watch

import (
	"context"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Epic 57 story 57.14: a watch does not start or go on while its account's time
// is over. This only refuses: the time is counted, and the stream ended, by the
// stream session's own calls (handlers/streaming/time.go), so a watch heartbeat
// adds nothing to the count and keeps no stream alive.

// ProblemParentalTime is the 403 of an account whose watching time is over.
const ProblemParentalTime = "https://maktaba.tv/problems/parental-time"

// TimeReader reads an account's time now (parental.TimeKeeper in production).
type TimeReader interface {
	Time(ctx context.Context, p *principal.Principal, libraryID string) (parental.TimeState, error)
}

// timeOK reports whether p may watch in libraryID now; when not, the answer is
// written. A time that cannot be read refuses.
func (h *Handler) timeOK(w http.ResponseWriter, r *http.Request, p *principal.Principal, libraryID string) bool {
	if h.Time == nil {
		return true
	}
	st, err := h.Time.Time(r.Context(), p, libraryID)
	if err != nil {
		httperror.Write(w, r, httperror.Unavailable(5).Wrap(err))
		return false
	}
	if st.Allowed {
		return true
	}
	e := httperror.Forbidden(ProblemParentalTime, "this account's watching time is over for now").
		With("code", "parental_time").With("reason", st.Reason)
	if st.NextAllowedAt != nil {
		e = e.With("next_allowed_at", st.NextAllowedAt.UTC().Format(time.RFC3339))
	}
	httperror.Write(w, r, e)
	return false
}
