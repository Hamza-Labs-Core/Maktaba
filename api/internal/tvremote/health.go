package tvremote

// Story 32.11 AC2: every minute the home writes the TV numbers the control
// centre's health view charts into health_samples (slot 0089) and refreshes
// the remote gauges on /metrics:
//
//	tv_hosts_up            streaming hosts heartbeating now
//	tv_channels_on_air     channels on air on any host
//	tv_remote_viewers      open remote sessions
//	tv_remote_pushes_live  pushes live at Cloudflare
//	tv_remote_cost_usd     the month's cost estimate

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"time"
)

// Health sample metric names.
const (
	SampleHostsUp     = "tv_hosts_up"
	SampleChannels    = "tv_channels_on_air"
	SampleViewers     = "tv_remote_viewers"
	SamplePushesLive  = "tv_remote_pushes_live"
	SampleRemoteCost  = "tv_remote_cost_usd"
	HealthEvery       = time.Minute
	hostUpWithin      = 30 * time.Second // a host beats every 5 s
	channelLiveWithin = 45 * time.Second // channel_runtime's stale window
	sqliteHealthTime  = "2006-01-02T15:04:05.000Z"
)

// HealthSampler writes the samples. Remote may be nil (only the cluster
// numbers are written then).
type HealthSampler struct {
	DB     *sql.DB
	Remote *Controller
	Logger *slog.Logger
	// Host is the health_samples host column (default: the hostname).
	Host string
	Now  func() time.Time
}

// Sample is one minute's numbers.
type Sample struct {
	HostsUp      int
	ChannelsLive int
	Viewers      int
	PushesLive   int
	CostUSD      float64
	Month        string
	Minutes      float64
	// per channel / per state, for the gauges
	viewersBy map[string]int
	pushesBy  map[string]int
}

func (h *HealthSampler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func (h *HealthSampler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func (h *HealthSampler) host() string {
	if h.Host != "" {
		return h.Host
	}
	if n, err := os.Hostname(); err == nil && n != "" {
		return n
	}
	return "home"
}

// Run samples every minute until ctx ends.
func (h *HealthSampler) Run(ctx context.Context) {
	t := time.NewTicker(HealthEvery)
	defer t.Stop()
	failing := false
	for {
		if err := h.Tick(ctx); err != nil {
			if !failing {
				h.log().Warn("tv: health samples not written", "err", err, "event", "tv_health_samples_failed")
			}
			failing = true
		} else {
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick takes one sample, writes it and refreshes the gauges.
func (h *HealthSampler) Tick(ctx context.Context) error {
	now := h.now()
	s, err := h.Take(ctx, now)
	if err != nil {
		return err
	}
	if h.Remote != nil {
		rmetrics().observe(s.viewersBy, s.pushesBy, s.Month, s.Minutes, s.CostUSD)
	}
	return h.write(ctx, now, s)
}

func (h *HealthSampler) postgres() bool { return newStore(h.DB).postgres }

// Take reads the numbers; a missing table counts as zero (a home before
// its cluster or remote migrations).
func (h *HealthSampler) Take(ctx context.Context, now time.Time) (Sample, error) {
	st := newStore(h.DB)
	s := Sample{viewersBy: map[string]int{}, pushesBy: map[string]int{}}
	if rows, err := h.DB.QueryContext(ctx, `SELECT heartbeat_at FROM streaming_hosts`); err == nil {
		for rows.Next() {
			var beat nt
			if rows.Scan(&beat) == nil && beat.Valid && now.Sub(beat.T) <= hostUpWithin {
				s.HostsUp++
			}
		}
		rows.Close()
	}
	if rows, err := h.DB.QueryContext(ctx, `SELECT COALESCE(heartbeat_at, last_segment_at), state FROM channel_runtime`); err == nil {
		for rows.Next() {
			var beat nt
			var state ns
			if rows.Scan(&beat, &state) == nil && beat.Valid && now.Sub(beat.T) <= channelLiveWithin && state.S != "idle" {
				s.ChannelsLive++
			}
		}
		rows.Close()
	}
	if h.Remote == nil {
		return s, nil
	}
	if open, err := st.openSessions(ctx); err == nil {
		s.Viewers = len(open)
		for _, x := range open {
			s.viewersBy[x.ChannelID]++
		}
	}
	if ps, err := st.activePushes(ctx); err == nil {
		for _, p := range ps {
			s.pushesBy[p.State]++
		}
		s.PushesLive = s.pushesBy[PushLive]
	}
	if e, err := h.Remote.Estimate(ctx, h.Remote.settings(ctx), now); err == nil {
		s.CostUSD, s.Month, s.Minutes = e.TotalUSD, e.Month, e.DeliveredMinutes+e.RunningMinutes
	}
	return s, nil
}

func (h *HealthSampler) write(ctx context.Context, now time.Time, s Sample) error {
	at := now.Truncate(time.Minute)
	var atArg any = at
	if !h.postgres() {
		atArg = at.Format(sqliteHealthTime)
	}
	rows := []struct {
		metric string
		value  float64
	}{
		{SampleHostsUp, float64(s.HostsUp)},
		{SampleChannels, float64(s.ChannelsLive)},
	}
	if h.Remote != nil {
		rows = append(rows, []struct {
			metric string
			value  float64
		}{
			{SampleViewers, float64(s.Viewers)},
			{SamplePushesLive, float64(s.PushesLive)},
			{SampleRemoteCost, s.CostUSD},
		}...)
	}
	host := h.host()
	for _, r := range rows {
		if _, err := h.DB.ExecContext(ctx, `INSERT INTO health_samples (at, host, metric, value) VALUES ($1, $2, $3, $4)
			ON CONFLICT (metric, host, at) DO UPDATE SET value = excluded.value`, atArg, host, r.metric, r.value); err != nil {
			return err
		}
	}
	return nil
}
