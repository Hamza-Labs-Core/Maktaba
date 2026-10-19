package tvremote

import (
	"context"
	"math"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

// StorageGuard is the storage check before a new push (story 32.10 AC5,
// design §7.7).
//
// A running broadcast is recorded while it runs (recording mode automatic)
// and its recording is deleted only after it ends, so a push started now will
// hold up to max_push_hours of storage before its rotation ends it. The guard
// therefore budgets, against the account's storage capacity:
//
//   - every running push's whole reservation: the minutes it already stored
//     plus the minutes it may still store until its rotation (max_push_hours);
//   - every recording not yet deleted (ended broadcasts waiting for cleanup);
//   - what the account stores besides Maktaba's broadcasts;
//   - the new push's own reservation (max_push_hours) and a safety margin.
//
// Counting only the running pushes' elapsed minutes would let a third push
// start on one 1,000-minute block while the first two are young, and the
// storage would still run out hours later.
type StorageGuard struct {
	CapacityMinutes float64 `json:"capacity_minutes"`
	// CapacitySource: cloudflare (the account's storage-usage answer) or
	// settings (storage_plan_minutes, when Cloudflare cannot be read).
	CapacitySource string `json:"capacity_source"`
	// UsedMinutes is what Cloudflare reports stored now (0 when unknown).
	UsedMinutes float64 `json:"used_minutes"`
	// StoredMinutes is what Maktaba's broadcasts store now (running ones'
	// elapsed minutes plus recordings not yet deleted).
	StoredMinutes float64 `json:"stored_minutes"`
	// OtherMinutes is the account's storage that is not Maktaba's broadcasts.
	OtherMinutes float64 `json:"other_minutes"`
	// RunningReservedMinutes is Σ over running pushes of max(elapsed, max_push).
	RunningReservedMinutes float64 `json:"running_reserved_minutes"`
	// PendingRecordingMinutes is Σ of the recordings not yet deleted.
	PendingRecordingMinutes float64 `json:"pending_recording_minutes"`
	RunningPushes           int     `json:"running_pushes"`
	// NeedMinutes is a new push's reservation (max_push_hours).
	NeedMinutes   float64 `json:"need_minutes"`
	MarginMinutes float64 `json:"margin_minutes"`
	// FreeMinutes = capacity − reserved (other + running + pending).
	FreeMinutes float64 `json:"free_minutes"`
	Fits        bool    `json:"fits"`
	Hint        string  `json:"hint,omitempty"`
}

// storageUsage is Cloudflare's storage answer, cached for a minute.
func (c *Controller) storageUsage(ctx context.Context, cl *cf.Client, now time.Time) (cf.StorageUsage, error) {
	c.mu.Lock()
	cached := c.stor
	c.mu.Unlock()
	if !cached.at.IsZero() && now.Sub(cached.at) < time.Minute {
		return cached.u, cached.err
	}
	if cl == nil {
		return cf.StorageUsage{}, errNoClient
	}
	u, err := cl.StorageUsage(ctx)
	c.mu.Lock()
	c.stor = storageCache{u: u, err: err, at: now}
	c.mu.Unlock()
	return u, err
}

// ForgetStorage drops the cached storage answer (tests, a new storage plan).
func (c *Controller) ForgetStorage() {
	c.init()
	c.mu.Lock()
	c.stor = storageCache{}
	c.mu.Unlock()
}

// Storage computes the guard for one more push.
func (c *Controller) Storage(ctx context.Context, cl *cf.Client, s Settings, now time.Time) (StorageGuard, error) {
	c.init()
	g := StorageGuard{NeedMinutes: float64(s.MaxPushHours * 60), MarginMinutes: float64(s.StorageMarginMinutes)}
	u, err := c.storageUsage(ctx, cl, now)
	if err == nil && u.TotalStorageMinutesLimit > 0 {
		g.CapacityMinutes, g.CapacitySource, g.UsedMinutes = u.TotalStorageMinutesLimit, "cloudflare", u.TotalStorageMinutes
	} else {
		g.CapacityMinutes, g.CapacitySource = float64(s.StoragePlanMinutes), "settings"
	}
	pushes, err := c.st.activePushes(ctx)
	if err != nil {
		return g, err
	}
	for _, p := range pushes {
		if !Running(p.State) {
			continue
		}
		g.RunningPushes++
		age := 0.0
		if p.LiveAt != nil {
			age = math.Max(0, now.Sub(*p.LiveAt).Minutes())
		}
		g.StoredMinutes += age
		g.RunningReservedMinutes += math.Max(age, g.NeedMinutes)
	}
	recs, err := c.st.undeletedRecordings(ctx)
	if err != nil {
		return g, err
	}
	for _, p := range recs {
		if p.LiveAt == nil {
			continue
		}
		end := now
		if p.EndedAt != nil {
			end = *p.EndedAt
		}
		m := math.Max(0, end.Sub(*p.LiveAt).Minutes())
		g.PendingRecordingMinutes += m
		g.StoredMinutes += m
	}
	if g.CapacitySource == "cloudflare" {
		g.OtherMinutes = math.Max(0, g.UsedMinutes-g.StoredMinutes)
	}
	reserved := g.OtherMinutes + g.RunningReservedMinutes + g.PendingRecordingMinutes
	g.FreeMinutes = g.CapacityMinutes - reserved
	g.Fits = reserved+g.NeedMinutes+g.MarginMinutes <= g.CapacityMinutes
	if !g.Fits {
		g.Hint = "buy another 1,000-minute block or lower max_push_hours"
	}
	return g, nil
}
