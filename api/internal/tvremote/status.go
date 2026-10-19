package tvremote

import (
	"context"
	"fmt"
	"time"
)

// RemoteStatus is GET /api/tv/remote's controller part (story 32.10 AC6):
// the pool, the pushes, the sessions, the month's usage and what each limit
// is doing.
type RemoteStatus struct {
	Ready        bool         `json:"ready"`
	Reason       string       `json:"reason,omitempty"`
	Unavailable  string       `json:"unavailable,omitempty"`
	Inputs       []Input      `json:"inputs"`
	Pushes       []PushView   `json:"pushes"`
	Sessions     int          `json:"sessions"`
	Estimate     Estimate     `json:"estimate"`
	Storage      StorageGuard `json:"storage"`
	Limits       Limits       `json:"limits"`
	BudgetNotice any          `json:"budget_notice,omitempty"`
	Settings     SettingsView `json:"settings"`
}

// PushView is a running (or recent) push with its viewers.
type PushView struct {
	Push
	Viewers int `json:"viewers"`
}

// Limits says what each limit is doing ("2 of 22 remote channels in use").
type Limits struct {
	ChannelsInUse  int     `json:"channels_in_use"`
	MaxChannels    int     `json:"max_channels"`
	ChannelsLine   string  `json:"channels_line"`
	StoredLine     string  `json:"stored_line"`
	PerUser        int     `json:"max_streams_per_user"`
	UploadMbps     float64 `json:"upload_mbps"`
	UploadLine     string  `json:"upload_line"`
	NoAudioOnly    string  `json:"no_audio_only"`
	BudgetMonthUTC string  `json:"budget_month_utc"`
}

// SettingsView echoes the effective settings the page needs.
type SettingsView struct {
	TokenTTLMinutes    int      `json:"token_ttl_minutes"`
	IdleTimeoutMinutes int      `json:"idle_timeout_minutes"`
	MaxPushHours       int      `json:"max_push_hours"`
	Ingest             string   `json:"ingest"`
	BadSettings        []string `json:"bad_settings,omitempty"`
}

// Status reports the controller (admin).
func (c *Controller) Status(ctx context.Context) (RemoteStatus, error) {
	c.init()
	now := c.now()
	s := c.settings(ctx)
	var st RemoteStatus
	st.Ready, st.Reason = c.Ready(ctx)
	st.Unavailable = c.unavailable(ctx)
	ins, err := c.st.inputs(ctx)
	if err != nil {
		return st, err
	}
	st.Inputs = ins
	if st.Inputs == nil {
		st.Inputs = []Input{}
	}
	open, err := c.st.openSessions(ctx)
	if err != nil {
		return st, err
	}
	st.Sessions = len(open)
	viewers := map[string]int{}
	for _, x := range open {
		viewers[x.PushID]++
	}
	recent, err := c.st.pushesSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return st, err
	}
	st.Pushes = []PushView{}
	running := 0
	for i := len(recent) - 1; i >= 0 && len(st.Pushes) < 50; i-- {
		p := recent[i]
		if Running(p.State) {
			running++
		}
		st.Pushes = append(st.Pushes, PushView{Push: p, Viewers: viewers[p.ID]})
	}
	st.Estimate, err = c.Estimate(ctx, s, now)
	if err != nil {
		return st, err
	}
	cl, _, _ := c.client(ctx)
	st.Storage, err = c.Storage(ctx, cl, s, now)
	if err != nil {
		return st, err
	}
	// ≈ 6.5 Mb/s per pushed channel at the default bitrate (6 Mb/s video + audio)
	const perChannelMbps = 6.5
	st.Limits = Limits{
		ChannelsInUse: running, MaxChannels: s.MaxChannels,
		ChannelsLine: fmt.Sprintf("%d of %d remote channels in use", running, s.MaxChannels),
		StoredLine: fmt.Sprintf("stored minutes: %.0f of %.0f (%.0f reserved by running broadcasts)",
			st.Storage.StoredMinutes+st.Storage.OtherMinutes, st.Storage.CapacityMinutes,
			st.Storage.RunningReservedMinutes+st.Storage.PendingRecordingMinutes+st.Storage.OtherMinutes),
		PerUser:    s.MaxStreamsPerUser,
		UploadMbps: float64(running) * perChannelMbps,
		UploadLine: fmt.Sprintf("upload: about %.1f Mbit/s now (one channel bitrate per pushed channel; %d channels ≈ %.0f Mbit/s)",
			float64(running)*perChannelMbps, s.MaxChannels, float64(s.MaxChannels)*perChannelMbps),
		NoAudioOnly:    "there is no audio-only saving: Cloudflare bills minutes delivered, not bytes",
		BudgetMonthUTC: "the budget month follows Cloudflare billing (UTC)",
	}
	var notice map[string]any
	if c.Integration != nil && c.Integration.getSetting(ctx, SettingBudgetNotice, &notice) == nil {
		if m, _ := notice["month"].(string); m == st.Estimate.Month {
			st.BudgetNotice = notice
		}
	}
	st.Settings = SettingsView{TokenTTLMinutes: int(s.TokenTTL / time.Minute), IdleTimeoutMinutes: int(s.IdleTimeout / time.Minute),
		MaxPushHours: s.MaxPushHours, Ingest: s.Ingest, BadSettings: s.Bad}
	return st, nil
}
