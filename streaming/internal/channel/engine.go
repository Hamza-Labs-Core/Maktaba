package channel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Defaults for the engine lifecycle.
const (
	DefaultWarmGrace    = 60 * time.Second // keep a station warm after the last viewer
	DefaultPerHostCap   = 8                // max concurrent channel stations
	DefaultReadyTimeout = 20 * time.Second // first HLS request waits this long for a segment
)

// Pipeline is a running channel (production: *Station).
type Pipeline interface {
	Subscribe() (<-chan []byte, func())
	WaitReady(ctx context.Context) error
	Done() <-chan struct{}
	Alive() bool
	Stop() // non-blocking
	PID() int
	Dir() string
}

// StartFunc starts a channel pipeline writing its HLS window into dir.
type StartFunc func(ctx context.Context, channelID uuid.UUID, dir string) (Pipeline, error)

// Layout resolves the on-disk HLS directory for a channel session.
type Layout interface {
	HLSDir(sessionID string) string
}

// Engine owns the live-channel lifecycle: lazy activation on the first
// HLS / MPEG-TS request, one pipeline per channel shared by every viewer,
// a warm grace window after the last viewer, and the per-host cap.
type Engine struct {
	Repo   Repo
	Layout Layout
	Start  StartFunc
	Host   string
	// Identity is this host in the cluster (its id goes into
	// channel_runtime; a channel on air on another live host is refused).
	Identity HostIdentity
	// StaleAfter: another host's row older than this no longer owns its
	// channel (0: DefaultRuntimeStaleAfter).
	StaleAfter time.Duration
	// Status, when set, reports the encoder and hardware slots free for
	// the streaming_hosts heartbeat (the engine adds its own counts).
	Status       func(ctx context.Context) HostStatus
	WarmGrace    time.Duration
	ReadyTimeout time.Duration
	// Limits, when set, is read before each start and each reap: the
	// channels-on-air cap and the warm grace (the tv_output settings), so a
	// settings change applies without a restart. Nil: the cap given to
	// NewEngine and WarmGrace.
	Limits func(ctx context.Context) (maxActive int, warmGrace time.Duration)
	// Fence, when set, fences every station start by the channel's placement
	// (Epic 32.2): the placement, not channel_runtime, decides which host runs a
	// channel.
	Fence Fence
	// HostStatusOff: the cluster heartbeat (tv_host_beat) reports this host; the
	// engine no longer writes its streaming_hosts row itself.
	HostStatusOff bool
	// Logger for fencing events (nil: slog.Default).
	Logger *slog.Logger

	gens     map[uuid.UUID]int64
	mu       sync.Mutex
	stations map[uuid.UUID]Pipeline
	started  map[uuid.UUID]time.Time
	reg      *registry
	now      func() time.Time
}

// NewEngine builds an Engine. A nil clock uses time.Now; cap <= 0 uses
// DefaultPerHostCap.
func NewEngine(repo Repo, layout Layout, start StartFunc, perHostCap int, now func() time.Time) *Engine {
	if perHostCap <= 0 {
		perHostCap = DefaultPerHostCap
	}
	if now == nil {
		now = time.Now
	}
	return &Engine{
		Repo: repo, Layout: layout, Start: start,
		WarmGrace: DefaultWarmGrace, ReadyTimeout: DefaultReadyTimeout,
		stations: map[uuid.UUID]Pipeline{},
		gens:     map[uuid.UUID]int64{},
		started:  map[uuid.UUID]time.Time{},
		reg:      newRegistry(perHostCap, now),
		now:      now,
	}
}

// TuneResult is what a successful Tune returns.
type TuneResult struct {
	ChannelID   uuid.UUID
	SessionID   string
	ManifestURL string // relative path served by the channel HTTP handler
}

// Tune makes sure the channel is on air and returns its live playlist
// path. HLS requests tune implicitly; this is for API callers.
func (e *Engine) Tune(ctx context.Context, channelID uuid.UUID) (TuneResult, error) {
	if _, err := e.Ensure(ctx, channelID); err != nil {
		return TuneResult{}, err
	}
	return TuneResult{
		ChannelID:   channelID,
		SessionID:   channelSessionID(channelID),
		ManifestURL: "/stream/channel/" + channelID.String() + "/index.m3u8",
	}, nil
}

// Ensure returns the channel's running pipeline, starting it if cold.
func (e *Engine) Ensure(ctx context.Context, channelID uuid.UUID) (Pipeline, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for {
		p, ok := e.stations[channelID]
		if !ok {
			break
		}
		if p.Alive() {
			e.reg.touch(channelID)
			return p, nil
		}
		// A stopping station still owns the HLS dir and the runtime row:
		// wait for it to finish before a new one reuses them.
		e.mu.Unlock()
		err := waitDone(ctx, p, stopWait)
		e.mu.Lock()
		if err != nil {
			return nil, err
		}
		if e.stations[channelID] == p {
			delete(e.stations, channelID)
			e.reg.remove(channelID)
		}
	}
	if _, err := e.Repo.Look(ctx, channelID); err != nil {
		return nil, err
	}
	var grant Grant
	if e.Fence != nil {
		g, err := e.Fence.Allow(ctx, channelID)
		if err != nil {
			return nil, err
		}
		grant = g
	} else if err := e.ownedElsewhere(ctx, channelID); err != nil {
		return nil, err
	}
	e.applyLimits(ctx)
	evicted, err := e.reg.admitEvict(channelID)
	if err != nil {
		return nil, err
	}
	if evicted != uuid.Nil {
		e.releaseLocked(evicted)
	}
	startCtx := WithStartOptions(ctx, StartOptions{Discontinuity: grant.Discontinuity})
	p, err := e.Start(startCtx, channelID, e.Layout.HLSDir(channelSessionID(channelID)))
	if err != nil {
		return nil, err
	}
	e.stations[channelID] = p
	if e.Fence != nil {
		e.gens[channelID] = grant.Gen
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		e.Fence.Ack(actx, channelID, grant.Gen)
		cancel()
	}
	// Registry stop runs under the registry lock: it must not block or
	// call back into the engine; Pipeline.Stop is non-blocking.
	e.reg.putWith(channelID, p.Stop, 0)
	started := e.now()
	e.started[channelID] = started
	go func() {
		<-p.Done()
		e.mu.Lock()
		defer e.mu.Unlock()
		// Only the station still registered for the channel clears the
		// runtime row, under the engine lock so it is ordered against a
		// re-tune writing the new station's row.
		if e.stations[channelID] == p {
			delete(e.stations, channelID)
			delete(e.started, channelID)
			e.reg.remove(channelID)
			_ = e.Repo.ClearRuntime(context.Background(), channelID)
		}
	}()
	_ = e.Repo.SetRuntime(ctx, e.runtimeOf(channelID, p, started))
	return p, nil
}

// statsReporter is a pipeline that reports its encoder and counters
// (*Station).
type statsReporter interface{ Stats() StationStats }

// runtimeOf is the channel_runtime row for a running pipeline.
func (e *Engine) runtimeOf(channelID uuid.UUID, p Pipeline, started time.Time) Runtime {
	rt := Runtime{ChannelID: channelID, Host: e.Host, HostID: e.Identity.ID,
		StreamBaseURL: e.Identity.StreamBaseURL, PID: p.PID(), State: StateLive,
		StartedAt: started, LastSegmentAt: e.now(), ViewerCount: e.reg.viewers(channelID)}
	if sr, ok := p.(statsReporter); ok {
		st := sr.Stats()
		rt.Encoder, rt.Restarts, rt.NVENCFallbacks = st.Encoder, st.Restarts, st.NVENCFallbacks
	}
	return rt
}

// SyncRuntime rewrites every running channel's channel_runtime row
// (viewers, encoder, restarts, NVENC fallbacks). Runs under the engine
// lock so it is ordered against a stopping station clearing its row.
func (e *Engine) SyncRuntime(ctx context.Context) {
	e.mu.Lock()
	for id, p := range e.stations {
		if !p.Alive() {
			continue
		}
		wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_ = e.Repo.SetRuntime(wctx, e.runtimeOf(id, p, e.started[id]))
		cancel()
	}
	e.mu.Unlock()
	if w, ok := e.Repo.(HostStatusWriter); ok && !e.HostStatusOff {
		st := HostStatus{NVENCFree: -1, VTFree: -1}
		if e.Status != nil {
			st = e.Status(ctx)
		}
		st.Identity = e.Identity
		if st.Identity.Host == "" {
			st.Identity.Host = e.Host
		}
		st.ActiveChannels = e.reg.size()
		st.MaxActive = e.reg.capacity()
		wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_ = w.SetHostStatus(wctx, st)
		cancel()
	}
}

// ownedElsewhere refuses a start while another live host runs the
// channel (one encode per channel cluster-wide). A row of this host, or
// one whose heartbeat is stale, does not block.
func (e *Engine) ownedElsewhere(ctx context.Context, channelID uuid.UUID) error {
	o, ok := e.Repo.(RuntimeOwner)
	if !ok || e.Identity.ID == "" {
		return nil
	}
	rt, hb, found, err := o.RuntimeOf(ctx, channelID)
	if err != nil || !found || rt.HostID == "" || rt.HostID == e.Identity.ID {
		return nil
	}
	stale := e.StaleAfter
	if stale <= 0 {
		stale = DefaultRuntimeStaleAfter
	}
	if hb.IsZero() || e.now().Sub(hb) > stale {
		return nil
	}
	return &RunningElsewhereError{HostID: rt.HostID, Host: rt.Host, StreamBaseURL: rt.StreamBaseURL}
}

// ClearStaleRuntime drops this host's channel_runtime rows left from
// before a restart (stations live in memory: none survived it). Call it
// once at boot, before the first station starts.
func (e *Engine) ClearStaleRuntime(ctx context.Context) (int64, error) {
	c, ok := e.Repo.(HostRuntimeClearer)
	if !ok {
		return 0, nil
	}
	return c.ClearHostRuntime(ctx, e.Identity.ID, e.Host)
}

// applyLimits re-reads the cap and warm grace from Limits.
func (e *Engine) applyLimits(ctx context.Context) time.Duration {
	grace := e.WarmGrace
	if e.Limits == nil {
		return grace
	}
	maxActive, g := e.Limits(ctx)
	if maxActive > 0 {
		e.reg.setCap(maxActive)
	}
	if g >= 0 {
		grace = g
	}
	return grace
}

// EnsureReady starts (or reuses) the channel and waits for its first
// HLS segment (bounded by ReadyTimeout).
func (e *Engine) EnsureReady(ctx context.Context, channelID uuid.UUID) (Pipeline, error) {
	p, err := e.Ensure(ctx, channelID)
	if err != nil {
		return nil, err
	}
	timeout := e.ReadyTimeout
	if timeout <= 0 {
		timeout = DefaultReadyTimeout
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := p.WaitReady(wctx); err != nil {
		return p, err
	}
	return p, nil
}

// StreamChannelTS writes the channel's continuous MPEG-TS to w until ctx
// ends (the tuner/client disconnecting). Every client shares the
// channel's single encode. Satisfies hdhr.Streamer.
func (e *Engine) StreamChannelTS(ctx context.Context, channelID uuid.UUID, w io.Writer) error {
	flusher, _ := w.(http.Flusher)
	for attempt := 0; attempt < 3; attempt++ {
		p, err := e.Ensure(ctx, channelID)
		if err != nil {
			return err
		}
		e.reg.attach(channelID)
		err = pumpTS(ctx, p, w, flusher)
		e.reg.detach(channelID)
		if err != nil || ctx.Err() != nil {
			return err
		}
		// The station ended under us (muxer died): restart and resume.
	}
	return errors.New("channel: stream ended")
}

// tsWriteTimeout drops a TS client whose connection stalls (so its
// tuner lease is released promptly instead of when the hub gives up).
const tsWriteTimeout = 15 * time.Second

func pumpTS(ctx context.Context, p Pipeline, w io.Writer, flusher http.Flusher) error {
	ch, cancel := p.Subscribe()
	defer cancel()
	var rc *http.ResponseController
	if rw, ok := w.(http.ResponseWriter); ok {
		rc = http.NewResponseController(rw)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case b, ok := <-ch:
			if !ok {
				return nil
			}
			if rc != nil {
				_ = rc.SetWriteDeadline(time.Now().Add(tsWriteTimeout))
			}
			if _, err := w.Write(b); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// Detach records that a viewer left; the channel stays warm until the
// grace window expires.
func (e *Engine) Detach(channelID uuid.UUID) { e.reg.detach(channelID) }

// Touch refreshes the idle timestamp (HLS playlist/segment fetch).
func (e *Engine) Touch(channelID uuid.UUID) { e.reg.touch(channelID) }

// ReapIdle stops channels idle past the warm grace window and gives their
// placements back.
func (e *Engine) ReapIdle(ctx context.Context) []uuid.UUID {
	ids := e.reg.reapIdle(e.applyLimits(ctx))
	if e.Fence != nil && len(ids) > 0 {
		e.mu.Lock()
		for _, id := range ids {
			e.releaseLocked(id)
		}
		e.mu.Unlock()
	}
	return ids
}

// releaseLocked gives a stopped station's placement back (e.mu held).
func (e *Engine) releaseLocked(id uuid.UUID) {
	if e.Fence == nil {
		return
	}
	gen, ok := e.gens[id]
	if !ok {
		return
	}
	delete(e.gens, id)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	e.Fence.Release(ctx, id, gen)
	cancel()
}

// CheckFences re-checks every running station's placement (every few seconds and
// on a placement notification): a station whose placement now names another host,
// a newer generation elsewhere, or nothing at all stops (design §3.4). ids limits
// the check to those channels (none: all).
func (e *Engine) CheckFences(ctx context.Context, ids ...uuid.UUID) {
	if e.Fence == nil {
		return
	}
	type held struct {
		id  uuid.UUID
		p   Pipeline
		gen int64
	}
	e.mu.Lock()
	var list []held
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for id, p := range e.stations {
		if len(want) > 0 && !want[id] {
			continue
		}
		if g, ok := e.gens[id]; ok && p.Alive() {
			list = append(list, held{id, p, g})
		}
	}
	e.mu.Unlock()
	log := e.Logger
	if log == nil {
		log = slog.Default()
	}
	for _, h := range list {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		keep, newGen := e.Fence.Check(cctx, h.id, h.gen)
		cancel()
		if keep {
			if newGen > 0 && newGen != h.gen {
				e.mu.Lock()
				if e.stations[h.id] == h.p {
					e.gens[h.id] = newGen
				}
				e.mu.Unlock()
				actx, cancel := context.WithTimeout(ctx, 3*time.Second)
				e.Fence.Ack(actx, h.id, newGen)
				cancel()
			}
			continue
		}
		log.Info("channels: placement superseded; stopping the station", "channel_id", h.id.String(),
			"generation", h.gen, "event", "tv_station_fenced")
		e.mu.Lock()
		if e.stations[h.id] == h.p {
			delete(e.gens, h.id)
		}
		e.mu.Unlock()
		h.p.Stop()
	}
}

// ActiveIDs lists the channels on air on this host.
func (e *Engine) ActiveIDs() []uuid.UUID {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]uuid.UUID, 0, len(e.stations))
	for id, p := range e.stations {
		if p.Alive() {
			out = append(out, id)
		}
	}
	return out
}

// Running reports whether the channel has a live station on this host.
func (e *Engine) Running(id uuid.UUID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.stations[id]
	return ok && p.Alive()
}

// StopAll stops every station (shutdown). Stops run outside the engine
// lock.
func (e *Engine) StopAll() {
	e.mu.Lock()
	ps := make([]Pipeline, 0, len(e.stations))
	for _, p := range e.stations {
		ps = append(ps, p)
	}
	e.mu.Unlock()
	for _, p := range ps {
		p.Stop()
	}
}

// stopWait bounds how long a re-tune waits for the previous station of
// the same channel to finish stopping.
const stopWait = 10 * time.Second

func waitDone(ctx context.Context, p Pipeline, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return errors.New("channel: previous station did not stop in time")
	}
}

// ActiveCount reports the number of live channels on this host.
func (e *Engine) ActiveCount() int { return e.reg.size() }

// HLSDir is the on-disk HLS directory of a channel.
func (e *Engine) HLSDir(channelID uuid.UUID) string {
	return e.Layout.HLSDir(channelSessionID(channelID))
}

// channelSessionID derives the stable session id for a channel's
// pipeline so its HLS dir is deterministic across re-tunes.
func channelSessionID(channelID uuid.UUID) string {
	return "channel-" + channelID.String()
}
