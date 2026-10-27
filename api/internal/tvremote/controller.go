package tvremote

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

// Timings of the push state machine (design §7.3, story 32.7).
const (
	// RequestTimeout: the home's push relay must report the push within this.
	RequestTimeout = 15 * time.Second
	// ConnectTimeout: Cloudflare must report the input live within this (AC5).
	ConnectTimeout = 30 * time.Second
	// StopTimeout: a stopping push is cleaned up after this even without the
	// relay's "stopped".
	StopTimeout = 15 * time.Second
	// RecordingWait: how long cleanup waits for the recording to leave
	// live-inprogress before deleting it anyway (AC8).
	RecordingWait = 120 * time.Second
	// SessionTimeout: a session without a heartbeat for this long ends (32.8 AC6).
	SessionTimeout = 150 * time.Second
	// LivePoll: how often a live push's input and lifecycle are polled (§7.8).
	LivePoll = 15 * time.Second
	// FailCooldown: a channel whose push failed twice is "remote failed" this long.
	FailCooldown = 10 * time.Minute
	// NotLiveGrace: a live push whose input Cloudflare reports not live for this
	// long is retried.
	NotLiveGrace = 2 * time.Minute
	// UsagePoll: how often the month's minutes are read (story 32.10 AC2).
	UsagePoll = time.Hour
	// InputTimeoutSeconds: the inputs' reconnect window (plan D6).
	InputTimeoutSeconds = 30
)

// Controller is the home's remote controller (design §7.1): the input pool,
// the push state machine, sessions, token minting, limits and budget. One
// reconcile loop, under a Postgres advisory lock so only one API acts.
type Controller struct {
	DB          *sql.DB
	Integration *Integration
	Now         func() time.Time
	Logger      *slog.Logger
	// Audit records tunes, ends and key rotations (audit_log); nil: none.
	Audit func(ctx context.Context, action, actor, target string, payload map[string]any)
	// Notifier receives the 80 % / 100 % budget and simulcast-cap notices (the
	// bell; stories 32.10 AC4, 32.13 AC9); nil sends none.
	Notifier notify.Notifier
	// TuneWait is how long a tune waits for its push to go live (default 20 s).
	TuneWait time.Duration
	// PollEvery is the long poll's step (default 250 ms).
	PollEvery time.Duration
	// EventSource returns raw JSON log lines for the admin page's remote
	// events (tests); nil: this API's log ring.
	EventSource func() []json.RawMessage

	once  sync.Once
	st    *store
	tune  sync.Mutex
	wake  chan struct{}
	mu    sync.Mutex
	polls map[string]time.Time // push id -> last lifecycle poll (live pushes)
	notLv map[string]time.Time // push id -> first "not live" seen
	usage time.Time            // last usage poll
	peakT time.Time            // last peak write
	stor  storageCache
	swept bool
	// simulcast (32.13)
	sched    map[string]schedCache // resolved windows per attachment
	holds    map[string]bool       // channel -> its push is held by simulcast
	pubHolds map[string]bool       // channel -> its push is held by the public site (Epic 34)
	pubMu    sync.Mutex            // one public decision at a time (the home's record of them)
	noted    map[string]bool       // notices given once
	outPoll  map[string]time.Time  // output row -> last Cloudflare poll
	outRetry map[string]time.Time  // errored output row -> last attempt
	simUse   time.Time             // last daily simulcast minutes write
	simErr   string                // last simulcast tick error (logged once)
}

type storageCache struct {
	u   cf.StorageUsage
	err error
	at  time.Time
}

func (c *Controller) init() {
	c.once.Do(func() {
		c.st = newStore(c.DB)
		c.wake = make(chan struct{}, 1)
		c.polls = map[string]time.Time{}
		c.notLv = map[string]time.Time{}
		c.sched = map[string]schedCache{}
		c.holds = map[string]bool{}
		c.pubHolds = map[string]bool{}
		c.noted = map[string]bool{}
		c.outPoll = map[string]time.Time{}
		c.outRetry = map[string]time.Time{}
	})
}

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// cleanupFailed logs a Cloudflare or secret cleanup step that did not go
// through (tv_cf_cleanup_failed): an input left enabled or not deleted, or a
// stream key kept, is otherwise found only on the Cloudflare bill.
func (c *Controller) cleanupFailed(step string, err error, attrs ...any) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	args := append([]any{"step", step}, attrs...)
	args = append(args, "err", err.Error(), "event", "tv_cf_cleanup_failed")
	c.log().Warn("tv_remote: a cleanup step failed", args...)
}

func (c *Controller) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *Controller) settings(ctx context.Context) Settings { return LoadSettings(ctx, c.DB) }

func (c *Controller) audit(ctx context.Context, action, actor, target string, payload map[string]any) {
	if c.Audit != nil {
		c.Audit(ctx, action, actor, target, payload)
	}
}

// Wake asks the loop for a reconcile now (a webhook, a tune, an end).
func (c *Controller) Wake() {
	c.init()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run is the reconcile loop: a startup sweep, then a pass every second (the
// per-push cadence is inside Reconcile) or when woken.
func (c *Controller) Run(ctx context.Context) {
	c.init()
	wait, lastErr := time.Second, ""
	for {
		if !c.swept && c.hasPool(ctx) {
			if err := c.Sweep(ctx); err == nil {
				c.swept = true
			}
		}
		err := c.Reconcile(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			// e.g. slot 0125 not migrated yet: back off, log each new error once
			if err.Error() != lastErr {
				c.log().Warn("tv_remote: reconcile failed", "err", err, "event", "tv_remote_reconcile_failed")
				lastErr = err.Error()
			}
			if wait < time.Minute {
				wait *= 2
			}
		default:
			wait, lastErr = time.Second, ""
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-c.wake:
			t.Stop()
		}
	}
}

// hasPool reports whether the startup sweep has anything to do: the
// integration is configured (no Cloudflare call otherwise).
func (c *Controller) hasPool(ctx context.Context) bool {
	if c.Integration == nil {
		return false
	}
	st, err := c.Integration.Status(ctx)
	return err == nil && st.Configured
}

// client is the Cloudflare client (nil and the reason when it cannot be had).
func (c *Controller) client(ctx context.Context) (*cf.Client, string, error) {
	if c.Integration == nil {
		return nil, "", errors.New("no Cloudflare integration")
	}
	return c.Integration.cfClient(ctx)
}

// unavailable is the stored account-level reason ("" when none).
func (c *Controller) unavailable(ctx context.Context) string {
	var v struct {
		Reason string `json:"reason"`
	}
	if c.Integration == nil || c.Integration.getSetting(ctx, SettingUnavailable, &v) != nil {
		return ""
	}
	return v.Reason
}

// Ready reports whether remote viewing can serve a tune, and why not.
func (c *Controller) Ready(ctx context.Context) (bool, string) {
	if c.Integration == nil {
		return false, "Cloudflare Stream is not set up"
	}
	st, err := c.Integration.Status(ctx)
	switch {
	case err != nil:
		return false, "the Cloudflare integration cannot be read"
	case !st.Configured:
		return false, "Cloudflare Stream is not set up"
	case !st.Enabled:
		return false, "remote viewing is switched off"
	case !st.SigningKeySet:
		return false, "no signing key: run Test connection"
	case st.CustomerDomain == nil:
		return false, "no Cloudflare playback domain: run Test connection"
	}
	if r := c.unavailable(ctx); r != "" {
		return false, r
	}
	return true, ""
}

// Reconcile is one pass of the loop: sessions, every push's state machine,
// the pool size, usage and the budget thresholds.
func (c *Controller) Reconcile(ctx context.Context) error {
	c.init()
	ok, release := c.st.tryLock(ctx, "tv_remote")
	if !ok {
		return nil
	}
	defer release()
	now := c.now()
	s := c.settings(ctx)
	if err := c.expireSessions(ctx, now); err != nil {
		return err
	}
	// simulcast first: its holds keep pushes from the idle timeout, and a
	// window opening requests its channel's push (story 32.13)
	if _, err := c.simulcastTick(ctx, nil, s, now); err != nil {
		if err.Error() != c.simErr {
			c.log().Warn("tv_simulcast: reconcile failed", "err", err, "event", "tv_simulcast_error")
			c.simErr = err.Error()
		}
	} else {
		c.simErr = ""
	}
	// the public site (Epic 34): its channels' pushes are held always on
	c.publicTick(ctx, s, now)
	pushes, err := c.st.activePushes(ctx)
	if err != nil {
		return err
	}
	recs, err := c.st.undeletedRecordings(ctx)
	if err != nil {
		return err
	}
	var cl *cf.Client
	var code string
	if len(pushes) > 0 || len(recs) > 0 {
		cl, code, err = c.client(ctx)
		if err != nil {
			c.log().Warn("tv_remote: Cloudflare unreachable for the controller", "err", err, "event", "tv_cf_error")
		}
	}
	sessions, err := c.st.openSessions(ctx)
	if err != nil {
		return err
	}
	viewers := map[string]int{}
	for _, x := range sessions {
		if x.PushID != "" {
			viewers[x.PushID]++
		}
	}
	for _, p := range pushes {
		sim, pub := c.holdOf(p.ChannelID), c.publicHold(p.ChannelID)
		hold := sim || pub
		if Running(p.State) {
			want := []string{}
			if viewers[p.ID] > 0 {
				want = append(want, HoldViewers)
			}
			if sim {
				want = append(want, HoldSimulcast)
			}
			if pub {
				want = append(want, HoldPublic)
			}
			if strings.Join(want, ",") != strings.Join(p.Holds, ",") {
				_ = c.st.setHolds(ctx, p.ID, want)
			}
		}
		if err := c.step(ctx, cl, code, s, p, viewers[p.ID], hold, now); err != nil {
			c.log().Warn("tv_remote: push step failed", "push_id", p.ID, "state", p.State, "err", err,
				"event", "tv_push_error")
		}
	}
	if cl != nil {
		c.deleteRecordings(ctx, cl, now)
	}
	c.shrinkPool(ctx, s)
	c.usageTick(ctx, s, now)
	c.budgetTick(ctx, s, now)
	return nil
}

// expireSessions ends sessions without a heartbeat for SessionTimeout.
func (c *Controller) expireSessions(ctx context.Context, now time.Time) error {
	xs, err := c.st.openSessions(ctx)
	if err != nil {
		return err
	}
	for _, x := range xs {
		if now.Sub(x.LastBeatAt) > SessionTimeout {
			if err := c.st.setSession(ctx, x.ID, map[string]any{"ended_at": now, "end_reason": "timeout"}); err != nil {
				return err
			}
			c.log().Info("tv_remote: session ended without heartbeat", "session_id", x.ID, "user_id", x.UserID,
				"event", "tv_session_timeout")
			c.audit(ctx, "tv_remote.end", x.UserID, x.ID, map[string]any{"channel_id": x.ChannelID, "reason": "timeout"})
		}
	}
	return nil
}

// step advances one push (design §7.3).
func (c *Controller) step(ctx context.Context, cl *cf.Client, code string, s Settings, p Push, viewers int, hold bool, now time.Time) error {
	if Running(p.State) && p.State != PushStopping {
		if host, gen, ok := c.st.placement(ctx, p.ChannelID); ok && (host != p.HostID || gen != p.Generation) {
			// failover, move or swap: the relay follows on its own; the row shows where
			if err := c.st.setPush(ctx, p.ID, map[string]any{"host_id": host, "generation": gen}); err != nil {
				return err
			}
			if p.HostID != "" {
				c.log().Info("tv_remote: push follows its channel to a new host", "push_id", p.ID,
					"channel_id", p.ChannelID, "from", p.HostID, "to", host, "event", "tv_push_follow")
			}
		}
		if strings.HasPrefix(p.RelayDetail, "fatal:") && p.RelayState == "error" {
			return c.fail(ctx, cl, p, strings.TrimSpace(strings.TrimPrefix(p.RelayDetail, "fatal:")), now, false)
		}
	}
	switch p.State {
	case PushRequested:
		if now.Sub(p.StateAt) > RequestTimeout {
			return c.retryOrFail(ctx, cl, s, p, "the home's push relay did not start the push", now, false)
		}
	case PushConnecting:
		if cl == nil {
			return nil
		}
		in, err := c.st.input(ctx, p.InputID)
		if err != nil {
			return err
		}
		if handled, err := c.inputErrors(ctx, cl, s, p, in, now); handled || err != nil {
			return err
		}
		lc, err := cl.Lifecycle(ctx, code, in.CFUID)
		if err == nil && lc.Live && lc.VideoUID != "" {
			if err := c.st.setPush(ctx, p.ID, map[string]any{"state": PushLive, "video_uid": lc.VideoUID,
				"live_at": now, "state_at": now, "detail": ""}); err != nil {
				return err
			}
			_ = c.st.setInput(ctx, in.ID, map[string]any{"state": InputBroadcasting, "last_video_uid": lc.VideoUID})
			c.log().Info("tv_remote: push live", "push_id", p.ID, "channel_id", p.ChannelID, "host_id", p.HostID,
				"video_uid", lc.VideoUID, "after", now.Sub(p.StartedAt).Round(time.Second).String(), "event", "tv_push_live")
			return nil
		}
		if now.Sub(p.StateAt) > ConnectTimeout {
			return c.retryOrFail(ctx, cl, s, p, "Cloudflare did not report the broadcast live", now, false)
		}
	case PushLive:
		return c.stepLive(ctx, cl, code, s, p, viewers, hold, now)
	case PushStopping:
		stopped := p.RelayState == "stopped" && p.RelayAt != nil && !p.RelayAt.Before(p.StateAt)
		if !stopped && now.Sub(p.StateAt) < StopTimeout {
			return nil
		}
		return c.toCleanup(ctx, cl, p, now)
	case PushCleanup:
		return c.stepCleanup(ctx, cl, s, p, now)
	}
	return nil
}

func (c *Controller) stepLive(ctx context.Context, cl *cf.Client, code string, s Settings, p Push, viewers int, hold bool, now time.Time) error {
	// idle: no remote session for idle_timeout (AC8); a simulcast hold keeps
	// the push regardless (32.13 AC4)
	if viewers == 0 && !hold {
		if p.IdleSince == nil {
			return c.st.setPush(ctx, p.ID, map[string]any{"idle_since": now})
		}
		if now.Sub(*p.IdleSince) >= s.IdleTimeout {
			return c.stop(ctx, p, "idle", now)
		}
	} else if p.IdleSince != nil {
		if err := c.st.setPush(ctx, p.ID, map[string]any{"idle_since": nil}); err != nil {
			return err
		}
	}
	// rotation: a broadcast never grows past max_push_hours (AC9)
	since := p.StartedAt
	if p.LiveAt != nil {
		since = *p.LiveAt
	}
	if now.Sub(since) >= s.MaxPush() {
		if viewers > 0 || hold {
			return c.stop(ctx, p, "rotate", now)
		}
		return c.stop(ctx, p, "max_push_hours", now)
	}
	if cl == nil {
		return nil
	}
	c.mu.Lock()
	last := c.polls[p.ID]
	due := now.Sub(last) >= LivePoll
	if due {
		c.polls[p.ID] = now
	}
	c.mu.Unlock()
	if !due {
		return nil
	}
	in, err := c.st.input(ctx, p.InputID)
	if err != nil {
		return err
	}
	if handled, err := c.inputErrors(ctx, cl, s, p, in, now); handled || err != nil {
		return err
	}
	lc, err := cl.Lifecycle(ctx, code, in.CFUID)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	first, seen := c.notLv[p.ID]
	if lc.Live {
		delete(c.notLv, p.ID)
	} else if !seen {
		c.notLv[p.ID] = now
		first = now
	}
	c.mu.Unlock()
	if lc.Live && lc.VideoUID != "" && lc.VideoUID != p.VideoUID {
		// the front door was gone longer than timeoutSeconds: a new video;
		// sessions get its token at their next heartbeat (AC10)
		if err := c.st.setPush(ctx, p.ID, map[string]any{"video_uid": lc.VideoUID}); err != nil {
			return err
		}
		_ = c.st.setInput(ctx, in.ID, map[string]any{"last_video_uid": lc.VideoUID})
		c.log().Warn("tv_remote: Cloudflare started a new video for a running push", "push_id", p.ID,
			"old_video_uid", p.VideoUID, "video_uid", lc.VideoUID, "event", "tv_push_broken")
		return nil
	}
	if !lc.Live && now.Sub(first) >= NotLiveGrace {
		return c.retryOrFail(ctx, cl, s, p, "Cloudflare reports the broadcast down", now, false)
	}
	return nil
}

// stop moves a push to stopping: the relay ends its ffmpeg.
func (c *Controller) stop(ctx context.Context, p Push, reason string, now time.Time) error {
	if err := c.st.setPush(ctx, p.ID, map[string]any{"state": PushStopping, "end_reason": reason, "state_at": now}); err != nil {
		return err
	}
	c.st.notify(ctx, "tv_push", p.ID)
	c.log().Info("tv_remote: push stopping", "push_id", p.ID, "channel_id", p.ChannelID, "host_id", p.HostID, "reason", reason,
		"event", "tv_push_stop")
	return nil
}

// toCleanup disables the input (no reconnects), drops the push's ingest secret
// and waits for the recording (design §7.7).
func (c *Controller) toCleanup(ctx context.Context, cl *cf.Client, p Push, now time.Time) error {
	if cl != nil && p.InputID != "" {
		if in, err := c.st.input(ctx, p.InputID); err == nil {
			if err := cl.SetLiveInputEnabled(ctx, in.CFUID, false); err != nil {
				c.log().Warn("tv_remote: disabling the input failed", "input", in.CFUID, "err", err, "event", "tv_cf_error")
			}
			if in.State != InputErrored {
				_ = c.st.setInput(ctx, in.ID, map[string]any{"state": InputCleanup})
			}
		}
	}
	c.cleanupFailed("delete_push_secret", c.st.deletePushSecret(ctx, p.ID), "push_id", p.ID)
	cols := map[string]any{"state": PushCleanup, "state_at": now}
	if p.EndedAt == nil {
		cols["ended_at"] = now
	}
	return c.st.setPush(ctx, p.ID, cols)
}

// stepCleanup deletes the broadcast's recording once it left live-inprogress
// (or after RecordingWait), frees the input, and starts a rotation's successor.
func (c *Controller) stepCleanup(ctx context.Context, cl *cf.Client, s Settings, p Push, now time.Time) error {
	if p.VideoUID != "" && p.RecordingDeletedAt == nil {
		if cl == nil {
			return nil
		}
		done, err := c.deleteRecording(ctx, cl, p, now)
		if err != nil || !done {
			return err
		}
	}
	if err := c.st.setPush(ctx, p.ID, map[string]any{"state": PushDone, "state_at": now}); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.polls, p.ID)
	delete(c.notLv, p.ID)
	c.mu.Unlock()
	if p.EndReason == "rotate" {
		if succ, err := c.rotateSuccessor(ctx, cl, s, p, now); err != nil {
			c.log().Warn("tv_remote: rotation could not start the next broadcast", "push_id", p.ID, "err", err,
				"event", "tv_push_error")
		} else if succ != "" {
			return nil
		}
	}
	if p.InputID != "" {
		if in, err := c.st.input(ctx, p.InputID); err == nil && in.State != InputErrored {
			// an idle input never keeps an output (32.13 D2)
			if err := c.dropOutputs(ctx, cl, in, "the broadcast ended", false); err != nil {
				return err
			}
			return c.st.setInput(ctx, in.ID, map[string]any{"state": InputIdle, "channel_id": nil, "assigned_at": nil})
		}
	}
	return nil
}

// deleteRecording deletes a push's video when it is no longer in progress (or
// waited long enough); true when it is gone.
func (c *Controller) deleteRecording(ctx context.Context, cl *cf.Client, p Push, now time.Time) (bool, error) {
	var in Input
	if p.InputID != "" {
		in, _ = c.st.input(ctx, p.InputID)
	}
	inProgress, found := false, false
	if in.CFUID != "" {
		if vs, err := cl.InputVideos(ctx, in.CFUID); err == nil {
			for _, v := range vs {
				if v.UID == p.VideoUID {
					found = true
					inProgress = v.Status.State == "live-inprogress"
				}
			}
		} else {
			found = true
		}
	} else {
		found = true
	}
	ended := p.StateAt
	if p.EndedAt != nil {
		ended = *p.EndedAt
	}
	if found && inProgress && now.Sub(ended) < RecordingWait {
		return false, nil
	}
	if found {
		if err := cl.DeleteVideo(ctx, p.VideoUID); err != nil {
			if ae, ok := cf.AsAPIError(err); !ok || ae.Status != 404 {
				return false, err
			}
		}
	}
	if err := c.st.setPush(ctx, p.ID, map[string]any{"recording_deleted_at": now}); err != nil {
		return false, err
	}
	c.log().Info("tv_remote: recording deleted", "push_id", p.ID, "video_uid", p.VideoUID,
		"event", "tv_recording_deleted")
	return true, nil
}

// deleteRecordings retries the recordings of ended pushes still at Cloudflare.
func (c *Controller) deleteRecordings(ctx context.Context, cl *cf.Client, now time.Time) {
	recs, err := c.st.undeletedRecordings(ctx)
	if err != nil {
		return
	}
	for _, p := range recs {
		if p.State == PushCleanup {
			continue // its own step
		}
		if _, err := c.deleteRecording(ctx, cl, p, now); err != nil {
			c.cleanupFailed("delete_recording", err, "push_id", p.ID)
		}
	}
}

// rotateSuccessor starts the next broadcast of a rotated push on the same
// input and moves its open sessions there ("" when nobody watches any more).
func (c *Controller) rotateSuccessor(ctx context.Context, cl *cf.Client, s Settings, p Push, now time.Time) (string, error) {
	xs, err := c.st.querySessions(ctx, `WHERE push_id = $1 AND ended_at IS NULL`, p.ID)
	if err != nil || (len(xs) == 0 && !c.holdOf(p.ChannelID) && !c.publicHold(p.ChannelID)) {
		return "", err
	}
	if cl == nil {
		return "", errors.New("no Cloudflare client")
	}
	in, err := c.st.input(ctx, p.InputID)
	if err != nil {
		return "", err
	}
	np, err := c.startOn(ctx, cl, s, p.ChannelID, in, 1, now)
	if err != nil {
		return "", err
	}
	for _, x := range xs {
		_ = c.st.setSession(ctx, x.ID, map[string]any{"push_id": np.ID})
	}
	c.log().Info("tv_remote: broadcast rotated", "push_id", p.ID, "next_push_id", np.ID,
		"channel_id", p.ChannelID, "event", "tv_push_rotate")
	return np.ID, nil
}

// retryOrFail ends a push that did not work: one retry (on the same input,
// or a fresh one after an input error), then failed and "remote failed" for
// FailCooldown (AC5, AC11).
func (c *Controller) retryOrFail(ctx context.Context, cl *cf.Client, s Settings, p Push, why string, now time.Time, freshInput bool) error {
	if p.Attempt >= 2 || cl == nil {
		return c.fail(ctx, cl, p, why, now, true)
	}
	if err := c.st.setPush(ctx, p.ID, map[string]any{"state": PushFailed, "end_reason": "retried",
		"detail": why, "ended_at": now, "state_at": now}); err != nil {
		return err
	}
	c.cleanupFailed("delete_push_secret", c.st.deletePushSecret(ctx, p.ID), "push_id", p.ID)
	c.st.notify(ctx, "tv_push", p.ID)
	var in Input
	var err error
	if freshInput {
		in, err = c.assignInput(ctx, cl, s, p.InputID)
	} else {
		in, err = c.st.input(ctx, p.InputID)
		if err == nil {
			c.cleanupFailed("disable_input", cl.SetLiveInputEnabled(ctx, in.CFUID, false), "input", in.CFUID)
		}
	}
	if err != nil {
		return c.failSessions(ctx, p, why, now)
	}
	np, err := c.startOn(ctx, cl, s, p.ChannelID, in, p.Attempt+1, now)
	if err != nil {
		return c.failSessions(ctx, p, why, now)
	}
	if _, err := c.st.db.ExecContext(ctx, `UPDATE tv_remote_sessions SET push_id = $1 WHERE push_id = $2 AND ended_at IS NULL`,
		np.ID, p.ID); err != nil {
		return err
	}
	code := ErrCode(why, "push_retried")
	rmetrics().pushError(code)
	c.log().Warn("tv_remote: push retried", "push_id", p.ID, "next_push_id", np.ID, "channel_id", p.ChannelID,
		"host_id", p.HostID, "why", why, "code", code, "event", "tv_push_error")
	return nil
}

// fail marks a push failed (the channel is "remote failed" for FailCooldown).
func (c *Controller) fail(ctx context.Context, cl *cf.Client, p Push, why string, now time.Time, retried bool) error {
	if err := c.st.setPush(ctx, p.ID, map[string]any{"state": PushFailed, "end_reason": "failed", "detail": why,
		"ended_at": now, "state_at": now}); err != nil {
		return err
	}
	c.cleanupFailed("delete_push_secret", c.st.deletePushSecret(ctx, p.ID), "push_id", p.ID)
	c.st.notify(ctx, "tv_push", p.ID)
	if p.InputID != "" {
		if in, err := c.st.input(ctx, p.InputID); err == nil {
			if cl != nil {
				c.cleanupFailed("disable_input", cl.SetLiveInputEnabled(ctx, in.CFUID, false), "input", in.CFUID)
			}
			_ = c.dropOutputs(ctx, cl, in, "the broadcast failed", false)
			if in.State != InputErrored {
				_ = c.st.setInput(ctx, in.ID, map[string]any{"state": InputIdle, "channel_id": nil, "assigned_at": nil})
			}
		}
	}
	code := ErrCode(why, "push_failed")
	rmetrics().pushError(code)
	c.log().Error("tv_remote: push failed", "push_id", p.ID, "channel_id", p.ChannelID, "host_id", p.HostID,
		"why", why, "code", code, "retried", retried, "event", "tv_push_error")
	return c.failSessions(ctx, p, why, now)
}

func (c *Controller) failSessions(ctx context.Context, p Push, _ string, now time.Time) error {
	_, err := c.st.db.ExecContext(ctx, `UPDATE tv_remote_sessions SET ended_at = $1, end_reason = $2
		WHERE push_id = $3 AND ended_at IS NULL`, c.st.t(now), "push_failed", p.ID)
	return err
}

// inputErrors maps an input's Cloudflare error (polled status reason or a
// webhook's ERR_* code) to its action (AC11); true when the push was handled.
func (c *Controller) inputErrors(ctx context.Context, cl *cf.Client, s Settings, p Push, in Input, now time.Time) (bool, error) {
	code := ""
	if in.Error != "" && in.ErrorAt != nil && !in.ErrorAt.Before(p.StartedAt) {
		code = in.Error
	}
	if code == "" {
		if x, _, err := cl.GetLiveInput(ctx, in.CFUID); err == nil {
			st := cf.ParseInputStatus(x.Status)
			if strings.HasPrefix(st.Reason, "ERR_") {
				code = st.Reason
			}
		}
	}
	if code == "" {
		return false, nil
	}
	switch Action(code) {
	case ActionUnavailable:
		reason := "Cloudflare: " + ErrorText(code)
		_ = c.Integration.putSetting(ctx, SettingUnavailable, map[string]any{"reason": reason, "code": code, "at": now})
		c.log().Error("tv_remote: remote viewing unavailable", "code", code, "event", "tv_cf_error")
		return true, c.fail(ctx, cl, p, reason, now, false)
	case ActionFreshInput:
		c.cleanupFailed("disable_input", cl.SetLiveInputEnabled(ctx, in.CFUID, false), "input", in.CFUID)
		_ = c.st.setInput(ctx, in.ID, map[string]any{"state": InputErrored, "error": code, "error_at": now,
			"channel_id": nil})
		c.retireInput(ctx, cl, in)
		return true, c.retryOrFail(ctx, cl, s, p, "Cloudflare: "+ErrorText(code), now, true)
	}
	return false, nil
}

// retireInput deletes an errored input at Cloudflare (a fresh one is made
// when needed); its row stays for the history of its pushes.
func (c *Controller) retireInput(ctx context.Context, cl *cf.Client, in Input) {
	if err := cl.DeleteLiveInput(ctx, in.CFUID); err != nil {
		c.log().Warn("tv_remote: deleting an errored input failed", "input", in.CFUID, "err", err, "event", "tv_cf_error")
	}
	if c.Integration != nil && c.Integration.Secrets != nil {
		c.cleanupFailed("delete_input_keys", c.Integration.Secrets.Delete(ctx, SecretInputPrefix+in.CFUID), "input", in.CFUID)
	}
}

// StopPush asks a push to stop (admin).
func (c *Controller) StopPush(ctx context.Context, id string) error {
	c.init()
	p, err := c.st.push(ctx, id)
	if err != nil {
		return err
	}
	if !Running(p.State) || p.State == PushStopping {
		return nil
	}
	err = c.stop(ctx, p, "admin", c.now())
	c.Wake()
	return err
}

// StopAll stops every running push (the integration is being removed).
func (c *Controller) StopAll(ctx context.Context) error {
	c.init()
	ps, err := c.st.activePushes(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if Running(p.State) && p.State != PushStopping {
			if err := c.stop(ctx, p, "removed", c.now()); err != nil {
				return err
			}
		}
	}
	_, err = c.st.db.ExecContext(ctx, `UPDATE tv_remote_sessions SET ended_at = $1, end_reason = 'removed' WHERE ended_at IS NULL`,
		c.st.t(c.now()))
	// the inputs are deleted at Cloudflare next, with their outputs (32.13 EC5)
	c.closeAllOutputs(ctx, "the Cloudflare integration was removed")
	return err
}

// Forget closes every push that is still open and drops the input rows: the
// integration was removed, so there is no Cloudflare left to finish against,
// and stale rows would count against max_channels when it is added again.
func (c *Controller) Forget(ctx context.Context) error {
	c.init()
	now := c.st.t(c.now())
	if _, err := c.st.db.ExecContext(ctx, `UPDATE tv_remote_pushes SET state = 'done',
	    end_reason = COALESCE(end_reason, 'removed'), ended_at = COALESCE(ended_at, $1), state_at = $1
	    WHERE state NOT IN ('done', 'failed')`, now); err != nil {
		return err
	}
	_, err := c.st.db.ExecContext(ctx, `DELETE FROM tv_remote_inputs`)
	return err
}

// inputKeys is the cf_input.<uid> secret: the ingest endpoints with their keys.
type inputKeys struct {
	RTMPSURL      string `json:"rtmps_url"`
	RTMPSKey      string `json:"rtmps_key"`
	SRTURL        string `json:"srt_url"`
	SRTStreamID   string `json:"srt_stream_id"`
	SRTPassphrase string `json:"srt_passphrase"`
}

func keysOf(x cf.LiveInput) inputKeys {
	var k inputKeys
	if x.RTMPS != nil {
		k.RTMPSURL, k.RTMPSKey = x.RTMPS.URL, x.RTMPS.StreamKey
	}
	if x.SRT != nil {
		k.SRTURL, k.SRTStreamID, k.SRTPassphrase = x.SRT.URL, x.SRT.StreamID, x.SRT.Passphrase
	}
	return k
}

func (c *Controller) saveKeys(ctx context.Context, uid string, k inputKeys) error {
	if c.Integration == nil || c.Integration.Secrets == nil {
		return errors.New("tvremote: no secret store")
	}
	blob, _ := json.Marshal(k)
	return c.Integration.Secrets.Put(ctx, SecretInputPrefix+uid, blob, "tv_remote")
}

// keys opens an input's keys (fetching them from Cloudflare when missing).
func (c *Controller) keys(ctx context.Context, cl *cf.Client, in Input) (inputKeys, error) {
	var k inputKeys
	if c.Integration != nil && c.Integration.Secrets != nil {
		if b, err := c.Integration.Secrets.Get(ctx, SecretInputPrefix+in.CFUID); err == nil && json.Unmarshal(b, &k) == nil && k.RTMPSKey != "" {
			return k, nil
		}
	}
	x, _, err := cl.GetLiveInput(ctx, in.CFUID)
	if err != nil {
		return k, err
	}
	k = keysOf(x)
	if k.RTMPSKey == "" && k.SRTPassphrase == "" {
		return k, errors.New("Cloudflare returned no ingest key for input " + in.CFUID)
	}
	return k, c.saveKeys(ctx, in.CFUID, k)
}

// ErrChannelsFull: the pool has no input left (max_channels).
var ErrChannelsFull = errors.New("tvremote: every remote channel is in use")

// poolSize counts the inputs that count towards max_channels (not errored).
func poolSize(ins []Input) int {
	n := 0
	for _, in := range ins {
		if in.State != InputErrored {
			n++
		}
	}
	return n
}

// assignInput takes an idle input of the pool, or creates one while the pool
// is below max_channels (AC2). exclude is an input that must not be used.
func (c *Controller) assignInput(ctx context.Context, cl *cf.Client, s Settings, exclude string) (Input, error) {
	ins, err := c.st.inputs(ctx)
	if err != nil {
		return Input{}, err
	}
	for _, in := range ins {
		if in.State == InputIdle && in.ID != exclude {
			// never hand another channel an input with an output on it (32.13 D2)
			if err := c.dropOutputs(ctx, cl, in, "the input was reassigned", false); err != nil {
				continue
			}
			return in, nil
		}
	}
	if poolSize(ins) >= s.MaxChannels {
		return Input{}, ErrChannelsFull
	}
	tag := c.st.instanceTag(ctx)
	used := map[string]bool{}
	for _, in := range ins {
		used[in.Name] = true
	}
	name := ""
	for n := 1; ; n++ {
		name = fmt.Sprintf("%s%s-%d", cf.LiveInputPrefix, tag, n)
		if !used[name] {
			break
		}
	}
	off := false
	x, err := cl.CreateLiveInput(ctx, cf.CreateLiveInput{
		Meta: map[string]any{"name": name}, Enabled: &off,
		Recording:        &cf.Recording{Mode: "automatic", RequireSignedURLs: true, TimeoutSeconds: InputTimeoutSeconds},
		PreferLowLatency: s.LowLatency,
	})
	if err != nil {
		return Input{}, err
	}
	k := keysOf(x)
	if err := c.saveKeys(ctx, x.UID, k); err != nil {
		c.cleanupFailed("delete_input", cl.DeleteLiveInput(ctx, x.UID), "input", x.UID)
		return Input{}, err
	}
	in := Input{ID: uuid.NewString(), CFUID: x.UID, Name: name, State: InputIdle,
		Ingest: IngestURLs{RTMPS: k.RTMPSURL, SRT: k.SRTURL}, CreatedAt: c.now()}
	if err := c.st.insertInput(ctx, in); err != nil {
		return Input{}, err
	}
	c.log().Info("tv_remote: live input created", "input", x.UID, "name", name, "event", "tv_input_created")
	return in, nil
}

// startOn starts a push of channel on input: the push row (requested), its
// ingest secret for the relay, the input enabled (AC3).
func (c *Controller) startOn(ctx context.Context, cl *cf.Client, s Settings, channel string, in Input, attempt int, now time.Time) (Push, error) {
	k, err := c.keys(ctx, cl, in)
	if err != nil {
		return Push{}, err
	}
	p := Push{ID: uuid.NewString(), ChannelID: channel, InputID: in.ID, State: PushRequested, Attempt: attempt,
		Ingest: s.Ingest, StartedAt: now, StateAt: now}
	url, key, sid := k.RTMPSURL, k.RTMPSKey, ""
	if s.Ingest == "srt" {
		url, key, sid = k.SRTURL, k.SRTPassphrase, k.SRTStreamID
	}
	if url == "" || key == "" {
		return Push{}, fmt.Errorf("input %s has no %s ingest", in.CFUID, s.Ingest)
	}
	if err := cl.SetLiveInputEnabled(ctx, in.CFUID, true); err != nil {
		return Push{}, err
	}
	if err := c.st.insertPush(ctx, p); err != nil {
		return Push{}, err
	}
	if err := c.st.putPushSecret(ctx, p.ID, url, key, sid, now.Add(s.MaxPush()+time.Hour)); err != nil {
		return Push{}, err
	}
	if host, gen, ok := c.st.placement(ctx, channel); ok {
		_ = c.st.setPush(ctx, p.ID, map[string]any{"host_id": host, "generation": gen})
	}
	if err := c.st.setInput(ctx, in.ID, map[string]any{"state": InputAssigned, "channel_id": channel,
		"assigned_at": now, "error": nil, "error_at": nil}); err != nil {
		return Push{}, err
	}
	c.st.notify(ctx, "tv_push", p.ID)
	c.log().Info("tv_remote: push requested", "push_id", p.ID, "channel_id", channel, "input", in.CFUID,
		"ingest", s.Ingest, "attempt", attempt, "event", "tv_push_start")
	return p, nil
}

// shrinkPool deletes idle inputs above max_channels (AC2, TC1).
func (c *Controller) shrinkPool(ctx context.Context, s Settings) {
	ins, err := c.st.inputs(ctx)
	if err != nil || poolSize(ins) <= s.MaxChannels {
		return
	}
	cl, _, err := c.client(ctx)
	if err != nil {
		return
	}
	extra := poolSize(ins) - s.MaxChannels
	for i := len(ins) - 1; i >= 0 && extra > 0; i-- {
		in := ins[i]
		if in.State != InputIdle {
			continue
		}
		if err := cl.DeleteLiveInput(ctx, in.CFUID); err != nil {
			if ae, ok := cf.AsAPIError(err); !ok || ae.Status != 404 {
				continue
			}
		}
		if c.Integration != nil && c.Integration.Secrets != nil {
			c.cleanupFailed("delete_input_keys", c.Integration.Secrets.Delete(ctx, SecretInputPrefix+in.CFUID), "input", in.CFUID)
		}
		if err := c.st.deleteInput(ctx, in.ID); err == nil {
			extra--
			c.log().Info("tv_remote: idle input deleted (pool shrunk)", "input", in.CFUID, "event", "tv_input_deleted")
		}
	}
}

// Sweep is the startup sweep (AC8): adopt Maktaba's inputs Cloudflare has
// that the pool does not, and delete every recording of a Maktaba input that
// is not the current broadcast of a live push (crash recovery).
func (c *Controller) Sweep(ctx context.Context) error {
	c.init()
	cl, _, err := c.client(ctx)
	if err != nil {
		return err
	}
	ins, err := c.st.inputs(ctx)
	if err != nil {
		return err
	}
	known := map[string]Input{}
	for _, in := range ins {
		known[in.CFUID] = in
	}
	list, err := cl.ListLiveInputs(ctx)
	if err != nil {
		return err
	}
	prefix := cf.LiveInputPrefix + c.st.instanceTag(ctx) + "-"
	for _, x := range list {
		if _, ok := known[x.UID]; ok {
			continue
		}
		if x.Name() != PoolInputName && !strings.HasPrefix(x.Name(), prefix) {
			continue // another home's inputs
		}
		full, _, err := cl.GetLiveInput(ctx, x.UID)
		if err != nil {
			continue
		}
		k := keysOf(full)
		if err := c.saveKeys(ctx, x.UID, k); err != nil {
			continue
		}
		_ = cl.SetLiveInputEnabled(ctx, x.UID, false)
		in := Input{ID: uuid.NewString(), CFUID: x.UID, Name: x.Name(), State: InputIdle,
			Ingest: IngestURLs{RTMPS: k.RTMPSURL, SRT: k.SRTURL}, CreatedAt: c.now()}
		if c.st.insertInput(ctx, in) == nil {
			known[x.UID] = in
			c.log().Info("tv_remote: live input adopted into the pool", "input", x.UID, "name", x.Name(),
				"event", "tv_input_adopted")
		}
	}
	pushes, err := c.st.activePushes(ctx)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	busy := map[string]bool{}
	for _, p := range pushes {
		if p.VideoUID != "" && (p.State == PushLive || p.State == PushConnecting || p.State == PushStopping) {
			keep[p.VideoUID] = true
		}
		if Running(p.State) {
			busy[p.InputID] = true
		}
	}
	deleted := 0
	for _, in := range known {
		if in.State == InputErrored {
			continue
		}
		vs, err := cl.InputVideos(ctx, in.CFUID)
		if err != nil {
			continue
		}
		for _, v := range vs {
			if keep[v.UID] || (busy[in.ID] && v.Status.State == "live-inprogress") {
				continue
			}
			if err := cl.DeleteVideo(ctx, v.UID); err == nil {
				deleted++
				_, _ = c.st.db.ExecContext(ctx, `UPDATE tv_remote_pushes SET recording_deleted_at = $1
					WHERE video_uid = $2 AND recording_deleted_at IS NULL`, c.st.t(c.now()), v.UID)
			}
		}
		if !busy[in.ID] {
			// crash recovery: an input without a running push keeps no output
			_ = c.dropOutputs(ctx, cl, in, "startup sweep", true)
		}
		if !busy[in.ID] && in.State != InputIdle {
			_ = c.st.setInput(ctx, in.ID, map[string]any{"state": InputIdle, "channel_id": nil, "assigned_at": nil})
		}
	}
	if deleted > 0 {
		c.log().Info("tv_remote: startup sweep deleted leftover recordings", "videos", deleted,
			"event", "tv_recording_deleted")
	}
	return nil
}
