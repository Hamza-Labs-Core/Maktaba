package tvremote

import (
	"context"
	"fmt"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

// RemoteStatus is GET /api/tv/remote's controller part (story 32.10 AC6):
// the pool, the pushes, the sessions, the month's usage and what each limit
// is doing.
type RemoteStatus struct {
	Ready       bool       `json:"ready"`
	Reason      string     `json:"reason,omitempty"`
	Unavailable string     `json:"unavailable,omitempty"`
	Inputs      []Input    `json:"inputs"`
	Pushes      []PushView `json:"pushes"`
	Sessions    int        `json:"sessions"`
	// SessionList: the open remote sessions (story 32.11 AC4).
	SessionList []SessionView `json:"session_list"`
	// Events: the last 20 remote events from this API's log ring (AC4).
	Events       []Event      `json:"events"`
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
	// CFViewers is Cloudflare's live viewer count (GET /{input}/views) for a
	// live push; nil when hidden or unavailable (story 32.11 EC1).
	CFViewers     *int   `json:"cf_viewers"`
	ChannelName   string `json:"channel_name,omitempty"`
	ChannelNumber int    `json:"channel_number,omitempty"`
}

// SessionView is an open remote session as the admin page shows it: the
// user's name, never an address.
type SessionView struct {
	ID            string    `json:"id"`
	User          string    `json:"user"`
	Device        string    `json:"device"`
	ChannelID     string    `json:"channel_id"`
	ChannelName   string    `json:"channel_name,omitempty"`
	ChannelNumber int       `json:"channel_number,omitempty"`
	PushID        string    `json:"push_id,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	LastBeatAt    time.Time `json:"last_beat_at"`
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
	chans := map[string]channelInfo{}
	chanOf := func(id string) channelInfo {
		if ch, ok := chans[id]; ok {
			return ch
		}
		ch, _ := c.st.channel(ctx, id)
		chans[id] = ch
		return ch
	}
	st.SessionList = []SessionView{}
	names := map[string]string{}
	for _, x := range open {
		name, ok := names[x.UserID]
		if !ok {
			var n ns
			if c.DB.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, x.UserID).Scan(&n) == nil {
				name = n.S
			}
			names[x.UserID] = name
		}
		if name == "" {
			name = x.UserID
		}
		ch := chanOf(x.ChannelID)
		st.SessionList = append(st.SessionList, SessionView{ID: x.ID, User: name, Device: x.Device, ChannelID: x.ChannelID,
			ChannelName: ch.Name, ChannelNumber: ch.Number, PushID: x.PushID, StartedAt: x.StartedAt, LastBeatAt: x.LastBeatAt})
	}
	st.Events = c.events(20)
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
		ch := chanOf(p.ChannelID)
		st.Pushes = append(st.Pushes, PushView{Push: p, Viewers: viewers[p.ID], ChannelName: ch.Name, ChannelNumber: ch.Number})
	}
	c.cfViewers(ctx, st.Pushes, st.Inputs)
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

// Availability is what /api/tv/info says about remote viewing (story 32.9):
// whether a tune can be served, why not, and the limits the player shows.
type Availability struct {
	Available       bool
	Reason          string
	MaxStreams      int
	TokenTTLMinutes int
	Detection       string
}

// Availability reports remote viewing for /api/tv/info.
func (c *Controller) Availability(ctx context.Context) Availability {
	c.init()
	s := c.settings(ctx)
	ok, why := c.Ready(ctx)
	return Availability{Available: ok, Reason: why, MaxStreams: s.MaxStreamsPerUser,
		TokenTTLMinutes: int(s.TokenTTL / time.Minute), Detection: s.RemoteDetection}
}

// cfViewers fills each live push's Cloudflare viewer count (2 s budget for
// all of them; a miss leaves nil, shown as "—").
func (c *Controller) cfViewers(ctx context.Context, pushes []PushView, inputs []Input) {
	byID := map[string]Input{}
	for _, in := range inputs {
		byID[in.ID] = in
	}
	var cl *cf.Client
	var code string
	for i := range pushes {
		p := &pushes[i]
		in, ok := byID[p.InputID]
		if p.State != PushLive || !ok || in.CFUID == "" {
			continue
		}
		if cl == nil {
			var err error
			if cl, code, err = c.client(ctx); err != nil || cl == nil || code == "" {
				return
			}
		}
		vctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if n, ok := cl.LiveViewers(vctx, code, in.CFUID); ok {
			p.CFViewers = &n
		}
		cancel()
	}
}
