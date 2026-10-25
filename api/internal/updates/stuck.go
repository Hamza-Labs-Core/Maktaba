package updates

import (
	"context"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// NoticeStuck: a release the home's automatic runs could not install for stuckWindows
// windows in a row for the same reason (the TV watched through every window): an admin
// chooses to install it now (Update now) or lets it wait for a quiet window.
const NoticeStuck = "update.stuck"

// stuckWindows is how many windows in a row a gate held a release before it is stuck.
const stuckWindows = 3

// stuckCodes are the failures of a gate, not of the release or the machine: the window
// ended while the TV was watched, or before the home could restart.
var stuckCodes = map[string]bool{"tv_busy": true, "outside_window": true}

// Stuck is a release held by a gate for several windows in a row.
type Stuck struct {
	Version string    `json:"version"`
	Code    string    `json:"code"`
	Windows int       `json:"windows"`
	Since   time.Time `json:"since"`
}

// stuck is version's run of gate failures (one a window, the newest first), when it is
// stuckWindows long or more (nil otherwise): any other outcome in between ends it.
func (s *Service) stuck(ctx context.Context, version string) *Stuck {
	failed, err := s.Store.FailedAutoRuns(ctx, version, time.Time{})
	if err != nil || len(failed) == 0 {
		return nil
	}
	if last, err := s.Store.LastFinishedOf(ctx, version); err != nil || last == nil || last.ID != failed[0].ID {
		return nil // a later run of it ended otherwise (cancelled, rolled back, an admin's)
	}
	code, _ := runErrDoc(failed[0])["code"].(string)
	if !stuckCodes[code] {
		return nil
	}
	days := map[string]bool{}
	since := failed[0].StartedAt
	for _, r := range failed {
		c, _ := runErrDoc(r)["code"].(string)
		if c != code {
			break
		}
		days[r.StartedAt.In(s.loc()).Format("2006-01-02")] = true
		since = r.StartedAt
	}
	if len(days) < stuckWindows {
		return nil
	}
	return &Stuck{Version: NormalizeVersion(version), Code: code, Windows: len(days), Since: since}
}

// gateFailed ends a run whose window ended at a gate (the TV watched, no time left to
// restart): the home's prepared release is let go, the run fails (tried again in the
// next window), and a release held so for stuckWindows windows is told once, for an
// admin to install it now.
func (d *Driver) gateFailed(ctx context.Context, run Run, det RunDetail, code string) error {
	_ = d.abortHome(ctx, run)
	d.Svc.notify(ctx, NoticeRunFailed, notify.Payload{Severity: notify.SeverityWarn,
		Params: map[string]any{"version": run.Version, "code": code}, Link: "/admin/updates",
		Subject: "release:" + run.Version + ":" + code, Every: time.Hour})
	if err := d.finish(ctx, run, det, RunFailed, map[string]any{"code": code,
		"detail": "the window ended before the home could restart; tried again in the next window"}); err != nil {
		return err
	}
	if st := d.Svc.stuck(ctx, run.Version); st != nil {
		d.Svc.notify(ctx, NoticeStuck, notify.Payload{Severity: notify.SeverityWarn,
			Params: map[string]any{"version": st.Version, "code": st.Code, "windows": st.Windows},
			Link:   "/admin/updates", Subject: "release:" + run.Version + ":stuck:" + code, Every: notifyOnce})
		d.audit(ctx, run, "release_stuck", map[string]any{"code": code, "windows": st.Windows})
	}
	return nil
}
