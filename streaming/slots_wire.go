package main

// The home's front door with TV slots (Epic 33 story 33.1, design §1-§2): this
// process keeps SSDP, HDHomeRun, the guide, every client connection, the
// placement service, the host heartbeat and the push relay; the channel engine
// runs in the slot processes the supervisor starts (blue, green, amber), and
// the front door routes the home's channels to the live one, failing over to
// the hot standby.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/relay"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/hdhr"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// frontdoorSlotsLink is VOD's view of the TV slots (vodGate): their reported
// sessions, the channels' caps, and a warm sync on demand.
type frontdoorSlotsLink struct {
	slots *frontdoor.Slots
	b     *tvBase
}

func (l frontdoorSlotsLink) Sessions(ctx context.Context) (int, int, int) {
	out := l.b.settings.Output(ctx)
	return l.slots.EncodersUsed(), out.NVENCMax, out.VTMax
}

func (l frontdoorSlotsLink) Sync(ctx context.Context) bool {
	return l.slots.SyncNow(ctx, 3*time.Second)
}

func (b *tvBase) buildHomeSlots(ctx context.Context, addrs map[string]string, ident channel.HostIdentity,
	chRepo *channel.PGRepo, publicBase string) []func(chi.Router) {
	logger := b.logger
	b.caps = cluster.DetectCapabilities(ctx, runtime.GOOS, b.cfg.FFmpeg.Binary, os.Getenv("MAKTABA_YTDLP"), nil)
	if err := cluster.EnsureSecret(ctx, b.pool); err != nil {
		logger.Warn("tv: creating the cluster stream key failed", "err", err.Error(), "event", "tv_secret_failed")
	}
	keyring := cluster.NewKeyring(nil)
	store := placement.PGStore{Pool: b.pool}
	csettings := &placement.PGSettings{Pool: b.pool}
	hosts := &placement.Hosts{Source: store, Settings: csettings, Protocol: cluster.ProtocolVersion, Logger: logger}
	svc := &placement.Service{Store: store, Hosts: hosts, Settings: csettings, Self: ident.ID, Logger: logger}
	tvmetrics.Register(svc.Collector(placement.OnAirPG(b.pool)))
	tracker := frontdoor.NewTracker()
	tracker.WarmGrace = func() time.Duration { return b.settings.Output(context.Background()).WarmGrace }

	var relayMu sync.Mutex
	var relayCtl *relay.Ctl
	staging := &frontdoor.PGStagingSettings{Pool: b.pool}
	slots := &frontdoor.Slots{Host: ident.ID, Addrs: addrs, Pool: b.pool, Logger: logger,
		Settings:   staging.Staging,
		Supervisor: os.Getenv("MAKTABA_SUPERVISOR_URL"),
		Demand:     tracker.Demands,
		Budget: func(ctx context.Context) (int, int, int) {
			c, enc := b.capacity(ctx)
			out := b.settings.Output(ctx)
			nv, vt := 0, 0
			switch enc {
			case channel.EncoderNVENC:
				nv = out.NVENCMax
			case channel.EncoderVideoToolbox:
				vt = out.VTMax
			}
			// less the sessions VOD holds or is taking (vodGate)
			nv, vt = slotBudget(nv, vt)
			return c, nv, vt
		},
		RemoteViewers: remoteViewers(b),
		OnSwitch: func(_, _ string, _ int64) {
			relayMu.Lock()
			ctl := relayCtl
			relayMu.Unlock()
			if ctl != nil {
				ctl.KickAll()
			}
		}}
	// VOD's GPU sessions count with the slots' (vodGate)
	tvSlots.Store(linkBox{frontdoorSlotsLink{slots: slots, b: b}})
	for {
		err := slots.Bootstrap(ctx)
		if err == nil {
			break
		}
		logger.Warn("tv: reading the slot roles failed; retrying", "err", err.Error(), "event", "tv_slot_bootstrap_failed")
		select {
		case <-ctx.Done():
			// shutting down before the slots were read: no TV routes
			return nil
		case <-time.After(2 * time.Second):
		}
	}
	b.bg.Go(slots.Run)
	// story 33.7/33.9: the swap and rollback operations the change controller
	// (and the automatic rollback) write for this host
	b.bg.Go(slots.RunOps)

	fd := &frontdoor.Handler{Svc: svc, Settings: csettings, Self: ident.ID, Keyring: keyring, Tracker: tracker,
		Logger: logger, Slots: slots, CacheRoot: b.cfg.Cache.Root,
		PublicBase: publicBase}

	liveStations := func() []uuid.UUID { return slots.LiveStations() }
	b.loop = &cluster.HostLoop{Store: cluster.PGBeatStore{Pool: b.pool}, HostID: ident.ID, Keyring: keyring, Logger: logger,
		Info: func(ctx context.Context) cluster.BeatInfo {
			info := b.beatInfo(ctx, channel.RoleHome, liveStations, ident.StreamBaseURL, "")
			pins, last := svc.Notices()
			info.Load["viewers"] = tracker.Snapshot()
			live, standby, stg, epoch := slots.Roles()
			info.Load["tv_slots"] = map[string]any{"live": live, "standby": standby, "staging": stg, "epoch": epoch,
				"healthy": slots.Healthy()}
			if len(pins) > 0 {
				info.Load["pin_skips"] = pins
			}
			if last != nil {
				info.Load["no_room"] = last
			}
			return info
		}}
	_ = b.loop.BeatOnce(ctx)
	hosts.Refresh(ctx)
	b.bg.Go(b.loop.Run)
	b.bg.Go(func(ctx context.Context) {
		placement.Listen(ctx, b.pool, logger, func(ch uuid.UUID) { svc.Invalidate(ch) })
	})
	sweeper := &placement.Sweeper{Svc: svc, Demand: tracker.Demand, Cleaner: func(ctx context.Context) {
		if n, err := store.CleanStaleRuntime(ctx); err == nil && n > 0 {
			logger.Info("tv: cleaned stale on-air rows", "rows", n, "event", "tv_runtime_cleaned")
		}
	}}
	b.bg.Go(sweeper.Run)
	b.bg.Go(func(ctx context.Context) {
		b.runPushRelay(ctx, fd, func(ctl *relay.Ctl) {
			relayMu.Lock()
			relayCtl = ctl
			relayMu.Unlock()
		})
	})

	assets := &cluster.AssetHandler{Looks: chRepo, PathMap: pathmap.FromEnv(), Keyring: keyring}
	logger.Info("tv: front door ready with TV slots", "host_id", ident.ID, "slots", slot.FormatAddrs(addrs),
		"event", "tv_frontdoor_ready")

	// story 33.6: the admin preview of a staged change (token-gated; no HDHomeRun)
	mounts := []func(chi.Router){fd.Mount, fd.MountInternal, fd.MountPreview, assets.Mount, slotReports(slots)}
	hdhrRepo := hdhr.NewPGRepo(b.pool)
	hdhrHandler := hdhr.New(hdhrRepo, fd)
	hdhrHandler.PublicBase = publicBase
	mounts = append(mounts, hdhrHandler.Mount)
	guideHandler := b.newGuideHandler(hdhrRepo, publicBase, nil)
	assets.Guide = guideHandler // the guide's images for the nodes
	mounts = append(mounts, guideHandler.Mount)
	// SSDP (udp/1900) and HDHomeRun discovery (udp/65001), absolute URLs only
	ssdp := &hdhr.SSDPController{Repo: hdhrRepo, Logger: logger, LocationBase: publicBase,
		HTTPAddr: os.Getenv("MAKTABA_HTTP_ADDR")}
	b.bg.Go(ssdp.Run)
	return mounts
}

// slotReports mounts the supervisor's loopback report of a slot process that
// ended (POST /internal/tv/slot-exited/{slot}): faster than the next probe.
func slotReports(slots *frontdoor.Slots) func(chi.Router) {
	return func(r chi.Router) {
		r.Post("/internal/tv/slot-exited/{slot}", func(w http.ResponseWriter, req *http.Request) {
			if !slot.Loopback(req.RemoteAddr) || req.Header.Get("X-Forwarded-For") != "" {
				http.Error(w, "loopback only", http.StatusForbidden)
				return
			}
			name := chi.URLParam(req, "slot")
			if !slot.Valid(name) {
				http.Error(w, "bad slot", http.StatusBadRequest)
				return
			}
			slots.Exited(req.Context(), name)
			live, standby, staging, epoch := slots.Roles()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"live": live, "standby": standby, "staging": staging, "epoch": epoch})
		})
	}
}

// remoteViewers counts the open remote sessions per channel (cached 10 s), to
// rank the pushes in the standby's warm set.
func remoteViewers(b *tvBase) func(ctx context.Context) map[uuid.UUID]int {
	var mu sync.Mutex
	var at time.Time
	cur := map[uuid.UUID]int{}
	return func(ctx context.Context) map[uuid.UUID]int {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) < 10*time.Second {
			return cur
		}
		at = time.Now()
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		rows, err := b.pool.Query(qctx, `SELECT channel_id::text, count(*) FROM tv_remote_sessions
			WHERE ended_at IS NULL GROUP BY channel_id`)
		if err != nil {
			return cur
		}
		defer rows.Close()
		next := map[uuid.UUID]int{}
		for rows.Next() {
			var ch string
			var n int
			if rows.Scan(&ch, &n) == nil {
				if id, err := uuid.Parse(ch); err == nil {
					next[id] = n
				}
			}
		}
		cur = next
		return cur
	}
}
