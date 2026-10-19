package relay

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// Ctl runs the home's relays: it watches tv_remote_pushes (NOTIFY tv_push and
// a 2 s poll), starts a Session for every push the API's controller wants
// running, stops it when the push leaves requested/connecting/live, and
// reports through tv_push_report. Credentials come from tv_push_credentials,
// which answers only the home's own database user (the front door).
type Ctl struct {
	Pool   *pgxpool.Pool
	Source Source
	FFmpeg string
	// Audio is the channels' output audio (tv_output) at push start.
	Audio  func(ctx context.Context) (Audio, int)
	Runner Runner
	Logger *slog.Logger
	// Poll is the poll period (default 2 s).
	Poll time.Duration

	mu      sync.Mutex
	running map[string]*running
}

type running struct {
	sess   *Session
	cancel context.CancelFunc
	done   chan struct{}
}

func (c *Ctl) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// Available reports whether slot 0125's relay functions exist.
func Available(ctx context.Context, pool *pgxpool.Pool) bool {
	var ok bool
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return pool.QueryRow(qctx, `SELECT to_regproc('tv_push_credentials') IS NOT NULL`).Scan(&ok) == nil && ok
}

// Run syncs until ctx ends, then stops every relay.
func (c *Ctl) Run(ctx context.Context) {
	c.mu.Lock()
	if c.running == nil {
		c.running = map[string]*running{}
	}
	c.mu.Unlock()
	wake := make(chan struct{}, 1)
	go c.listen(ctx, wake)
	poll := c.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		c.Sync(ctx)
		select {
		case <-ctx.Done():
			c.stopAll()
			return
		case <-t.C:
		case <-wake:
		}
	}
}

func (c *Ctl) listen(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		conn, err := c.Pool.Acquire(ctx)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		if _, err := conn.Exec(ctx, `LISTEN tv_push`); err == nil {
			for {
				if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
					break
				}
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
		conn.Release()
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// Running is the push ids with a live relay session.
func (c *Ctl) Running() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.running))
	for id := range c.running {
		out = append(out, id)
	}
	return out
}

// Session is a running push's session (nil when none).
func (c *Ctl) Session(push string) *Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.running[push]; r != nil {
		return r.sess
	}
	return nil
}

// Kick asks every relay of channel to resolve its source again now (a move
// or a swap; Epic 33.8 calls this at the swap).
func (c *Ctl) Kick(ch uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.running {
		if r.sess.Channel == ch {
			r.sess.Kick()
		}
	}
}

// KickAll asks every relay to resolve its source again now (the live TV slot
// changed: story 33.1's failover, 33.7's swap).
func (c *Ctl) KickAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.running {
		r.sess.Kick()
	}
}

// Sync starts and stops relays to match tv_remote_pushes.
func (c *Ctl) Sync(ctx context.Context) {
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	rows, err := c.Pool.Query(qctx, `SELECT id::text, channel_id::text FROM tv_remote_pushes
		WHERE state IN ('requested', 'connecting', 'live')`)
	want := map[string]uuid.UUID{}
	if err == nil {
		for rows.Next() {
			var id, ch string
			if rows.Scan(&id, &ch) == nil {
				if u, err := uuid.Parse(ch); err == nil {
					want[id] = u
				}
			}
		}
		rows.Close()
		err = rows.Err()
	}
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			c.log().Warn("relay: reading the pushes failed", "err", err, "event", "tv_push_error")
		}
		return
	}
	c.mu.Lock()
	var stop []*running
	for id, r := range c.running {
		if _, ok := want[id]; !ok {
			stop = append(stop, r)
			delete(c.running, id)
		}
	}
	var start []string
	for id := range want {
		if _, ok := c.running[id]; !ok {
			start = append(start, id)
		}
	}
	c.mu.Unlock()
	for _, r := range stop {
		r.cancel()
		<-r.done
	}
	for _, id := range start {
		c.start(ctx, id, want[id])
	}
}

func (c *Ctl) report(push, state, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ok bool
	if err := c.Pool.QueryRow(ctx, `SELECT tv_push_report($1::uuid, $2, $3)`, push, state, detail).Scan(&ok); err != nil {
		c.log().Warn("relay: report failed", "push_id", push, "state", state, "err", err, "event", "tv_push_error")
	}
}

func (c *Ctl) start(ctx context.Context, push string, ch uuid.UUID) {
	var cr Credentials
	var sid *string
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := c.Pool.QueryRow(qctx, `SELECT url, key, stream_id, ingest FROM tv_push_credentials($1::uuid)`, push).
		Scan(&cr.URL, &cr.Key, &sid, &cr.Ingest)
	cancel()
	if err != nil {
		if err == pgx.ErrNoRows {
			err = errNoCreds
		}
		c.log().Warn("relay: no credentials for the push", "push_id", push, "err", err, "code", "relay_credentials",
			"event", "tv_push_error")
		tvmetrics.PushErrors.Inc("relay_credentials")
		c.report(push, "error", "no ingest credentials: "+err.Error())
		return
	}
	if sid != nil {
		cr.StreamID = *sid
	}
	var audio Audio
	kbps := 0
	if c.Audio != nil {
		audio, kbps = c.Audio(ctx)
	}
	s := &Session{PushID: push, Channel: ch, Creds: cr, Audio: audio, FFmpeg: c.FFmpeg, Source: c.Source,
		Runner: c.Runner, Logger: c.Logger, KbpsHint: kbps,
		Report: func(state, detail string) { c.report(push, state, detail) }}
	rctx, rcancel := context.WithCancel(context.Background())
	r := &running{sess: s, cancel: rcancel, done: make(chan struct{})}
	c.mu.Lock()
	c.running[push] = r
	c.mu.Unlock()
	c.log().Info("relay: push starting", "push_id", push, "channel_id", ch.String(), "ingest", cr.Ingest,
		"url", Mask(cr.URL, cr.Key), "event", "tv_push_start")
	go func() {
		defer close(r.done)
		err := s.Run(rctx)
		if err != nil {
			c.log().Error("relay: push failed", "push_id", push, "err", Mask(err.Error(), cr.Key), "event", "tv_push_error")
		}
		c.mu.Lock()
		if c.running[push] == r && err != nil {
			delete(c.running, push) // the controller fails it; never restarted here
			c.failed(push)
		}
		c.mu.Unlock()
	}()
}

// failed remembers a push that failed for good, so Sync does not restart it
// before the controller marks it.
func (c *Ctl) failed(push string) {
	c.running[push] = &running{sess: &Session{PushID: push}, cancel: func() {}, done: closed()}
}

func closed() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (c *Ctl) stopAll() {
	c.mu.Lock()
	rs := make([]*running, 0, len(c.running))
	for id, r := range c.running {
		rs = append(rs, r)
		delete(c.running, id)
	}
	c.mu.Unlock()
	for _, r := range rs {
		r.cancel()
		<-r.done
	}
}

type credErr string

func (e credErr) Error() string { return string(e) }

const errNoCreds = credErr("the push has no ingest secret (expired or not running)")
