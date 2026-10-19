package channel

import (
	"context"
	"errors"
	"io"
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
	Repo         Repo
	Layout       Layout
	Start        StartFunc
	Host         string
	WarmGrace    time.Duration
	ReadyTimeout time.Duration

	mu       sync.Mutex
	stations map[uuid.UUID]Pipeline
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
	if err := e.reg.admit(channelID); err != nil {
		return nil, err
	}
	p, err := e.Start(ctx, channelID, e.Layout.HLSDir(channelSessionID(channelID)))
	if err != nil {
		return nil, err
	}
	e.stations[channelID] = p
	// Registry stop runs under the registry lock: it must not block or
	// call back into the engine; Pipeline.Stop is non-blocking.
	e.reg.putWith(channelID, p.Stop, 0)
	started := e.now()
	go func() {
		<-p.Done()
		e.mu.Lock()
		defer e.mu.Unlock()
		// Only the station still registered for the channel clears the
		// runtime row, under the engine lock so it is ordered against a
		// re-tune writing the new station's row.
		if e.stations[channelID] == p {
			delete(e.stations, channelID)
			e.reg.remove(channelID)
			_ = e.Repo.ClearRuntime(context.Background(), channelID)
		}
	}()
	_ = e.Repo.SetRuntime(ctx, Runtime{ChannelID: channelID, Host: e.Host, PID: p.PID(),
		State: StateLive, StartedAt: started, LastSegmentAt: started})
	return p, nil
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

// ReapIdle stops channels idle past the warm grace window.
func (e *Engine) ReapIdle(_ context.Context) []uuid.UUID {
	return e.reg.reapIdle(e.WarmGrace)
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
