package tvremote

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

var errNoClient = errors.New("tvremote: no Cloudflare client")

// SettingBudgetNotice records the highest budget threshold already announced
// this month ({month, level}); the admin page shows it.
const SettingBudgetNotice = "tv_remote.budget_notice"

// Estimate is the month's cost estimate (story 32.10 AC3, design §9). The
// month is the UTC calendar month (Cloudflare bills in UTC, EC1).
type Estimate struct {
	Month string `json:"month"`
	// DeliveredMinutes is what the daily usage rows hold (analytics or sessions).
	DeliveredMinutes float64 `json:"delivered_minutes"`
	// RunningMinutes is the open sessions' minutes since the last usage poll:
	// added so the estimate errs high, never low.
	RunningMinutes float64 `json:"running_minutes"`
	// SimulcastMinutes are this month's live output minutes (32.13): billed
	// as minutes delivered. Added on top of the analytics too, so the
	// estimate errs high (whether Cloudflare's per-video analytics already
	// include output minutes is for the spike to show).
	SimulcastMinutes     float64 `json:"simulcast_minutes"`
	DeliveryUSD          float64 `json:"delivery_usd"`
	StorageUSD           float64 `json:"storage_usd"`
	TotalUSD             float64 `json:"total_usd"`
	BudgetUSD            float64 `json:"budget_usd"`
	Percent              float64 `json:"percent"`
	Action               string  `json:"action"`
	Source               string  `json:"source"` // analytics | sessions
	PeakRecordingMinutes float64 `json:"peak_recording_minutes"`
	// Exhausted: at 100 % with budget_action block_new: new tunes get 402.
	Exhausted bool `json:"exhausted"`
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Estimate computes the month's estimate now.
func (c *Controller) Estimate(ctx context.Context, s Settings, now time.Time) (Estimate, error) {
	c.init()
	ms := monthStart(now)
	e := Estimate{Month: ms.Format("2006-01"), BudgetUSD: s.MonthlyBudgetUSD, Action: s.BudgetAction, Source: "sessions"}
	days, err := c.st.usageSince(ctx, ms.Format("2006-01-02"))
	if err != nil {
		return e, err
	}
	for _, d := range days {
		e.DeliveredMinutes += d.MinutesDelivered
		e.PeakRecordingMinutes = math.Max(e.PeakRecordingMinutes, d.PeakRecordingMinutes)
		if d.Source == "analytics" {
			e.Source = "analytics"
		}
	}
	c.mu.Lock()
	last := c.usage
	c.mu.Unlock()
	xs, err := c.st.openSessions(ctx)
	if err != nil {
		return e, err
	}
	for _, x := range xs {
		from := x.StartedAt
		if last.After(from) {
			from = last
		}
		if from.Before(ms) {
			from = ms
		}
		if now.After(from) {
			e.RunningMinutes += now.Sub(from).Minutes()
		}
	}
	e.SimulcastMinutes = c.simulcastMinutes(ctx, now)
	e.DeliveryUSD = (e.DeliveredMinutes + e.RunningMinutes + e.SimulcastMinutes) * s.DeliveryUSDPer1000 / 1000
	e.StorageUSD = float64(s.StoragePlanMinutes) / 1000 * s.StorageUSDPer1000
	e.TotalUSD = e.DeliveryUSD + e.StorageUSD
	if s.MonthlyBudgetUSD > 0 {
		e.Percent = e.TotalUSD / s.MonthlyBudgetUSD * 100
		e.Exhausted = e.Percent >= 100 && s.BudgetAction == BudgetBlockNew
	}
	return e, nil
}

// budgetLevel is the threshold an estimate has reached (0, 80 or 100).
func budgetLevel(e Estimate) int {
	switch {
	case e.BudgetUSD <= 0:
		return 0
	case e.Percent >= 100:
		return 100
	case e.Percent >= 80:
		return 80
	}
	return 0
}

// budgetTick announces a threshold once per month (AC4): event tv_budget_80
// or tv_budget_100 and the admin notice.
func (c *Controller) budgetTick(ctx context.Context, s Settings, now time.Time) {
	if c.Integration == nil {
		return
	}
	e, err := c.Estimate(ctx, s, now)
	if err != nil {
		return
	}
	lvl := budgetLevel(e)
	if lvl == 0 {
		return
	}
	var n struct {
		Month string `json:"month"`
		Level int    `json:"level"`
	}
	_ = c.Integration.getSetting(ctx, SettingBudgetNotice, &n)
	if n.Month == e.Month && n.Level >= lvl {
		return
	}
	_ = c.Integration.putSetting(ctx, SettingBudgetNotice, map[string]any{"month": e.Month, "level": lvl,
		"at": now, "total_usd": round2(e.TotalUSD), "budget_usd": e.BudgetUSD, "action": e.Action})
	event := "tv_budget_80"
	if lvl == 100 {
		event = "tv_budget_100"
	}
	c.log().Warn("tv_remote: monthly budget threshold reached", "month", e.Month, "percent", round2(e.Percent),
		"total_usd", round2(e.TotalUSD), "budget_usd", e.BudgetUSD, "action", e.Action, "event", event)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// usageTick reads the month's delivered minutes hourly (story 32.10 AC2) and
// keeps the day's peak of stored minutes.
func (c *Controller) usageTick(ctx context.Context, s Settings, now time.Time) {
	c.mu.Lock()
	dueUsage := now.Sub(c.usage) >= UsagePoll
	duePeak := now.Sub(c.peakT) >= time.Minute
	c.mu.Unlock()
	if duePeak {
		c.mu.Lock()
		c.peakT = now
		c.mu.Unlock()
		if g, err := c.Storage(ctx, nil, s, now); err == nil && g.StoredMinutes > 0 {
			_ = c.st.putUsage(ctx, UsageDay{Day: now.Format("2006-01-02"), PeakRecordingMinutes: g.StoredMinutes,
				Source: "sessions"}, now)
		}
	}
	if !dueUsage {
		return
	}
	var cl *cf.Client
	if c.Integration != nil {
		cl, _, _ = c.Integration.cfClient(ctx)
	}
	if err := c.PollUsage(ctx, cl, s, now); err == nil {
		c.mu.Lock()
		c.usage = now
		c.mu.Unlock()
	}
}

// PollUsage writes the month's daily minutes: from Cloudflare's analytics
// for the video ids of this month's pushes, or (no Analytics permission)
// from the sessions' minutes, flagged "sessions".
func (c *Controller) PollUsage(ctx context.Context, cl *cf.Client, s Settings, now time.Time) error {
	c.init()
	ms := monthStart(now)
	pushes, err := c.st.pushesSince(ctx, ms)
	if err != nil {
		return err
	}
	var uids []string
	byUID := map[string]string{}
	for _, p := range pushes {
		if p.VideoUID != "" {
			uids = append(uids, p.VideoUID)
			byUID[p.VideoUID] = p.ID
		}
	}
	if cl != nil {
		rows, err := cl.MinutesViewedByVideo(ctx, ms, now.AddDate(0, 0, 1), uids)
		if err == nil {
			perDay := map[string]float64{}
			perPush := map[string]float64{}
			for _, r := range rows {
				perDay[r.Date] += r.Minutes
				perPush[byUID[r.UID]] += r.Minutes
			}
			for day, m := range perDay {
				if err := c.st.putUsage(ctx, UsageDay{Day: day, MinutesDelivered: m,
					CostUSD: m * s.DeliveryUSDPer1000 / 1000, Source: "analytics"}, now); err != nil {
					return err
				}
			}
			for id, m := range perPush {
				if id != "" {
					_ = c.st.setPush(ctx, id, map[string]any{"minutes_delivered": m})
				}
			}
			return nil
		}
		c.log().Info("tv_remote: analytics unreadable; estimating from sessions", "err", err, "event", "tv_usage_sessions")
	}
	xs, err := c.st.sessionsSince(ctx, ms)
	if err != nil {
		return err
	}
	perDay := map[string]float64{}
	for _, x := range xs {
		from, to := x.StartedAt, now
		if x.EndedAt != nil {
			to = *x.EndedAt
		}
		if from.Before(ms) {
			from = ms
		}
		for d := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC); d.Before(to); d = d.AddDate(0, 0, 1) {
			a, b := from, to
			if d.After(a) {
				a = d
			}
			if next := d.AddDate(0, 0, 1); next.Before(b) {
				b = next
			}
			if b.After(a) {
				perDay[d.Format("2006-01-02")] += b.Sub(a).Minutes()
			}
		}
	}
	for day, m := range perDay {
		if err := c.st.putUsage(ctx, UsageDay{Day: day, MinutesDelivered: m,
			CostUSD: m * s.DeliveryUSDPer1000 / 1000, Source: "sessions"}, now); err != nil {
			return err
		}
	}
	return nil
}
