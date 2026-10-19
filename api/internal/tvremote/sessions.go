package tvremote

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

// TuneError is a refused tune or heartbeat: an HTTP status and a stable code
// (story 32.8 AC2).
type TuneError struct {
	Status int
	Code   string
	Detail string
	// Hint, for remote_storage_full.
	Hint string
}

func (e *TuneError) Error() string { return e.Code + ": " + e.Detail }

func refuse(status int, code, detail string) *TuneError {
	return &TuneError{Status: status, Code: code, Detail: detail}
}

// Viewer is the signed-in user asking (from the request's principal).
type Viewer struct {
	UserID  string
	Name    string
	IsAdmin bool
	// CanRead reports whether the user may read a channel of this library
	// (nil: a multi-library channel).
	CanRead func(libraryID *string) bool
}

// Tuned is the tune answer: 200 with the URL, or 202 starting.
type Tuned struct {
	SessionID    string       `json:"session_id"`
	State        string       `json:"state"` // live | starting
	HLSURL       string       `json:"hls_url,omitempty"`
	ExpiresAt    *time.Time   `json:"expires_at,omitempty"`
	RefreshAfter *time.Time   `json:"refresh_after,omitempty"`
	RetryAfter   int          `json:"retry_after,omitempty"`
	Channel      *channelInfo `json:"channel,omitempty"`
}

// Beat is the heartbeat answer: a new token only when one is due.
type Beat struct {
	State     string     `json:"state"` // live | starting | ended
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Token     string     `json:"token,omitempty"`
	HLSURL    string     `json:"hls_url,omitempty"`
}

func viewerAllowed(s Settings, v Viewer) bool {
	switch s.Viewers {
	case ViewersAdmins:
		return v.IsAdmin
	case ViewersListed:
		if v.IsAdmin {
			return true
		}
		for _, u := range s.ViewerUsers {
			if u == v.UserID || (v.Name != "" && strings.EqualFold(u, v.Name)) {
				return true
			}
		}
		return false
	}
	return v.UserID != ""
}

func (c *Controller) canRead(ch channelInfo, v Viewer) bool {
	if v.CanRead == nil {
		return v.IsAdmin
	}
	return v.CanRead(ch.LibraryID)
}

// named fills the viewer's username when the viewers rule lists names.
func (c *Controller) named(ctx context.Context, s Settings, v Viewer) Viewer {
	if s.Viewers == ViewersListed && v.Name == "" {
		var name ns
		if c.DB.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, v.UserID).Scan(&name) == nil {
			v.Name = name.S
		}
	}
	return v
}

// Tune opens (or resumes) a remote session for a channel (design §7.5). The
// gates run in this order: remote ready, viewer allowed, channel readable,
// budget (402, new tunes only), the user's streams (429), then for a channel
// not pushing yet: remote failed (503), max_channels (429) and the storage
// guard (503).
func (c *Controller) Tune(ctx context.Context, v Viewer, channelID, device string) (Tuned, error) {
	c.init()
	device = strings.TrimSpace(device)
	if len(device) > 120 {
		device = device[:120]
	}
	if ok, why := c.Ready(ctx); !ok {
		return Tuned{}, refuse(http.StatusServiceUnavailable, "remote_unavailable", why)
	}
	s := c.settings(ctx)
	v = c.named(ctx, s, v)
	if !viewerAllowed(s, v) {
		return Tuned{}, refuse(http.StatusForbidden, "remote_not_allowed", "you may not watch away from home")
	}
	if !isUUID(channelID) {
		return Tuned{}, refuse(http.StatusNotFound, "no_such_channel", "no such channel")
	}
	ch, err := c.st.channel(ctx, channelID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (!ch.enabled || !c.canRead(ch, v))) {
		return Tuned{}, refuse(http.StatusNotFound, "no_such_channel", "no such channel")
	}
	if err != nil {
		return Tuned{}, err
	}

	sessID, pushID, err := c.openSession(ctx, s, v, ch, device)
	if err != nil {
		return Tuned{}, err
	}
	c.Wake()
	out := Tuned{SessionID: sessID, State: "starting", RetryAfter: 3, Channel: &ch}
	if pushID == "" {
		return out, nil
	}
	p, ok := c.waitLive(ctx, pushID)
	if !ok {
		return out, nil
	}
	tok, exp, kid, url, err := c.mint(ctx, s, p.VideoUID)
	if err != nil {
		return Tuned{}, err
	}
	if err := c.st.setSession(ctx, sessID, map[string]any{"token_exp": exp, "token_kid": kid, "video_uid": p.VideoUID}); err != nil {
		return Tuned{}, err
	}
	_ = tok
	ra := exp.Add(-s.TokenTTL / 10)
	return Tuned{SessionID: sessID, State: "live", HLSURL: url, ExpiresAt: &exp, RefreshAfter: &ra, Channel: &ch}, nil
}

// openSession runs the limit gates and the push start under the tune lock;
// it returns the session and the push it waits on ("" while a stopping push
// still holds the channel: the client retries).
func (c *Controller) openSession(ctx context.Context, s Settings, v Viewer, ch channelInfo, device string) (string, string, error) {
	c.tune.Lock()
	defer c.tune.Unlock()
	unlock, err := c.st.lock(ctx, "tv_remote_tune")
	if err != nil {
		return "", "", err
	}
	defer unlock()
	now := c.now()

	open, err := c.st.openSessions(ctx)
	if err != nil {
		return "", "", err
	}
	var mine *Session
	userOpen := 0
	for i := range open {
		if open[i].UserID != v.UserID {
			continue
		}
		userOpen++
		if open[i].ChannelID == ch.ID && open[i].Device == device {
			mine = &open[i]
		}
	}
	if mine == nil {
		// a new tune: the budget (README Q8) and the user's streams
		e, err := c.Estimate(ctx, s, now)
		if err != nil {
			return "", "", err
		}
		if e.Exhausted {
			return "", "", refuse(http.StatusPaymentRequired, "budget_exhausted",
				fmt.Sprintf("this month's remote viewing budget ($%.2f) is used up", s.MonthlyBudgetUSD))
		}
		if userOpen >= s.MaxStreamsPerUser {
			return "", "", refuse(http.StatusTooManyRequests, "user_streams_full",
				fmt.Sprintf("you already watch %d channels away from home", userOpen))
		}
	}

	p, running, err := c.st.runningPushFor(ctx, ch.ID)
	if err != nil {
		return "", "", err
	}
	pushID := p.ID
	if running && p.State == PushStopping {
		pushID = "" // the stopping push must end before the channel can start again
	}
	if !running {
		pushID, err = c.newPush(ctx, s, ch.ID, now)
		if err != nil {
			return "", "", err
		}
	}
	if mine != nil {
		if mine.PushID != pushID && pushID != "" {
			if err := c.st.setSession(ctx, mine.ID, map[string]any{"push_id": pushID, "last_beat_at": now}); err != nil {
				return "", "", err
			}
		} else {
			_ = c.st.setSession(ctx, mine.ID, map[string]any{"last_beat_at": now})
		}
		return mine.ID, pushID, nil
	}
	x := Session{ID: uuid.NewString(), UserID: v.UserID, ChannelID: ch.ID, PushID: pushID, Device: device,
		StartedAt: now, LastBeatAt: now}
	if err := c.st.insertSession(ctx, x); err != nil {
		return "", "", err
	}
	c.log().Info("tv_remote: remote tune", "session_id", x.ID, "user_id", v.UserID, "channel_id", ch.ID,
		"device", device, "push_id", pushID, "event", "tv_remote_tune")
	c.audit(ctx, "tv_remote.tune", v.UserID, x.ID, map[string]any{"channel_id": ch.ID, "device": device, "push_id": pushID})
	return x.ID, pushID, nil
}

// newPush starts a push for a channel that has none (the channel gates).
func (c *Controller) newPush(ctx context.Context, s Settings, channel string, now time.Time) (string, error) {
	if n, err := c.st.recentFailures(ctx, channel, now.Add(-FailCooldown)); err != nil {
		return "", err
	} else if n > 0 {
		return "", refuse(http.StatusServiceUnavailable, "remote_failed",
			"this channel could not be sent to Cloudflare; try again in a few minutes")
	}
	pushes, err := c.st.activePushes(ctx)
	if err != nil {
		return "", err
	}
	running := 0
	for _, p := range pushes {
		if Running(p.State) {
			running++
		}
	}
	if running >= s.MaxChannels {
		return "", refuse(http.StatusTooManyRequests, "remote_channels_full",
			fmt.Sprintf("all %d remote channels are in use", s.MaxChannels))
	}
	cl, _, err := c.client(ctx)
	if err != nil {
		return "", refuse(http.StatusServiceUnavailable, "remote_unavailable", err.Error())
	}
	g, err := c.Storage(ctx, cl, s, now)
	if err != nil {
		return "", err
	}
	if !g.Fits {
		e := refuse(http.StatusServiceUnavailable, "remote_storage_full",
			fmt.Sprintf("Cloudflare storage has room for no more broadcasts (%.0f of %.0f minutes reserved, %.0f more needed)",
				g.CapacityMinutes-g.FreeMinutes, g.CapacityMinutes, g.NeedMinutes+g.MarginMinutes))
		e.Hint = g.Hint
		c.log().Warn("tv_remote: storage guard refused a new push", "channel_id", channel,
			"capacity", g.CapacityMinutes, "free", g.FreeMinutes, "need", g.NeedMinutes, "event", "tv_storage_full")
		return "", e
	}
	in, err := c.assignInput(ctx, cl, s, "")
	if errors.Is(err, ErrChannelsFull) {
		return "", refuse(http.StatusTooManyRequests, "remote_channels_full",
			fmt.Sprintf("all %d remote channels are in use", s.MaxChannels))
	}
	if err != nil {
		return "", refuse(http.StatusServiceUnavailable, "remote_unavailable", "Cloudflare: "+err.Error())
	}
	p, err := c.startOn(ctx, cl, s, channel, in, 1, now)
	if errors.Is(err, errDuplicate) {
		if q, ok, _ := c.st.runningPushFor(ctx, channel); ok {
			return q.ID, nil
		}
	}
	if err != nil {
		return "", refuse(http.StatusServiceUnavailable, "remote_unavailable", "Cloudflare: "+err.Error())
	}
	return p.ID, nil
}

// waitLive long-polls a push until it is live (TuneWait, default 20 s).
func (c *Controller) waitLive(ctx context.Context, pushID string) (Push, bool) {
	wait := c.TuneWait
	if wait == 0 {
		wait = 20 * time.Second
	}
	step := c.PollEvery
	if step <= 0 {
		step = 250 * time.Millisecond
	}
	deadline := time.Now().Add(wait)
	for {
		p, err := c.st.push(ctx, pushID)
		if err == nil && p.State == PushLive && p.VideoUID != "" {
			return p, true
		}
		if err != nil || !Running(p.State) || wait < 0 || !time.Now().Before(deadline) {
			return p, false
		}
		select {
		case <-ctx.Done():
			return p, false
		case <-time.After(step):
		}
	}
}

// mint signs a playback token for a video and builds its HLS URL (design §7.6).
func (c *Controller) mint(ctx context.Context, s Settings, videoUID string) (string, time.Time, string, string, error) {
	key, err := c.Integration.SigningKey(ctx)
	if err != nil {
		return "", time.Time{}, "", "", fmt.Errorf("signing key: %w", err)
	}
	cl, code, err := c.client(ctx)
	if err != nil {
		return "", time.Time{}, "", "", err
	}
	ttl := s.TokenTTL
	if ttl <= 0 || ttl > cf.MaxTokenTTL {
		ttl = cf.MaxTokenTTL
	}
	now := c.now()
	var rules []cf.AccessRule
	if len(s.AllowedCountries) > 0 {
		rules = []cf.AccessRule{{Type: "ip.geoip.country", Action: "allow", Country: s.AllowedCountries},
			{Type: "any", Action: "block"}}
	}
	tok, err := cf.SignToken(key, videoUID, ttl, now, rules)
	if err != nil {
		return "", time.Time{}, "", "", err
	}
	exp := time.Unix(now.Add(ttl).Unix(), 0).UTC()
	return tok, exp, key.ID, cl.ManifestURL(code, tok), nil
}

// Heartbeat keeps a session (every 60 s, owner only, while the tune's gates
// still let them watch) and returns a fresh token past half its life, after
// a new video, or after a key rotation.
func (c *Controller) Heartbeat(ctx context.Context, v Viewer, sessionID string) (Beat, error) {
	c.init()
	x, err := c.st.session(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && x.UserID != v.UserID) {
		return Beat{}, refuse(http.StatusNotFound, "no_such_session", "no such session")
	}
	if err != nil {
		return Beat{}, err
	}
	if x.EndedAt != nil {
		return Beat{State: "ended"}, refuse(http.StatusGone, "session_ended", "the session ended: tune again")
	}
	now := c.now()
	s := c.settings(ctx)
	// the tune's gates hold for the whole session: a viewer taken off the
	// viewers rule, or a channel switched off, gets no fresh token
	ch, err := c.st.channel(ctx, x.ChannelID)
	if err != nil || !ch.enabled || !c.canRead(ch, v) || !viewerAllowed(s, c.named(ctx, s, v)) {
		_ = c.st.setSession(ctx, x.ID, map[string]any{"ended_at": now, "end_reason": "access_lost"})
		c.Wake()
		return Beat{State: "ended"}, refuse(http.StatusForbidden, "remote_not_allowed", "you may no longer watch this channel")
	}
	if err := c.st.setSession(ctx, x.ID, map[string]any{"last_beat_at": now}); err != nil {
		return Beat{}, err
	}
	b := Beat{State: "starting", ExpiresAt: x.TokenExp}
	if x.PushID == "" {
		// the channel was stopping when this session tuned: pick up its new push
		if p, ok, _ := c.st.runningPushFor(ctx, x.ChannelID); ok && p.State != PushStopping {
			x.PushID = p.ID
			_ = c.st.setSession(ctx, x.ID, map[string]any{"push_id": p.ID})
		} else {
			return b, nil
		}
	}
	p, err := c.st.push(ctx, x.PushID)
	if err != nil {
		return Beat{}, err
	}
	if !Running(p.State) {
		if p.State == PushCleanup && p.EndReason == "rotate" {
			return b, nil // the next broadcast starts once this recording is gone
		}
		return Beat{State: "ended"}, refuse(http.StatusGone, "session_ended", "the broadcast ended: tune again")
	}
	if p.State != PushLive || p.VideoUID == "" {
		return b, nil
	}
	b.State = "live"
	due := x.TokenExp == nil || x.VideoUID != p.VideoUID || !now.Before(x.TokenExp.Add(-s.TokenTTL/2))
	if !due {
		if k, err := c.Integration.SigningKey(ctx); err == nil && k.ID != x.TokenKID {
			due = true // the key was rotated
		}
	}
	if !due {
		return b, nil
	}
	tok, exp, kid, url, err := c.mint(ctx, s, p.VideoUID)
	if err != nil {
		return Beat{}, err
	}
	if err := c.st.setSession(ctx, x.ID, map[string]any{"token_exp": exp, "token_kid": kid, "video_uid": p.VideoUID}); err != nil {
		return Beat{}, err
	}
	b.Token, b.HLSURL, b.ExpiresAt = tok, url, &exp
	return b, nil
}

// End ends a session (owner only).
func (c *Controller) End(ctx context.Context, v Viewer, sessionID string) error {
	c.init()
	x, err := c.st.session(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && x.UserID != v.UserID && !v.IsAdmin) {
		return refuse(http.StatusNotFound, "no_such_session", "no such session")
	}
	if err != nil {
		return err
	}
	if x.EndedAt != nil {
		return nil
	}
	if err := c.st.setSession(ctx, x.ID, map[string]any{"ended_at": c.now(), "end_reason": "ended"}); err != nil {
		return err
	}
	c.log().Info("tv_remote: remote session ended", "session_id", x.ID, "user_id", x.UserID, "event", "tv_remote_end")
	c.audit(ctx, "tv_remote.end", v.UserID, x.ID, map[string]any{"channel_id": x.ChannelID, "reason": "ended"})
	c.Wake()
	return nil
}

// InputEvent records a Cloudflare Notifications event for an input (the
// optional webhook, story 32.7 AC13): an errored event's ERR_* code is kept
// on the input for the admin page and the next reconcile; nothing else
// changes until the poller sees it.
func (c *Controller) InputEvent(ctx context.Context, inputUID, event, code string) error {
	c.init()
	if code != "" && strings.HasPrefix(code, "ERR_") {
		if _, err := c.st.db.ExecContext(ctx, `UPDATE tv_remote_inputs SET error = $1, error_at = $2 WHERE cf_uid = $3`,
			code, c.st.t(c.now()), inputUID); err != nil {
			return err
		}
	}
	c.log().Info("tv_remote: Cloudflare event", "input", inputUID, "type", event, "code", code, "event", "tv_cf_webhook")
	c.Wake()
	return nil
}

// RotateResult is what rotating the signing key did.
type RotateResult struct {
	KeyID      string `json:"key_id"`
	OldKeyID   string `json:"old_key_id,omitempty"`
	OldDeleted bool   `json:"old_deleted"`
	Error      string `json:"error,omitempty"`
}

// RotateSigningKey makes a new signing key, signs with it from now on and
// deletes the old one at Cloudflare, which invalidates every outstanding
// token (story 32.8 AC7, R8.4). Sessions recover at their next heartbeat.
func (c *Controller) RotateSigningKey(ctx context.Context, actor string) (RotateResult, error) {
	c.init()
	var res RotateResult
	cl, _, err := c.client(ctx)
	if err != nil {
		return res, err
	}
	in := c.Integration
	old, oldErr := in.SigningKey(ctx)
	nk, err := cl.CreateSigningKey(ctx)
	if err != nil {
		return res, err
	}
	if oldErr == nil {
		prev, _ := json.Marshal(storedKey{ID: old.ID, PEM: old.PEM})
		if err := in.Secrets.Put(ctx, SecretSigningKey+"_prev", prev, actor); err != nil {
			return res, err
		}
		res.OldKeyID = old.ID
	}
	blob, _ := json.Marshal(storedKey{ID: nk.ID, PEM: nk.PEM})
	if err := in.Secrets.Put(ctx, SecretSigningKey, blob, actor); err != nil {
		return res, err
	}
	res.KeyID = nk.ID
	if oldErr == nil {
		if err := cl.DeleteSigningKey(ctx, old.ID); err != nil {
			res.Error = "the old key could not be deleted at Cloudflare (its tokens still play until they expire): " + err.Error()
		} else {
			res.OldDeleted = true
			_ = in.Secrets.Delete(ctx, SecretSigningKey+"_prev")
		}
	}
	c.log().Warn("tv_remote: signing key rotated: every remote token is invalid", "key_id", nk.ID,
		"old_key_id", res.OldKeyID, "old_deleted", res.OldDeleted, "event", "tv_signing_key_rotated")
	c.audit(ctx, "tv_remote.key_rotated", actor, nk.ID, map[string]any{"old_key_id": res.OldKeyID, "old_deleted": res.OldDeleted})
	return res, nil
}
