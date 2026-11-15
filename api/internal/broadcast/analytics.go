package broadcast

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// AnalyticsCloud is what the home asks Maktaba Cloud about its viewers (story 50.28): the relay
// agent in production, a fake in tests. The home never holds the owner's cloud sign-in; the agent
// asks with the home's own credential, and the cloud answers for that home only.
type AnalyticsCloud interface {
	Analytics(ctx context.Context, period, tz string) (json.RawMessage, error)
}

// analyticsView is GET /api/tv/broadcast/analytics: the cloud's report as it came (folded there:
// a country or device under 5 sessions in an hour is "other" for that hour, and the home never
// sees more), and whether the cloud answered.
type analyticsView struct {
	Cloud  string          `json:"cloud"` // ok, unreachable, unclaimed
	Report json.RawMessage `json:"report,omitempty"`
}

var analyticsPeriods = map[string]bool{"today": true, "7d": true, "month": true}

func (h *Routes) analytics(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	q := r.URL.Query()
	period := q.Get("period")
	if period == "" {
		period = "today"
	}
	if !analyticsPeriods[period] {
		httperror.Write(w, r, httperror.BadRequest("period is today, 7d or month").With("code", "broadcast.badPeriod"))
		return
	}
	tz := q.Get("tz")
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil || len(tz) > 64 {
			httperror.Write(w, r, httperror.BadRequest("tz is an IANA time zone").With("code", "broadcast.badTimeZone"))
			return
		}
	}
	a := h.Service.Analytics
	if a == nil {
		writeJSON(w, http.StatusOK, analyticsView{Cloud: "unclaimed"})
		return
	}
	raw, err := a.Analytics(r.Context(), period, tz)
	if err != nil || !json.Valid(raw) {
		h.Service.log().Warn("broadcast analytics: the cloud did not answer", "err", err)
		writeJSON(w, http.StatusOK, analyticsView{Cloud: "unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, analyticsView{Cloud: "ok", Report: raw})
}
