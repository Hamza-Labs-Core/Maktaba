package tvremote

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

// Simulcast (story 32.13, design §7.9): Cloudflare retransmits what a live
// input receives to its live outputs, so a channel simulcast to YouTube,
// Facebook or any RTMP/RTMPS destination is still uploaded once, by the
// channel's one push (32.7). The controller:
//
//   - resolves each attachment (channel × destination, always or on a
//     schedule) into "wanted now" or not, under the cap and the budget;
//   - holds the channel's push (hold "simulcast") while any of its outputs is
//     wanted, starting a push for the channel when there is none (as a tune
//     would, minus the per-user gates); the idle timeout ignores held pushes;
//   - creates the output on the push's input when the push is live (the key
//     from home_secrets) and deletes it when the window closes. An input that
//     goes idle never keeps an output (D2): a pooled input reassigned to
//     another channel cannot reach a destination.
//
// Outputs belong to the input, so whatever keeps the input keeps them: a
// failover or move (the push relay switches its source, the push and input
// stay), a relay crash (same input, same broadcast) and a rotation at
// max_push_hours (a new push on the same input). An input swap (a retry on a
// fresh input after an ERR_* error) re-creates the outputs on the new input;
// the old input is deleted at Cloudflare with its outputs.

// Attachment modes.
const (
	ModeAlways   = "always"
	ModeSchedule = "schedule"
)

// Destination kinds.
const (
	KindYouTube  = "youtube"
	KindFacebook = "facebook"
	KindRTMP     = "rtmp"
)

// DefaultIngest is the documented ingest URL of a platform (prefilled,
// editable).
var DefaultIngest = map[string]string{
	KindYouTube:  "rtmp://a.rtmp.youtube.com/live2",
	KindFacebook: "rtmps://live-api-s.facebook.com:443/rtmp/",
}

// Output states (AC7).
const (
	OutScheduled = "scheduled"
	OutStarting  = "starting"
	OutLive      = "live"
	OutStopping  = "stopping"
	OutOff       = "off"
	OutErrored   = "errored"
)

// SecretSimulcastKey is a destination's stream key in home_secrets.
func SecretSimulcastKey(destination string) string { return "simulcast_" + destination + "_key" }

// SettingSimulcastNotice records the highest cap threshold announced this
// month ({month, level}).
const SettingSimulcastNotice = "tv_simulcast.cap_notice"

// OutputRetry is how long an errored output waits before it is tried again.
const OutputRetry = time.Minute

// Detail texts of an output waiting or ended (the admin page translates the
// codes; the text is the English fallback).
const (
	ReasonNoInput      = "no free input: every remote channel is in use"
	ReasonCap          = "this month's simulcast hours are used up"
	ReasonBudget       = "this month's remote budget is used up"
	ReasonNotReady     = "Cloudflare Stream is not ready (run Test connection)"
	ReasonOff          = "simulcast is switched off"
	ReasonPlatform     = "sent; the destination's state is shown on the platform"
	ReasonReconnecting = "the broadcast is reconnecting; the output stays on the input"
)

func maskKey(k string) string {
	if len(k) <= 8 {
		return "••••"
	}
	return "••••" + k[len(k)-4:]
}

// checkIngest validates an ingest URL: rtmp:// or rtmps:// with a host.
func checkIngest(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "rtmp" && u.Scheme != "rtmps") || u.Host == "" || u.User != nil || u.Hostname() == "" {
		return "", errors.New("the ingest URL must be rtmp:// or rtmps:// with a host (no user or password)")
	}
	if len(raw) > 1000 {
		return "", errors.New("the ingest URL is too long")
	}
	return raw, nil
}

func checkKey(k string) (string, error) {
	k = strings.TrimSpace(k)
	if k == "" {
		return "", errors.New("the stream key is required")
	}
	if len(k) > 512 {
		return "", errors.New("the stream key is too long")
	}
	for _, r := range k {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("the stream key cannot hold spaces")
		}
	}
	return k, nil
}

func checkName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || len([]rune(n)) > 80 {
		return "", errors.New("the name is required (at most 80 characters)")
	}
	return n, nil
}

func badRequest(code, detail string) *TuneError { return refuse(http.StatusBadRequest, code, detail) }

// DestinationInput is a destination to create or change (nil: unchanged).
type DestinationInput struct {
	Name      *string `json:"name"`
	Kind      *string `json:"kind"`
	IngestURL *string `json:"ingest_url"`
	StreamKey *string `json:"stream_key"`
}

func (c *Controller) secretStore() error {
	if c.Integration == nil || c.Integration.Secrets == nil {
		return errors.New("tvremote: no secret store")
	}
	return nil
}

// destView fills a destination's answer: key set and masked, in use.
func (c *Controller) destView(ctx context.Context, d Destination, open []SimOutput) Destination {
	if c.Integration != nil && c.Integration.Secrets != nil {
		if b, err := c.Integration.Secrets.Get(ctx, d.KeySecret); err == nil && len(b) > 0 {
			d.KeySet, d.KeyMasked = true, maskKey(string(b))
		}
	}
	for _, o := range open {
		if o.DestinationID == d.ID && o.State != OutScheduled {
			d.InUse = true
		}
	}
	return d
}

// CreateDestination adds a destination; its key goes to home_secrets.
func (c *Controller) CreateDestination(ctx context.Context, actor string, in DestinationInput) (Destination, error) {
	c.init()
	if err := c.secretStore(); err != nil {
		return Destination{}, err
	}
	var d Destination
	var err error
	if in.Name == nil || in.Kind == nil || in.StreamKey == nil {
		return d, badRequest("invalid_destination", "send name, kind, ingest_url and stream_key")
	}
	if d.Name, err = checkName(*in.Name); err != nil {
		return d, badRequest("invalid_destination", err.Error())
	}
	d.Kind = strings.TrimSpace(*in.Kind)
	if d.Kind != KindYouTube && d.Kind != KindFacebook && d.Kind != KindRTMP {
		return d, badRequest("invalid_destination", "kind must be youtube, facebook or rtmp")
	}
	ingest := DefaultIngest[d.Kind]
	if in.IngestURL != nil && strings.TrimSpace(*in.IngestURL) != "" {
		ingest = *in.IngestURL
	}
	if d.IngestURL, err = checkIngest(ingest); err != nil {
		return d, badRequest("invalid_destination", err.Error())
	}
	key, err := checkKey(*in.StreamKey)
	if err != nil {
		return d, badRequest("invalid_destination", err.Error())
	}
	now := c.now()
	d.ID = uuid.NewString()
	d.KeySecret = SecretSimulcastKey(d.ID)
	d.CreatedAt, d.UpdatedAt = now, now
	if err := c.Integration.Secrets.Put(ctx, d.KeySecret, []byte(key), actor); err != nil {
		return Destination{}, err
	}
	if err := c.st.insertDestination(ctx, d); err != nil {
		_ = c.Integration.Secrets.Delete(ctx, d.KeySecret)
		return Destination{}, err
	}
	c.log().Info("tv_simulcast: destination added", "destination_id", d.ID, "kind", d.Kind, "event", "tv_simulcast_destination")
	c.audit(ctx, "tv_simulcast.destination_added", actor, d.ID, map[string]any{"name": d.Name, "kind": d.Kind})
	return c.destView(ctx, d, nil), nil
}

// UpdateDestination changes a destination's name, ingest URL or key. A
// running output keeps the old key until its next start.
func (c *Controller) UpdateDestination(ctx context.Context, actor, id string, in DestinationInput) (Destination, error) {
	c.init()
	if err := c.secretStore(); err != nil {
		return Destination{}, err
	}
	d, err := c.st.destination(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return d, refuse(http.StatusNotFound, "no_such_destination", "no such destination")
	}
	if err != nil {
		return d, err
	}
	cols := map[string]any{}
	if in.Kind != nil && strings.TrimSpace(*in.Kind) != d.Kind {
		return d, badRequest("invalid_destination", "the kind of a destination cannot change: add a new one")
	}
	if in.Name != nil {
		n, err := checkName(*in.Name)
		if err != nil {
			return d, badRequest("invalid_destination", err.Error())
		}
		cols["name"], d.Name = n, n
	}
	if in.IngestURL != nil {
		u, err := checkIngest(*in.IngestURL)
		if err != nil {
			return d, badRequest("invalid_destination", err.Error())
		}
		cols["ingest_url"], d.IngestURL = u, u
	}
	if in.StreamKey != nil {
		k, err := checkKey(*in.StreamKey)
		if err != nil {
			return d, badRequest("invalid_destination", err.Error())
		}
		if err := c.Integration.Secrets.Put(ctx, d.KeySecret, []byte(k), actor); err != nil {
			return d, err
		}
	}
	now := c.now()
	cols["updated_at"] = now
	if err := c.st.set(ctx, "tv_simulcast_destinations", id, cols); err != nil {
		return d, err
	}
	d.UpdatedAt = now
	c.audit(ctx, "tv_simulcast.destination_changed", actor, id, map[string]any{"key_changed": in.StreamKey != nil})
	open, _ := c.st.openOutputs(ctx)
	return c.destView(ctx, d, open), nil
}

// DeleteDestination deletes a destination, its attachments and its key;
// refused while a channel simulcasts to it.
func (c *Controller) DeleteDestination(ctx context.Context, actor, id string) error {
	c.init()
	d, err := c.st.destination(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return refuse(http.StatusNotFound, "no_such_destination", "no such destination")
	}
	if err != nil {
		return err
	}
	open, err := c.st.openOutputs(ctx)
	if err != nil {
		return err
	}
	for _, o := range open {
		if o.DestinationID == id {
			return refuse(http.StatusConflict, "destination_in_use",
				"a channel is simulcasting to this destination: stop it or detach it first")
		}
	}
	if err := c.st.deleteDestination(ctx, id); err != nil {
		return err
	}
	if c.Integration != nil && c.Integration.Secrets != nil {
		_ = c.Integration.Secrets.Delete(ctx, d.KeySecret)
	}
	c.audit(ctx, "tv_simulcast.destination_deleted", actor, id, map[string]any{"name": d.Name})
	c.forget("")
	return nil
}

// DestinationTest is "Test": the URL parses and the key is set (Cloudflare
// and the platform are not called; the platform shows the rest).
type DestinationTest struct {
	OK     bool   `json:"ok"`
	URLOK  bool   `json:"url_ok"`
	KeySet bool   `json:"key_set"`
	Detail string `json:"detail"`
}

// TestDestination checks a destination.
func (c *Controller) TestDestination(ctx context.Context, id string) (DestinationTest, error) {
	c.init()
	d, err := c.st.destination(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return DestinationTest{}, refuse(http.StatusNotFound, "no_such_destination", "no such destination")
	}
	if err != nil {
		return DestinationTest{}, err
	}
	var t DestinationTest
	_, uerr := checkIngest(d.IngestURL)
	t.URLOK = uerr == nil
	d = c.destView(ctx, d, nil)
	t.KeySet = d.KeySet
	t.OK = t.URLOK && t.KeySet
	switch {
	case !t.URLOK:
		t.Detail = uerr.Error()
	case !t.KeySet:
		t.Detail = "the stream key is not set (or cannot be opened): enter it again"
	default:
		t.Detail = "the URL parses and the key is set; whether the platform accepts it shows once an output runs"
	}
	return t, nil
}

// AttachmentInput is one attachment of PUT /api/channels/{id}/simulcast.
type AttachmentInput struct {
	DestinationID   string          `json:"destination_id"`
	Mode            string          `json:"mode"`
	Rules           json.RawMessage `json:"rules"`
	Enabled         bool            `json:"enabled"`
	RightsConfirmed bool            `json:"rights_confirmed"`
}

// SetChannelSimulcast replaces a channel's attachments. Turning one on
// needs the rights confirmation (AC3): 409 rights_confirmation_required.
func (c *Controller) SetChannelSimulcast(ctx context.Context, actor, channel string, in []AttachmentInput) ([]Attachment, error) {
	c.init()
	if _, err := uuid.Parse(channel); err != nil {
		return nil, refuse(http.StatusNotFound, "no_such_channel", "no such channel")
	}
	if _, err := c.st.channel(ctx, channel); errors.Is(err, sql.ErrNoRows) {
		return nil, refuse(http.StatusNotFound, "no_such_channel", "no such channel")
	} else if err != nil {
		return nil, err
	}
	ss := LoadSimulcastSettings(ctx, c.DB)
	if len(in) > ss.MaxOutputsPerChannel {
		return nil, refuse(http.StatusConflict, "too_many_outputs",
			fmt.Sprintf("at most %d destinations per channel (tv_simulcast.max_outputs_per_channel)", ss.MaxOutputsPerChannel))
	}
	cur, err := c.st.attachments(ctx, channel)
	if err != nil {
		return nil, err
	}
	byDest := map[string]Attachment{}
	for _, a := range cur {
		byDest[a.DestinationID] = a
	}
	now := c.now()
	seen := map[string]bool{}
	var next []Attachment
	for i, x := range in {
		if seen[x.DestinationID] {
			return nil, badRequest("invalid_attachment", fmt.Sprintf("destination %s is listed twice", x.DestinationID))
		}
		seen[x.DestinationID] = true
		if _, err := c.st.destination(ctx, x.DestinationID); errors.Is(err, sql.ErrNoRows) {
			return nil, refuse(http.StatusNotFound, "no_such_destination", "no such destination: "+x.DestinationID)
		} else if err != nil {
			return nil, err
		}
		if x.Mode != ModeAlways && x.Mode != ModeSchedule {
			return nil, badRequest("invalid_attachment", fmt.Sprintf("attachment %d: mode must be always or schedule", i+1))
		}
		rules, err := ParseRules(x.Rules, x.Mode)
		if err != nil {
			return nil, badRequest("invalid_rules", fmt.Sprintf("attachment %d: %v", i+1, err))
		}
		a, had := byDest[x.DestinationID]
		if !had {
			a = Attachment{ID: uuid.NewString(), ChannelID: channel, DestinationID: x.DestinationID, CreatedAt: now}
		}
		turningOn := x.Enabled && (!had || !a.Enabled)
		if turningOn && !x.RightsConfirmed {
			return nil, refuse(http.StatusConflict, "rights_confirmation_required",
				"confirm that you hold the rights to everything this channel airs while it is simulcast "+
					"(YouTube and Facebook match live content against copyright claims)")
		}
		if turningOn {
			t := now
			a.RightsConfirmedBy, a.RightsConfirmedAt = actor, &t
		}
		if a.Mode != x.Mode {
			a.Manual, a.ManualUntil = "", nil
		}
		a.Mode, a.Rules, a.Enabled, a.UpdatedAt = x.Mode, rules, x.Enabled, now
		next = append(next, a)
	}
	for _, a := range next {
		if err := c.st.upsertAttachment(ctx, a); err != nil {
			return nil, err
		}
		if a.RightsConfirmedAt != nil && a.RightsConfirmedAt.Equal(now) {
			c.log().Info("tv_simulcast: rights confirmed", "channel_id", channel, "destination_id", a.DestinationID,
				"by", actor, "event", "tv_simulcast_rights")
			c.audit(ctx, "tv_simulcast.rights_confirmed", actor, a.ID, map[string]any{"channel_id": channel,
				"destination_id": a.DestinationID, "mode": a.Mode})
		}
	}
	for _, a := range cur {
		if !seen[a.DestinationID] {
			if err := c.st.deleteAttachment(ctx, a.ID); err != nil {
				return nil, err
			}
			c.audit(ctx, "tv_simulcast.detached", actor, a.ID, map[string]any{"channel_id": channel, "destination_id": a.DestinationID})
		}
	}
	c.audit(ctx, "tv_simulcast.attachments_saved", actor, channel, map[string]any{"attachments": len(next)})
	c.forget(channel)
	c.Wake()
	return c.st.attachments(ctx, channel)
}

// StartOutput is "Start now": the output runs until the next window boundary
// (an always attachment: until Stop).
func (c *Controller) StartOutput(ctx context.Context, actor, attachment string) error {
	return c.manual(ctx, actor, attachment, "start")
}

// StopOutput is "Stop now": off until the next window boundary (an always
// attachment: until Start).
func (c *Controller) StopOutput(ctx context.Context, actor, attachment string) error {
	return c.manual(ctx, actor, attachment, "stop")
}

func (c *Controller) manual(ctx context.Context, actor, id, what string) error {
	c.init()
	a, err := c.st.attachment(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return refuse(http.StatusNotFound, "no_such_attachment", "no such attachment")
	}
	if err != nil {
		return err
	}
	if what == "start" && !a.Enabled {
		return refuse(http.StatusConflict, "attachment_off", "turn the attachment on first (it needs the rights confirmation)")
	}
	now := c.now()
	cols := map[string]any{"updated_at": now}
	switch {
	case a.Mode == ModeAlways && what == "start":
		cols["manual"], cols["manual_until"] = nil, nil // back to always on
	case a.Mode == ModeAlways:
		cols["manual"], cols["manual_until"] = "stop", nil
	default:
		ws, _ := c.windows(ctx, a, LoadSimulcastSettings(ctx, c.DB), now)
		cols["manual"], cols["manual_until"] = what, nextBoundary(ws, now)
	}
	if err := c.st.setAttachment(ctx, id, cols); err != nil {
		return err
	}
	c.log().Info("tv_simulcast: manual start or stop", "what", what, "attachment_id", id, "by", actor, "event", "tv_simulcast_manual")
	c.audit(ctx, "tv_simulcast."+what, actor, id, map[string]any{"channel_id": a.ChannelID, "destination_id": a.DestinationID})
	c.Wake()
	return nil
}

// --- the controller's part (reconcile) ---

type schedCache struct {
	at time.Time
	ws []Window
}

// forget drops cached windows (a channel's, or all with "").
func (c *Controller) forget(channel string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.sched {
		if channel == "" || strings.HasPrefix(k, channel+"|") {
			delete(c.sched, k)
		}
	}
}

// windows resolves an attachment's schedule over [now-1d, now+horizon),
// cached for a minute (the timeline changes slowly).
func (c *Controller) windows(ctx context.Context, a Attachment, ss SimulcastSettings, now time.Time) ([]Window, error) {
	if a.Mode != ModeSchedule {
		return nil, nil
	}
	rb, _ := json.Marshal(a.Rules)
	key := a.ChannelID + "|" + a.ID + "|" + string(rb) + "|" + ss.Lead.String() + ss.Tail.String()
	c.mu.Lock()
	if x, ok := c.sched[key]; ok && now.Sub(x.at) < time.Minute && !now.Before(x.at) {
		c.mu.Unlock()
		return x.ws, nil
	}
	c.mu.Unlock()
	from, to := now.Add(-24*time.Hour), now.Add(ScheduleHorizon)
	var blocks []Block
	for _, r := range a.Rules {
		if r.Kind != RuleWindow {
			b, err := c.st.blocks(ctx, a.ChannelID, from.Add(-ss.Tail), to.Add(ss.Lead))
			if err != nil {
				return nil, err
			}
			blocks = b
			break
		}
	}
	ws := ResolveWindows(a.Rules, c.st.channelZone(ctx, a.ChannelID), blocks, from, to, ss.Lead, ss.Tail)
	c.mu.Lock()
	c.sched[key] = schedCache{at: now, ws: ws}
	c.mu.Unlock()
	return ws, nil
}

// SimulcastReady reports whether outputs can start: the integration set up
// with a passed test, Cloudflare not unavailable. Independent of
// tv_remote.enabled (AC10).
func (c *Controller) SimulcastReady(ctx context.Context) (bool, string) {
	if c.Integration == nil {
		return false, ReasonNotReady
	}
	st, err := c.Integration.Status(ctx)
	if err != nil || !st.Configured || st.LastTest == nil || !st.LastTest.OK {
		return false, ReasonNotReady
	}
	if r := c.unavailable(ctx); r != "" {
		return false, r
	}
	return true, ""
}

// SimulcastUsage is the month's output-hours against the cap.
type SimulcastUsage struct {
	Month     string  `json:"month"`
	Minutes   float64 `json:"minutes"`
	Hours     float64 `json:"hours"`
	CapHours  float64 `json:"cap_hours"`
	Percent   float64 `json:"percent"`
	Capped    bool    `json:"capped"`
	CostUSD   float64 `json:"cost_usd"`
	Exhausted bool    `json:"budget_exhausted"`
}

func (c *Controller) simulcastMinutes(ctx context.Context, now time.Time) float64 {
	ms := monthStart(now)
	outs, err := c.st.outputsSince(ctx, ms)
	if err != nil {
		return 0
	}
	m := 0.0
	for _, o := range outs {
		m += o.liveMinutes(ms, now)
	}
	return m
}

// SimulcastUsageNow computes the month's simulcast use.
func (c *Controller) SimulcastUsageNow(ctx context.Context, s Settings, ss SimulcastSettings, now time.Time) SimulcastUsage {
	c.init()
	u := SimulcastUsage{Month: monthStart(now).Format("2006-01"), CapHours: ss.MaxHoursPerMonth}
	u.Minutes = c.simulcastMinutes(ctx, now)
	u.Hours = u.Minutes / 60
	u.CostUSD = u.Minutes * s.DeliveryUSDPer1000 / 1000
	if ss.MaxHoursPerMonth > 0 {
		u.Percent = u.Hours / ss.MaxHoursPerMonth * 100
	}
	u.Capped = u.Hours >= ss.MaxHoursPerMonth
	if e, err := c.Estimate(ctx, s, now); err == nil {
		u.Exhausted = e.Exhausted
	}
	return u
}

type want struct {
	on     bool
	end    *time.Time // the window's end (nil: open-ended)
	reason string     // why not (or why waiting)
}

// wanted decides whether an attachment's output should run now.
func (c *Controller) wanted(ctx context.Context, a Attachment, o *SimOutput, ss SimulcastSettings, ready bool,
	notReady string, u SimulcastUsage, now time.Time) want {
	if !ss.Enabled {
		return want{reason: ReasonOff}
	}
	if !a.Enabled {
		return want{}
	}
	if ch, err := c.st.channel(ctx, a.ChannelID); err != nil || !ch.enabled {
		return want{reason: "the channel is disabled"} // EC4
	}
	if a.Manual != "" && a.ManualUntil != nil && !now.Before(*a.ManualUntil) {
		_ = c.st.setAttachment(ctx, a.ID, map[string]any{"manual": nil, "manual_until": nil})
		a.Manual, a.ManualUntil = "", nil
	}
	var w want
	switch {
	case a.Manual == "stop":
		return want{reason: "stopped by the admin"}
	case a.Manual == "start":
		w = want{on: true, end: a.ManualUntil}
		if a.Mode == ModeSchedule {
			if ws, err := c.windows(ctx, a, ss, now); err == nil {
				if x, ok := windowAt(ws, now); ok && (w.end == nil || x.End.After(*w.end)) {
					e := x.End
					w.end = &e
				}
			}
		}
	case a.Mode == ModeAlways:
		w = want{on: true}
	default:
		ws, err := c.windows(ctx, a, ss, now)
		if err != nil {
			return want{reason: "the schedule cannot be read: " + err.Error()}
		}
		x, ok := windowAt(ws, now)
		if !ok {
			return want{}
		}
		e := x.End
		w = want{on: true, end: &e}
	}
	running := o != nil && o.State == OutLive
	switch {
	case !ready:
		return want{reason: notReady}
	case u.Capped && running && a.Mode == ModeAlways && a.Manual != "start":
		return want{reason: ReasonCap} // always outputs stop at once at the cap
	case u.Capped && !running:
		return want{reason: ReasonCap}
	case u.Exhausted && !running:
		return want{reason: ReasonBudget}
	}
	return w
}

// holdOf reports whether the channel's push is held by simulcast.
func (c *Controller) holdOf(channel string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.holds[channel]
}

// simulcastTick is the simulcast part of a reconcile pass; it returns the
// channels whose push it holds.
func (c *Controller) simulcastTick(ctx context.Context, cl *cf.Client, s Settings, now time.Time) (map[string]bool, error) {
	atts, err := c.st.attachments(ctx, "")
	if err != nil {
		return nil, err
	}
	open, err := c.st.openOutputs(ctx)
	if err != nil {
		return nil, err
	}
	holds := map[string]bool{}
	if len(atts) == 0 && len(open) == 0 {
		c.setHoldsMap(holds)
		return holds, nil
	}
	ss := LoadSimulcastSettings(ctx, c.DB)
	byAtt := map[string]*SimOutput{}
	attIDs := map[string]bool{}
	for i := range open {
		if open[i].AttachmentID != "" {
			byAtt[open[i].AttachmentID] = &open[i]
		}
	}
	ready, notReady := c.SimulcastReady(ctx)
	u := c.SimulcastUsageNow(ctx, s, ss, now)
	wants := map[string]want{}
	for _, a := range atts {
		attIDs[a.ID] = true
		w := c.wanted(ctx, a, byAtt[a.ID], ss, ready, notReady, u, now)
		wants[a.ID] = w
		if w.on {
			holds[a.ChannelID] = true
		}
	}
	c.setHoldsMap(holds)
	if cl == nil && len(holds) > 0 {
		cl, _, _ = c.client(ctx)
	}
	// a push for every channel that needs one (as a tune would)
	waiting := map[string]string{}
	for ch := range holds {
		if _, running, err := c.st.runningPushFor(ctx, ch); err != nil || running {
			continue
		}
		if c.hasEndingPush(ctx, ch) {
			waiting[ch] = ReasonReconnecting
			continue
		}
		if cl == nil {
			waiting[ch] = ReasonNotReady
			continue
		}
		if _, err := c.lockedNewPush(ctx, s, ch, now); err != nil {
			var te *TuneError
			why := err.Error()
			if errors.As(err, &te) {
				why = te.Detail
				if te.Code == "remote_channels_full" {
					why = ReasonNoInput
				}
			}
			waiting[ch] = why
			c.noteOnce(ch+"|"+why, func() {
				c.log().Warn("tv_simulcast: a simulcast window cannot start its push", "channel_id", ch, "why", why,
					"event", "tv_simulcast_waiting")
			})
		} else {
			c.log().Info("tv_simulcast: push requested for a simulcast window", "channel_id", ch, "event", "tv_simulcast_push")
		}
	}
	for _, a := range atts {
		w, o := wants[a.ID], byAtt[a.ID]
		if w.on {
			if err := c.ensureOutput(ctx, cl, a, o, w, waiting[a.ChannelID], now); err != nil {
				c.log().Warn("tv_simulcast: output step failed", "attachment_id", a.ID, "err", err, "event", "tv_simulcast_error")
			}
			continue
		}
		if o != nil {
			why := w.reason
			if why == "" {
				why = "the window closed"
			}
			if why == ReasonCap && a.Mode == ModeAlways {
				c.log().Warn("tv_simulcast: an always-on output stopped at the monthly cap", "attachment_id", a.ID,
					"channel_id", a.ChannelID, "event", "tv_simulcast_capped")
			}
			c.endOutput(ctx, cl, *o, why, now)
		}
	}
	// outputs whose attachment is gone (detached, channel deleted: EC4)
	for _, o := range open {
		if o.AttachmentID == "" || !attIDs[o.AttachmentID] {
			c.endOutput(ctx, cl, o, "the attachment was removed", now)
		}
	}
	c.simulcastNotices(ctx, u, now)
	return holds, nil
}

// lockedNewPush starts a channel's push under the tune locks, so a tune
// and a simulcast window never take the same idle input.
func (c *Controller) lockedNewPush(ctx context.Context, s Settings, channel string, now time.Time) (string, error) {
	c.tune.Lock()
	defer c.tune.Unlock()
	unlock, err := c.st.lock(ctx, "tv_remote_tune")
	if err != nil {
		return "", err
	}
	defer unlock()
	if p, running, err := c.st.runningPushFor(ctx, channel); err != nil {
		return "", err
	} else if running {
		return p.ID, nil // a tune started it meanwhile
	}
	return c.newPush(ctx, s, channel, now)
}

func (c *Controller) setHoldsMap(h map[string]bool) {
	c.mu.Lock()
	c.holds = h
	c.mu.Unlock()
}

// noteOnce runs f once per key (per process): "the admin is notified once".
func (c *Controller) noteOnce(key string, f func()) {
	c.mu.Lock()
	seen := c.noted[key]
	c.noted[key] = true
	c.mu.Unlock()
	if !seen {
		f()
	}
}

// hasEndingPush: the channel has a push past running but not done (a
// rotation's successor starts on the same input once its recording is gone).
func (c *Controller) hasEndingPush(ctx context.Context, channel string) bool {
	ps, err := c.st.queryPushes(ctx, `WHERE channel_id = $1 AND state = 'cleanup'`, channel)
	return err == nil && len(ps) > 0
}

// ensureOutput moves a wanted output towards live.
func (c *Controller) ensureOutput(ctx context.Context, cl *cf.Client, a Attachment, o *SimOutput, w want, waiting string, now time.Time) error {
	if o == nil {
		x := SimOutput{ID: uuid.NewString(), AttachmentID: a.ID, ChannelID: a.ChannelID, DestinationID: a.DestinationID,
			State: OutScheduled, WindowEnd: w.end, Detail: waiting, CreatedAt: now}
		st := now
		x.WindowStart = &st
		if err := c.st.insertSimOutput(ctx, x); err != nil {
			if isUnique(err) {
				return nil
			}
			return err
		}
		c.log().Info("tv_simulcast: window open", "attachment_id", a.ID, "channel_id", a.ChannelID, "event", "tv_simulcast_window")
		o = &x
	}
	cols := map[string]any{}
	if !sameTime(o.WindowEnd, w.end) {
		cols["window_end"] = w.end
	}
	p, running, err := c.st.runningPushFor(ctx, a.ChannelID)
	if err != nil {
		return err
	}
	if !running || p.State != PushLive {
		switch {
		case o.State == OutLive || (o.CFOutputUID != "" && o.State == OutStopping):
			// a rotation or a retry on the same input: the output stays on the input
			cols["detail"] = ReasonReconnecting
		case running:
			cols["state"], cols["detail"] = OutStarting, ""
		default:
			cols["state"], cols["detail"] = OutScheduled, waiting
		}
		return c.st.setSimOutput(ctx, o.ID, diffCols(*o, cols))
	}
	in, err := c.st.input(ctx, p.InputID)
	if err != nil {
		return err
	}
	if o.CFOutputUID != "" && o.InputID == p.InputID {
		// the same input: a failover, a move, a rotation or a retry kept it
		if o.PushID != p.ID {
			cols["push_id"] = p.ID
			c.log().Info("tv_simulcast: output kept on the input of the channel's next push", "attachment_id", a.ID,
				"push_id", p.ID, "input", in.CFUID, "event", "tv_simulcast_kept")
		}
		if o.State != OutLive {
			cols["state"] = OutLive
		}
		cols["detail"] = ""
		if cl != nil && c.outputPollDue(o.ID, now) {
			outs, err := cl.ListOutputs(ctx, in.CFUID)
			if err == nil {
				found := false
				for _, x := range outs {
					if x.UID == o.CFOutputUID {
						found = true
						cols["cf_status"] = x.Status()
					}
				}
				if !found {
					// deleted behind Maktaba's back: make it again
					c.log().Warn("tv_simulcast: output missing at Cloudflare; re-creating", "attachment_id", a.ID,
						"event", "tv_simulcast_error")
					o.CFOutputUID = ""
					cols["cf_output_uid"] = nil
				}
			}
		}
		if o.CFOutputUID != "" {
			return c.st.setSimOutput(ctx, o.ID, diffCols(*o, cols))
		}
	}
	if o.CFOutputUID != "" && o.InputID != p.InputID {
		// an input swap (a retry on a fresh input): the old input went with its
		// outputs; re-attach on the new one
		if cl != nil && o.InputUID != "" {
			if err := cl.DeleteOutput(ctx, o.InputUID, o.CFOutputUID); err != nil && !notFound(err) {
				c.log().Warn("tv_simulcast: deleting the output on the old input failed", "attachment_id", a.ID,
					"err", err, "event", "tv_simulcast_error")
			}
		}
		c.log().Info("tv_simulcast: the channel moved to another input; re-attaching the output", "attachment_id", a.ID,
			"from", o.InputUID, "to", in.CFUID, "event", "tv_simulcast_reattach")
	}
	if cl == nil {
		cols["state"], cols["detail"] = OutStarting, ReasonNotReady
		return c.st.setSimOutput(ctx, o.ID, diffCols(*o, cols))
	}
	if o.State == OutErrored && !c.retryDue(o.ID, now) {
		return c.st.setSimOutput(ctx, o.ID, diffCols(*o, cols))
	}
	d, err := c.st.destination(ctx, a.DestinationID)
	if err != nil {
		return err
	}
	key, err := c.Integration.Secrets.Get(ctx, d.KeySecret)
	if err != nil || len(key) == 0 {
		cols["state"], cols["error"] = OutErrored, "the destination's stream key cannot be opened: enter it again"
		c.markRetry(o.ID, now)
		return c.st.setSimOutput(ctx, o.ID, diffCols(*o, cols))
	}
	out, err := cl.CreateOutput(ctx, in.CFUID, d.IngestURL, string(key), true)
	if err != nil {
		msg := err.Error()
		if ae, ok := cf.AsAPIError(err); ok {
			msg = ae.Error()
		}
		cols["state"], cols["error"] = OutErrored, "Cloudflare: "+msg
		c.markRetry(o.ID, now)
		c.log().Warn("tv_simulcast: creating the output failed", "attachment_id", a.ID, "input", in.CFUID, "err", msg,
			"event", "tv_simulcast_error")
		return c.st.setSimOutput(ctx, o.ID, diffCols(*o, cols))
	}
	cols["state"], cols["cf_output_uid"], cols["push_id"], cols["input_id"], cols["input_uid"] = OutLive, out.UID, p.ID, in.ID, in.CFUID
	cols["error"], cols["detail"], cols["cf_status"] = nil, "", out.Status()
	if o.StartedAt == nil {
		cols["started_at"] = now
	}
	c.log().Info("tv_simulcast: output live", "attachment_id", a.ID, "channel_id", a.ChannelID, "input", in.CFUID,
		"output", out.UID, "destination", d.Kind, "event", "tv_simulcast_live")
	c.audit(ctx, "tv_simulcast.output_started", "", a.ID, map[string]any{"channel_id": a.ChannelID,
		"destination_id": a.DestinationID, "push_id": p.ID})
	return c.st.setSimOutput(ctx, o.ID, cols)
}

// diffCols keeps only the columns that change (fewer writes every second).
func diffCols(o SimOutput, cols map[string]any) map[string]any {
	for k, v := range cols {
		switch k {
		case "state":
			if v == o.State {
				delete(cols, k)
			}
		case "detail":
			if v == o.Detail {
				delete(cols, k)
			}
		case "cf_status":
			if v == o.CFStatus {
				delete(cols, k)
			}
		case "error":
			if (v == nil && o.Error == "") || v == o.Error {
				delete(cols, k)
			}
		}
	}
	return cols
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func notFound(err error) bool {
	ae, ok := cf.AsAPIError(err)
	return ok && ae.Status == http.StatusNotFound
}

func (c *Controller) outputPollDue(id string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.outPoll[id]) < LivePoll {
		return false
	}
	c.outPoll[id] = now
	return true
}

func (c *Controller) retryDue(id string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return now.Sub(c.outRetry[id]) >= OutputRetry
}

func (c *Controller) markRetry(id string, now time.Time) {
	c.mu.Lock()
	c.outRetry[id] = now
	c.mu.Unlock()
}

// endOutput deletes an output at Cloudflare and closes its row; a failed
// delete leaves it stopping (retried next pass).
func (c *Controller) endOutput(ctx context.Context, cl *cf.Client, o SimOutput, why string, now time.Time) {
	if o.CFOutputUID != "" && o.InputUID != "" {
		if cl == nil {
			cl, _, _ = c.client(ctx)
		}
		if cl == nil {
			_ = c.st.setSimOutput(ctx, o.ID, map[string]any{"state": OutStopping, "detail": why})
			return
		}
		if err := cl.DeleteOutput(ctx, o.InputUID, o.CFOutputUID); err != nil && !notFound(err) {
			_ = c.st.setSimOutput(ctx, o.ID, map[string]any{"state": OutStopping, "detail": why, "error": "Cloudflare: " + err.Error()})
			c.log().Warn("tv_simulcast: deleting an output failed; retrying", "output", o.CFOutputUID, "err", err,
				"event", "tv_simulcast_error")
			return
		}
	}
	c.closeOutput(ctx, o, why, now)
}

// closeOutput ends an output's row (its Cloudflare output is gone).
func (c *Controller) closeOutput(ctx context.Context, o SimOutput, why string, now time.Time) {
	o.EndedAt = &now
	m := o.liveMinutes(time.Time{}, now)
	_ = c.st.setSimOutput(ctx, o.ID, map[string]any{"state": OutOff, "ended_at": now, "minutes": m, "detail": why})
	c.mu.Lock()
	delete(c.outPoll, o.ID)
	delete(c.outRetry, o.ID)
	c.mu.Unlock()
	if o.CFOutputUID != "" {
		c.log().Info("tv_simulcast: output stopped", "attachment_id", o.AttachmentID, "output", o.CFOutputUID,
			"minutes", round2(m), "why", why, "event", "tv_simulcast_stop")
	}
}

// dropOutputs deletes every output of an input that is going idle (D2): the
// rows Maktaba knows and anything else Cloudflare lists on it (always with
// list, else only when Maktaba knew of one).
func (c *Controller) dropOutputs(ctx context.Context, cl *cf.Client, in Input, why string, list bool) error {
	if in.ID == "" {
		return nil
	}
	open, err := c.st.querySimOutputs(ctx, `WHERE ended_at IS NULL AND (input_id = $1 OR input_uid = $2)`, in.ID, in.CFUID)
	if err != nil {
		return nil // no simulcast tables: nothing to drop
	}
	now := c.now()
	var failed error
	for _, o := range open {
		if o.CFOutputUID != "" && cl != nil {
			if err := cl.DeleteOutput(ctx, in.CFUID, o.CFOutputUID); err != nil && !notFound(err) {
				failed = err
				_ = c.st.setSimOutput(ctx, o.ID, map[string]any{"state": OutStopping, "error": "Cloudflare: " + err.Error()})
				continue
			}
		} else if o.CFOutputUID != "" {
			failed = errNoClient
			continue
		}
		c.closeOutput(ctx, o, why, now)
	}
	if cl != nil && (len(open) > 0 || list) {
		if outs, err := cl.ListOutputs(ctx, in.CFUID); err == nil {
			for _, x := range outs {
				if err := cl.DeleteOutput(ctx, in.CFUID, x.UID); err != nil && !notFound(err) {
					failed = err
				}
			}
		}
	}
	return failed
}

// simulcastNotices announces 80 % and 100 % of the cap once a month and
// keeps the daily output minutes (usage rows).
func (c *Controller) simulcastNotices(ctx context.Context, u SimulcastUsage, now time.Time) {
	c.mu.Lock()
	due := now.Sub(c.simUse) >= time.Minute
	if due {
		c.simUse = now
	}
	c.mu.Unlock()
	if due {
		c.writeSimulcastDays(ctx, now)
	}
	lvl := 0
	switch {
	case u.CapHours <= 0:
	case u.Percent >= 100:
		lvl = 100
	case u.Percent >= 80:
		lvl = 80
	}
	if lvl == 0 || c.Integration == nil {
		return
	}
	var n struct {
		Month string `json:"month"`
		Level int    `json:"level"`
	}
	_ = c.Integration.getSetting(ctx, SettingSimulcastNotice, &n)
	if n.Month == u.Month && n.Level >= lvl {
		return
	}
	_ = c.Integration.putSetting(ctx, SettingSimulcastNotice, map[string]any{"month": u.Month, "level": lvl, "at": now,
		"hours": round2(u.Hours), "cap_hours": u.CapHours})
	event := "tv_simulcast_cap_80"
	if lvl == 100 {
		event = "tv_simulcast_cap_100"
	}
	c.log().Warn("tv_simulcast: monthly output-hours threshold reached", "month", u.Month, "hours", round2(u.Hours),
		"cap_hours", u.CapHours, "event", event)
}

// writeSimulcastDays splits this month's output minutes per UTC day.
func (c *Controller) writeSimulcastDays(ctx context.Context, now time.Time) {
	ms := monthStart(now)
	outs, err := c.st.outputsSince(ctx, ms)
	if err != nil {
		return
	}
	perDay := map[string]float64{}
	for _, o := range outs {
		if o.StartedAt == nil {
			continue
		}
		from, to := *o.StartedAt, now
		if o.EndedAt != nil {
			to = *o.EndedAt
		}
		if from.Before(ms) {
			from = ms
		}
		for d := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC); d.Before(to); d = d.AddDate(0, 0, 1) {
			a, b := from, to
			if d.After(a) {
				a = d
			}
			if next := d.AddDate(0, 0, 1); next.Before(b) {
				b = next
			}
			if b.After(a) {
				perDay[d.Format("2006-01-02")] += b.Sub(a).Minutes()
			}
		}
	}
	for day, m := range perDay {
		_ = c.st.putSimulcastUsage(ctx, day, m, now)
	}
}

// closeAllOutputs ends every open output row (the integration was removed:
// Cloudflare deleted the outputs with the inputs, EC5).
func (c *Controller) closeAllOutputs(ctx context.Context, why string) {
	open, err := c.st.openOutputs(ctx)
	if err != nil {
		return
	}
	now := c.now()
	for _, o := range open {
		c.closeOutput(ctx, o, why, now)
	}
}

// --- the admin view ---

// OutputView is an attachment's output status for the page (AC7).
type OutputView struct {
	State       string     `json:"state"`
	Detail      string     `json:"detail,omitempty"`
	Error       string     `json:"error,omitempty"`
	CFStatus    string     `json:"cf_status,omitempty"`
	Platform    bool       `json:"platform_note"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	WindowEnd   *time.Time `json:"window_end,omitempty"`
	NextWindow  *Window    `json:"next_window,omitempty"`
	PushID      string     `json:"push_id,omitempty"`
	PushState   string     `json:"push_state,omitempty"`
	InputError  string     `json:"input_error,omitempty"`
	MinutesThis float64    `json:"minutes"`
}

// AttachmentView is an attachment with its destination, channel and output.
type AttachmentView struct {
	Attachment
	ChannelName     string     `json:"channel_name"`
	ChannelNumber   int        `json:"channel_number"`
	DestinationName string     `json:"destination_name"`
	DestinationKind string     `json:"destination_kind"`
	Output          OutputView `json:"output"`
	Windows         []Window   `json:"windows,omitempty"`
}

// SimulcastView is GET /api/tv/simulcast.
type SimulcastView struct {
	Enabled      bool              `json:"enabled"`
	Ready        bool              `json:"ready"`
	Reason       string            `json:"reason,omitempty"`
	Settings     map[string]any    `json:"settings"`
	Destinations []Destination     `json:"destinations"`
	Attachments  []AttachmentView  `json:"attachments"`
	Usage        SimulcastUsage    `json:"usage"`
	Notice       any               `json:"notice,omitempty"`
	DefaultURLs  map[string]string `json:"default_ingest"`
}

// SimulcastStatus answers the admin page (every channel, or one).
func (c *Controller) SimulcastStatus(ctx context.Context, channel string) (SimulcastView, error) {
	c.init()
	now := c.now()
	s := c.settings(ctx)
	ss := LoadSimulcastSettings(ctx, c.DB)
	v := SimulcastView{Enabled: ss.Enabled, Destinations: []Destination{}, Attachments: []AttachmentView{},
		DefaultURLs: DefaultIngest,
		Settings: map[string]any{"max_outputs_per_channel": ss.MaxOutputsPerChannel, "max_hours_per_month": ss.MaxHoursPerMonth,
			"lead_seconds": int(ss.Lead / time.Second), "tail_seconds": int(ss.Tail / time.Second), "bad_settings": ss.Bad}}
	v.Ready, v.Reason = c.SimulcastReady(ctx)
	if !ss.Enabled && v.Reason == "" {
		v.Reason = ReasonOff
	}
	open, err := c.st.openOutputs(ctx)
	if err != nil {
		return v, err
	}
	ds, err := c.st.destinations(ctx)
	if err != nil {
		return v, err
	}
	dmap := map[string]Destination{}
	for _, d := range ds {
		d = c.destView(ctx, d, open)
		dmap[d.ID] = d
		v.Destinations = append(v.Destinations, d)
	}
	atts, err := c.st.attachments(ctx, channel)
	if err != nil {
		return v, err
	}
	byAtt := map[string]SimOutput{}
	for _, o := range open {
		byAtt[o.AttachmentID] = o
	}
	for _, a := range atts {
		av := AttachmentView{Attachment: a}
		if ch, err := c.st.channel(ctx, a.ChannelID); err == nil {
			av.ChannelName, av.ChannelNumber = ch.Name, ch.Number
		}
		d := dmap[a.DestinationID]
		av.DestinationName, av.DestinationKind = d.Name, d.Kind
		ws, _ := c.windows(ctx, a, ss, now)
		for _, w := range ws {
			if w.End.After(now) && len(av.Windows) < 5 {
				av.Windows = append(av.Windows, w)
			}
		}
		ov := OutputView{State: OutOff}
		if o, ok := byAtt[a.ID]; ok {
			ov.State, ov.Detail, ov.Error, ov.CFStatus = o.State, o.Detail, o.Error, o.CFStatus
			ov.StartedAt, ov.WindowEnd, ov.PushID = o.StartedAt, o.WindowEnd, o.PushID
			ov.MinutesThis = o.liveMinutes(time.Time{}, now)
			ov.Platform = o.State == OutLive && o.CFStatus == ""
			if o.InputID != "" {
				if in, err := c.st.input(ctx, o.InputID); err == nil && in.Error != "" {
					ov.InputError = in.Error + ": " + ErrorText(in.Error)
					if o.State == OutLive {
						ov.State = OutErrored
					}
				}
			}
		} else if a.Enabled && a.Mode == ModeSchedule {
			for _, w := range av.Windows {
				if w.Start.After(now) {
					x := w
					ov.State, ov.NextWindow = OutScheduled, &x
					break
				}
			}
		}
		if p, ok, _ := c.st.runningPushFor(ctx, a.ChannelID); ok {
			ov.PushState = p.State
		}
		av.Output = ov
		v.Attachments = append(v.Attachments, av)
	}
	v.Usage = c.SimulcastUsageNow(ctx, s, ss, now)
	var notice map[string]any
	if c.Integration != nil && c.Integration.getSetting(ctx, SettingSimulcastNotice, &notice) == nil {
		if m, _ := notice["month"].(string); m == v.Usage.Month {
			v.Notice = notice
		}
	}
	return v, nil
}
