package systemstatus

import (
	"context"
	"fmt"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
)

// checkUpdateCheck is the "Update check" item: whether this home can read GitHub's list of
// releases (the update check, every 6 hours). From updates.FailingAfter failures in a row
// it is degraded, "Can't check for updates since …" with what the check failed on: on
// 2026-11-23 the list grew past what a home reads, and every home's check failed for over a
// day with nothing but a line in its log. One failure alone is fine but marked (the next
// check is 6 hours away). No item on a home that has never checked (updates off).
func checkUpdateCheck(ctx context.Context, e *env) []Item {
	st, ok := e.h.updateState(ctx)
	if !ok || st.LastCheckAt == nil {
		return nil
	}
	it := base("update_check", GroupIntegrations, KindUpdateCheck, "Update check")
	it.Link = "/admin/updates"
	it.LastChecked = *st.LastCheckAt
	it.Details = map[string]any{"last_check_at": st.LastCheckAt, "last_check_ok": st.LastCheckOK,
		"failures": st.Failures, "failing_since": st.FailingSince, "code": st.LastCheckCode, "error": st.LastCheckErr}
	detail := errText(errString(st.LastCheckErr))
	switch {
	case st.Failures == 0:
		it.set(StatusOK, "update_check_ok", nil, "Updates are checked every 6 hours.")
	case st.Failures < updates.FailingAfter || st.FailingSince == nil:
		it.Attention = true
		it.set(StatusOK, "update_check_failed_once", map[string]any{"code": st.LastCheckCode, "detail": detail},
			"The last update check failed: "+detail+". It is tried again in 6 hours.")
	default:
		since := st.FailingSince.UTC()
		it.set(StatusDegraded, "update_check_failing", map[string]any{"since": since.Format(time.RFC3339),
			"failing_seconds": secs(e.now.Sub(since)), "failures": st.Failures, "code": st.LastCheckCode,
			"detail": detail},
			fmt.Sprintf("Can't check for updates since %s (%d checks in a row failed): %s",
				since.Format("2006-01-02 15:04 MST"), st.Failures, detail))
	}
	return one(it)
}

type errString string

func (e errString) Error() string { return string(e) }

// updateState reads the update check's state (UpdateState, else app_settings through the
// updates store); false when there is nothing to read it from.
func (h *Handler) updateState(ctx context.Context) (updates.State, bool) {
	if h.UpdateState != nil {
		return h.UpdateState(ctx)
	}
	if h.DB == nil {
		return updates.State{}, false
	}
	return (&updates.Store{DB: h.DB}).LoadState(ctx), true
}
