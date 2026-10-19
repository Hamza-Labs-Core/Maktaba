package tvremote_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

// Story 32.13: simulcast through Cloudflare live outputs on the channel's
// push input, on the SQLite twin with the fake Cloudflare API (outputs) and
// the fake clock.

type auditRow struct{ action, actor, target string }

type simFx struct {
	*remoteFx
	mu     sync.Mutex
	audits []auditRow
}

func newSim(t *testing.T) *simFx {
	t.Helper()
	fx := &simFx{remoteFx: newRemote(t)}
	// a passed Test connection (32.6) and simulcast switched on
	fx.setting(tvremote.SettingLastTest, map[string]any{"ok": true, "checked_at": fx.clock(), "lines": []any{}})
	fx.simRules(map[string]any{"enabled": true})
	fx.exec(`UPDATE channels SET mode_config = '{"timezone":"UTC"}'`)
	fx.c.Audit = func(_ context.Context, action, actor, target string, _ map[string]any) {
		fx.mu.Lock()
		fx.audits = append(fx.audits, auditRow{action, actor, target})
		fx.mu.Unlock()
	}
	return fx
}

func (fx *simFx) simRules(m map[string]any) { fx.setting(tvremote.SimulcastSettingsKey, m) }

func (fx *simFx) audited(action string) []auditRow {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	var out []auditRow
	for _, a := range fx.audits {
		if a.action == action {
			out = append(out, a)
		}
	}
	return out
}

func sp(s string) *string { return &s }

func (fx *simFx) dest(kind, url, key string) tvremote.Destination {
	fx.t.Helper()
	in := tvremote.DestinationInput{Name: sp("Dest " + kind), Kind: sp(kind), StreamKey: sp(key)}
	if url != "" {
		in.IngestURL = sp(url)
	}
	d, err := fx.c.CreateDestination(context.Background(), "admin-1", in)
	if err != nil {
		fx.t.Fatalf("create destination: %v", err)
	}
	return d
}

// attach sets one attachment on a channel (rights confirmed) and returns its id.
func (fx *simFx) attach(ch, dest, mode string, rules any) string {
	fx.t.Helper()
	raw, _ := json.Marshal(rules)
	as, err := fx.c.SetChannelSimulcast(context.Background(), "admin-1", ch, []tvremote.AttachmentInput{
		{DestinationID: dest, Mode: mode, Rules: raw, Enabled: true, RightsConfirmed: true}})
	if err != nil {
		fx.t.Fatalf("attach: %v", err)
	}
	for _, a := range as {
		if a.DestinationID == dest {
			return a.ID
		}
	}
	fx.t.Fatal("attachment not saved")
	return ""
}

type outRow struct {
	ID, State, UID, Push, Input, Detail, Error string
	Ended                                      bool
}

// outputs are an attachment's output rows, oldest first.
func (fx *simFx) outputs(att string) []outRow {
	fx.t.Helper()
	rows, err := fx.db.Query(`SELECT id, state, COALESCE(cf_output_uid, ''), COALESCE(push_id, ''), COALESCE(input_id, ''),
		COALESCE(detail, ''), COALESCE(error, ''), ended_at IS NOT NULL FROM tv_simulcast_outputs
		WHERE attachment_id = $1 ORDER BY created_at`, att)
	if err != nil {
		fx.t.Fatal(err)
	}
	defer rows.Close()
	var out []outRow
	for rows.Next() {
		var o outRow
		if err := rows.Scan(&o.ID, &o.State, &o.UID, &o.Push, &o.Input, &o.Detail, &o.Error, &o.Ended); err != nil {
			fx.t.Fatal(err)
		}
		out = append(out, o)
	}
	return out
}

// open is the attachment's open output row (fails when there is none).
func (fx *simFx) open(att string) outRow {
	fx.t.Helper()
	for _, o := range fx.outputs(att) {
		if !o.Ended {
			return o
		}
	}
	fx.t.Fatalf("no open output for %s: %+v", att, fx.outputs(att))
	return outRow{}
}

func (fx *simFx) hasOpen(att string) bool {
	for _, o := range fx.outputs(att) {
		if !o.Ended {
			return true
		}
	}
	return false
}

func (fx *simFx) holds(push string) []string {
	fx.t.Helper()
	var raw string
	if err := fx.db.QueryRow(`SELECT holds FROM tv_remote_pushes WHERE id = $1`, push).Scan(&raw); err != nil {
		fx.t.Fatal(err)
	}
	var hs []string
	_ = json.Unmarshal([]byte(raw), &hs)
	return hs
}

// endPush walks a stopping push to done (relay stopped, broadcast ended).
func (fx *simFx) endPush(ch string) {
	fx.t.Helper()
	p := fx.last(ch)
	if p.State != tvremote.PushStopping {
		fx.t.Fatalf("push not stopping: %+v", p)
	}
	uid := fx.cfInput(p.ID)
	fx.relay(p.ID, "stopped")
	fx.reconcile()
	fx.f.EndLive(uid)
	fx.reconcile()
}

func (fx *simFx) postOutputs(inputUID string) int {
	return fx.f.Count("POST /client/v4/accounts/acc1/stream/live_inputs/" + inputUID + "/outputs")
}

// TC1: the key is sealed in home_secrets, never in a table, masked in every answer.
func TestDestinationKeyInHomeSecrets(t *testing.T) {
	fx := newSim(t)
	ctx := context.Background()
	const key = "abcd-efgh-ijkl-mnop-qrst"
	d := fx.dest(tvremote.KindYouTube, "", key)
	if d.IngestURL != tvremote.DefaultIngest[tvremote.KindYouTube] {
		t.Errorf("YouTube ingest not prefilled: %s", d.IngestURL)
	}
	if !d.KeySet || d.KeyMasked != "••••qrst" {
		t.Errorf("masked %q set %v", d.KeyMasked, d.KeySet)
	}
	b, err := fx.store.Get(ctx, tvremote.SecretSimulcastKey(d.ID))
	if err != nil || string(b) != key {
		t.Fatalf("key not in home_secrets: %q %v", b, err)
	}
	var dump string
	_ = fx.db.QueryRow(`SELECT name || ingest_url || key_secret FROM tv_simulcast_destinations`).Scan(&dump)
	if strings.Contains(dump, key) {
		t.Error("the stream key is in tv_simulcast_destinations")
	}
	v, err := fx.c.SimulcastStatus(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(v)
	if strings.Contains(string(blob), key) {
		t.Error("the status answer carries the key")
	}
	// a changed key replaces the secret; the answer stays masked
	u, err := fx.c.UpdateDestination(ctx, "admin-1", d.ID, tvremote.DestinationInput{StreamKey: sp("zzzz-yyyy-xxxx-9999")})
	if err != nil || u.KeyMasked != "••••9999" {
		t.Fatalf("update %+v %v", u, err)
	}
	// validation
	for _, in := range []tvremote.DestinationInput{
		{Name: sp("x"), Kind: sp("rtmp"), IngestURL: sp("http://example.com/live"), StreamKey: sp("k")},
		{Name: sp("x"), Kind: sp("rtmp"), IngestURL: sp("rtmp://user:pw@example.com/live"), StreamKey: sp("k")},
		{Name: sp("x"), Kind: sp("twitch"), IngestURL: sp("rtmp://example.com/live"), StreamKey: sp("k")},
		{Name: sp("x"), Kind: sp("rtmp"), IngestURL: sp("rtmps://example.com/live"), StreamKey: sp("has space")},
		{Name: sp(""), Kind: sp("rtmp"), IngestURL: sp("rtmps://example.com/live"), StreamKey: sp("k")},
	} {
		var te *tvremote.TuneError
		if _, err := fx.c.CreateDestination(ctx, "a", in); !errors.As(err, &te) || te.Status != 400 {
			t.Errorf("%+v accepted: %v", in, err)
		}
	}
	tr, err := fx.c.TestDestination(ctx, d.ID)
	if err != nil || !tr.OK || !tr.URLOK || !tr.KeySet {
		t.Errorf("test %+v %v", tr, err)
	}
	_ = fx.store.Delete(ctx, tvremote.SecretSimulcastKey(d.ID))
	if tr, _ := fx.c.TestDestination(ctx, d.ID); tr.OK || tr.KeySet {
		t.Errorf("test without a key %+v", tr)
	}
	// deleting a destination deletes its key
	if err := fx.c.DeleteDestination(ctx, "admin-1", d.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := fx.store.Has(ctx, tvremote.SecretSimulcastKey(d.ID)); ok {
		t.Error("the key survived the destination")
	}
}

// TC2: weekly windows across midnight and DST, the timeline rules, merges, lead/tail.
func TestScheduleWindows(t *testing.T) {
	utc := time.UTC
	at := func(s string) time.Time {
		x, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	parse := func(js string) []tvremote.Rule {
		t.Helper()
		rs, err := tvremote.ParseRules(json.RawMessage(js), tvremote.ModeSchedule)
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	// Friday 23:00 to 01:00 (past midnight), no margins
	rs := parse(`[{"kind":"window","days":["fri"],"start":"23:00","end":"01:00"}]`)
	ws := tvremote.ResolveWindows(rs, utc, nil, at("2026-10-19T00:00:00Z"), at("2026-10-27T00:00:00Z"), 0, 0)
	if len(ws) != 1 || !ws[0].Start.Equal(at("2026-10-23T23:00:00Z")) || !ws[0].End.Equal(at("2026-10-24T01:00:00Z")) {
		t.Fatalf("across midnight: %+v", ws)
	}
	// DST: Europe/London leaves summer time on Sunday 25 Oct 2026 at 02:00 BST;
	// a 00:30-02:30 window keeps its wall times and lasts three real hours
	lon, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skip("no tzdata")
	}
	rs = parse(`[{"kind":"window","days":[0],"start":"00:30","end":"02:30"}]`)
	ws = tvremote.ResolveWindows(rs, lon, nil, at("2026-10-24T00:00:00Z"), at("2026-10-26T00:00:00Z"), 0, 0)
	if len(ws) != 1 || !ws[0].Start.Equal(at("2026-10-24T23:30:00Z")) || !ws[0].End.Equal(at("2026-10-25T02:30:00Z")) ||
		ws[0].End.Sub(ws[0].Start) != 3*time.Hour {
		t.Fatalf("DST: %+v", ws)
	}
	// the week after: GMT, two hours
	ws = tvremote.ResolveWindows(rs, lon, nil, at("2026-10-31T00:00:00Z"), at("2026-11-02T00:00:00Z"), 0, 0)
	if len(ws) != 1 || !ws[0].Start.Equal(at("2026-11-01T00:30:00Z")) || ws[0].End.Sub(ws[0].Start) != 2*time.Hour {
		t.Fatalf("after DST: %+v", ws)
	}
	// "while series Y airs" on a fake timeline: two episodes back to back
	// merge into one window with lead/tail, a third later is its own; the
	// live slot rule matches the live block only
	blocks := []tvremote.Block{
		{Start: at("2026-10-19T10:00:00Z"), End: at("2026-10-19T10:30:00Z"), Kind: "program", VideoID: "v1", SeriesID: "s1"},
		{Start: at("2026-10-19T10:30:00Z"), End: at("2026-10-19T11:00:00Z"), Kind: "program", VideoID: "v2", SeriesID: "s1"},
		{Start: at("2026-10-19T11:00:00Z"), End: at("2026-10-19T11:05:00Z"), Kind: "program", VideoID: "v9"},
		{Start: at("2026-10-19T11:05:00Z"), End: at("2026-10-19T12:00:00Z"), Kind: "program", VideoID: "v3", SeriesID: "s1"},
		{Start: at("2026-10-19T15:00:00Z"), End: at("2026-10-19T16:00:00Z"), Kind: "program", VideoID: "v4", SeriesID: "s1"},
		{Start: at("2026-10-19T18:00:00Z"), End: at("2026-10-19T19:00:00Z"), Kind: "live"},
	}
	from, to := at("2026-10-19T00:00:00Z"), at("2026-10-20T00:00:00Z")
	ws = tvremote.ResolveWindows(parse(`[{"kind":"series","series_id":"s1"}]`), utc, blocks, from, to, time.Minute, 30*time.Second)
	// 10:00-10:30 and 10:30-11:00 merge; 11:05-12:00 stays apart (its lead
	// starts 11:04, after the first one's tail at 11:00:30)
	if len(ws) != 3 || !ws[0].Start.Equal(at("2026-10-19T09:59:00Z")) || !ws[0].End.Equal(at("2026-10-19T11:00:30Z")) ||
		!ws[1].Start.Equal(at("2026-10-19T11:04:00Z")) || !ws[2].Start.Equal(at("2026-10-19T14:59:00Z")) {
		t.Fatalf("series: %+v", ws)
	}
	// with a 5-minute lead the gap closes: one window
	ws = tvremote.ResolveWindows(parse(`[{"kind":"series","series_id":"s1"}]`), utc, blocks, from, to, 5*time.Minute, 0)
	if len(ws) != 2 || !ws[0].End.Equal(at("2026-10-19T12:00:00Z")) {
		t.Fatalf("series merged: %+v", ws)
	}
	ws = tvremote.ResolveWindows(parse(`[{"kind":"programme","video_id":"v4"},{"kind":"live"}]`), utc, blocks, from, to, 0, 0)
	if len(ws) != 2 || !ws[0].Start.Equal(at("2026-10-19T15:00:00Z")) || !ws[1].Start.Equal(at("2026-10-19T18:00:00Z")) {
		t.Fatalf("programme + live: %+v", ws)
	}
	// overlapping rules merge
	ws = tvremote.ResolveWindows(parse(`[{"kind":"live"},{"kind":"window","start":"18:30","end":"20:00"}]`), utc, blocks, from, to, 0, 0)
	if len(ws) != 1 || !ws[0].Start.Equal(at("2026-10-19T18:00:00Z")) || !ws[0].End.Equal(at("2026-10-19T20:00:00Z")) {
		t.Fatalf("overlap: %+v", ws)
	}
	for _, bad := range []string{`[]`, `[{"kind":"window","start":"25:00","end":"01:00"}]`, `[{"kind":"series"}]`,
		`[{"kind":"window","days":["xyz"],"start":"01:00","end":"02:00"}]`, `[{"kind":"weather"}]`, `{"kind":"live"}`} {
		if _, err := tvremote.ParseRules(json.RawMessage(bad), tvremote.ModeSchedule); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if rs, err := tvremote.ParseRules(nil, tvremote.ModeAlways); err != nil || len(rs) != 0 {
		t.Errorf("always without rules: %v", err)
	}
}

// TC3: a simulcast hold keeps the push past the idle timeout; viewers share
// it; once the hold ends the normal idle timeout applies.
func TestSimulcastHoldsPush(t *testing.T) {
	fx := newSim(t)
	ctx := context.Background()
	ch := fx.chans[0]
	d := fx.dest(tvremote.KindRTMP, "rtmps://ingest.example.com/live", "key-123456789")
	att := fx.attach(ch, d.ID, tvremote.ModeAlways, nil)
	fx.reconcile()
	p := fx.last(ch)
	if p.State != tvremote.PushRequested {
		t.Fatalf("no push for an always-on simulcast: %+v", p)
	}
	p = fx.goLive(ch)
	fx.reconcile()
	if o := fx.open(att); o.State != tvremote.OutLive || o.UID == "" {
		t.Fatalf("output %+v", o)
	}
	if hs := fx.holds(p.ID); len(hs) != 1 || hs[0] != tvremote.HoldSimulcast {
		t.Errorf("holds %v", hs)
	}
	for i := 0; i < 5; i++ { // 10 minutes with nobody watching
		fx.advance(2 * time.Minute)
		fx.reconcile()
	}
	if q := fx.last(ch); q.ID != p.ID || q.State != tvremote.PushLive {
		t.Fatalf("idle timeout stopped a held push: %+v", q)
	}
	// a remote viewer shares the push
	r, te := fx.tune("u1", ch, "phone")
	if te != nil || r.State != "live" && r.State != "starting" {
		t.Fatalf("tune %+v %v", r, te)
	}
	if len(fx.pushes(ch)) != 1 {
		t.Fatal("the viewer got a second push")
	}
	fx.reconcile()
	if hs := fx.holds(p.ID); len(hs) != 2 {
		t.Errorf("holds with a viewer %v", hs)
	}
	// stop the output: it is deleted at Cloudflare; the viewer keeps watching
	if err := fx.c.StopOutput(ctx, "admin-1", att); err != nil {
		t.Fatal(err)
	}
	fx.reconcile()
	if fx.f.OutputCount() != 0 || fx.hasOpen(att) {
		t.Fatalf("output not deleted: %d", fx.f.OutputCount())
	}
	if fx.last(ch).State != tvremote.PushLive {
		t.Fatal("stopping the output stopped the push")
	}
	_ = fx.c.End(ctx, viewer("u1"), r.SessionID)
	fx.reconcile()
	fx.advance(2 * time.Minute)
	fx.reconcile()
	if fx.last(ch).State != tvremote.PushLive {
		t.Fatal("stopped before the idle timeout")
	}
	fx.advance(61 * time.Second)
	fx.reconcile()
	if q := fx.last(ch); q.State != tvremote.PushStopping || q.Reason != "idle" {
		t.Fatalf("after the idle timeout: %+v", q)
	}
	// Start now: back to always on (a new push, a new output)
	fx.endPush(ch)
	if err := fx.c.StartOutput(ctx, "admin-1", att); err != nil {
		t.Fatal(err)
	}
	fx.reconcile()
	fx.goLive(ch)
	fx.reconcile()
	if o := fx.open(att); o.State != tvremote.OutLive {
		t.Fatalf("after Start now: %+v", o)
	}
}

// TC4: the output is created at the window's start (minus lead) with the key
// from home_secrets and deleted at its end (plus tail); the push then idles
// out and the idle input keeps no output.
func TestOutputsCreatedAndDeletedWithWindow(t *testing.T) {
	fx := newSim(t)
	ch := fx.chans[0]
	const key = "yt-key-0123456789"
	d := fx.dest(tvremote.KindYouTube, "", key)
	// Monday 19 Oct 2026, 12:10-13:10 UTC; lead 60 s, tail 30 s (defaults)
	att := fx.attach(ch, d.ID, tvremote.ModeSchedule, []map[string]any{{"kind": "window", "days": []int{1}, "start": "12:10", "end": "13:10"}})
	fx.reconcile()
	if len(fx.pushes(ch)) != 0 || fx.hasOpen(att) {
		t.Fatal("a push before the window")
	}
	v, _ := fx.c.SimulcastStatus(context.Background(), ch)
	if len(v.Attachments) != 1 || v.Attachments[0].Output.State != tvremote.OutScheduled ||
		v.Attachments[0].Output.NextWindow == nil || v.Attachments[0].Output.NextWindow.Start.Format("15:04:05") != "12:09:00" {
		t.Fatalf("scheduled view %+v", v.Attachments)
	}
	fx.advance(9 * time.Minute) // 12:09: the lead
	fx.reconcile()
	if fx.last(ch).State != tvremote.PushRequested {
		t.Fatalf("no push at the window's lead: %+v", fx.pushes(ch))
	}
	if o := fx.open(att); o.State != tvremote.OutStarting {
		t.Errorf("output while the push starts: %+v", o)
	}
	p := fx.goLive(ch)
	uid := fx.cfInput(p.ID)
	fx.reconcile()
	outs := fx.f.InputOutputs(uid)
	if len(outs) != 1 || outs[0].URL != tvremote.DefaultIngest[tvremote.KindYouTube] || outs[0].StreamKey != key ||
		outs[0].Enabled == nil || !*outs[0].Enabled {
		t.Fatalf("output at Cloudflare %+v", outs)
	}
	if o := fx.open(att); o.State != tvremote.OutLive || o.UID != outs[0].UID || o.Push != p.ID {
		t.Fatalf("output row %+v", o)
	}
	if len(fx.audited("tv_simulcast.output_started")) != 1 {
		t.Error("output start not audited")
	}
	fx.advance(61 * time.Minute) // 13:10: still in the tail
	fx.reconcile()
	if len(fx.f.InputOutputs(uid)) != 1 {
		t.Fatal("deleted inside the tail")
	}
	fx.advance(31 * time.Second) // 13:10:31
	fx.reconcile()
	if len(fx.f.InputOutputs(uid)) != 0 || fx.hasOpen(att) {
		t.Fatalf("output not deleted at the window's end: %+v", fx.outputs(att))
	}
	if o := fx.outputs(att)[0]; o.State != tvremote.OutOff {
		t.Errorf("closed row %+v", o)
	}
	// no hold: the push idles out after idle_timeout
	if fx.last(ch).State != tvremote.PushLive {
		t.Fatal("the push stopped with the window (it waits for the idle timeout)")
	}
	fx.advance(3*time.Minute + time.Second)
	fx.reconcile()
	fx.endPush(ch)
	var st string
	_ = fx.db.QueryRow(`SELECT state FROM tv_remote_inputs`).Scan(&st)
	if fx.last(ch).State != tvremote.PushDone || st != tvremote.InputIdle || fx.f.OutputCount() != 0 {
		t.Fatalf("after: push %s input %s outputs %d", fx.last(ch).State, st, fx.f.OutputCount())
	}
	// minutes counted: about 62 output-minutes
	u := fx.c.SimulcastUsageNow(context.Background(), tvremote.DefaultSettings(), tvremote.LoadSimulcastSettings(context.Background(), fx.db), fx.clock())
	if u.Minutes < 61 || u.Minutes > 63 {
		t.Errorf("output minutes %.1f", u.Minutes)
	}
}

// TC5: an attached output stays attached across a failover (the placement
// moves) and a rotation at max_push_hours (a new broadcast on the same
// input): no output is re-created. An input swap re-attaches it.
func TestOutputsSurviveFailoverAndRotation(t *testing.T) {
	fx := newSim(t)
	ch := fx.chans[0]
	fx.rules(map[string]any{"max_push_hours": 1})
	d := fx.dest(tvremote.KindFacebook, "", "fb-key-0123456789")
	att := fx.attach(ch, d.ID, tvremote.ModeAlways, nil)
	fx.exec(`INSERT INTO channel_placements (channel_id, host_id, generation, state) VALUES ($1, 'node-pc', 7, 'live')`, ch)
	fx.reconcile()
	first := fx.goLive(ch)
	uid := fx.cfInput(first.ID)
	fx.reconcile()
	o := fx.open(att)
	if o.State != tvremote.OutLive || fx.postOutputs(uid) != 1 {
		t.Fatalf("output %+v posts %d", o, fx.postOutputs(uid))
	}

	// failover: the channel moves to the home mid-window
	fx.exec(`UPDATE channel_placements SET host_id = 'home', generation = 9, reason = 'failover' WHERE channel_id = $1`, ch)
	for i := 0; i < 3; i++ {
		fx.advance(20 * time.Second)
		fx.reconcile()
	}
	if q := fx.last(ch); q.ID != first.ID || q.State != tvremote.PushLive {
		t.Fatalf("failover changed the push: %+v", q)
	}
	if x := fx.open(att); x.ID != o.ID || x.UID != o.UID || x.State != tvremote.OutLive || fx.postOutputs(uid) != 1 ||
		len(fx.f.InputOutputs(uid)) != 1 {
		t.Fatalf("failover touched the output: %+v posts %d", x, fx.postOutputs(uid))
	}

	// rotation at max_push_hours: nobody watches, the simulcast hold rotates
	fx.advance(time.Hour)
	fx.reconcile()
	if p := fx.pushes(ch)[0]; p.State != tvremote.PushStopping || p.Reason != "rotate" {
		t.Fatalf("not rotated: %+v", p)
	}
	fx.relay(first.ID, "stopped")
	fx.reconcile()
	if x := fx.open(att); x.State != tvremote.OutLive || x.Detail != tvremote.ReasonReconnecting {
		t.Errorf("output during the rotation %+v", x)
	}
	fx.f.EndLive(uid) // Cloudflare ends the broadcast; the outputs stay on the input
	fx.reconcile()
	ps := fx.pushes(ch)
	if len(ps) != 2 || ps[0].State != tvremote.PushDone || ps[1].State != tvremote.PushRequested || ps[1].Input != first.Input {
		t.Fatalf("rotation pushes %+v", ps)
	}
	if outs := fx.f.InputOutputs(uid); len(outs) != 1 || outs[0].UID != o.UID {
		t.Fatalf("the rotation lost the output: %+v", outs)
	}
	second := fx.goLive(ch)
	fx.reconcile()
	if x := fx.open(att); x.ID != o.ID || x.UID != o.UID || x.Push != second.ID || x.State != tvremote.OutLive {
		t.Fatalf("after the rotation %+v", x)
	}
	if fx.postOutputs(uid) != 1 {
		t.Errorf("output re-created on rotation: %d posts", fx.postOutputs(uid))
	}

	// input swap: a codec error retries on a fresh input; the old input is
	// deleted at Cloudflare with its outputs and the output is re-attached
	fx.f.Mu.Lock()
	fx.f.Status = map[string]json.RawMessage{uid: json.RawMessage(`{"current":{"state":"errored","reason":"ERR_GOP_OUT_OF_RANGE"}}`)}
	fx.f.Mu.Unlock()
	fx.advance(tvremote.LivePoll + time.Second)
	fx.reconcile()
	ps = fx.pushes(ch)
	third := ps[len(ps)-1]
	if third.Input == first.Input || third.State != tvremote.PushRequested {
		t.Fatalf("no swap: %+v", ps)
	}
	third = fx.goLive(ch)
	uid2 := fx.cfInput(third.ID)
	fx.reconcile()
	if outs := fx.f.InputOutputs(uid2); len(outs) != 1 || fx.postOutputs(uid2) != 1 {
		t.Fatalf("not re-attached on the new input: %+v", outs)
	}
	if x := fx.open(att); x.ID != o.ID || x.Input != third.Input || x.State != tvremote.OutLive {
		t.Fatalf("after the swap %+v", x)
	}
}

// TC6: 80 % and 100 % of the cap and of the budget.
func TestSimulcastCapAndBudget(t *testing.T) {
	t.Run("always stops at once at the cap", func(t *testing.T) {
		fx := newSim(t)
		fx.simRules(map[string]any{"enabled": true, "max_hours_per_month": 1})
		bell := &recNotifier{}
		fx.c.Notifier = bell
		ch, ch2 := fx.chans[0], fx.chans[1]
		d := fx.dest(tvremote.KindRTMP, "rtmp://ingest.example.com/app", "key-aaaaaaaaaa")
		att := fx.attach(ch, d.ID, tvremote.ModeAlways, nil)
		fx.reconcile()
		fx.goLive(ch)
		fx.reconcile()
		for i := 0; i < 6; i++ { // 48 minutes
			fx.advance(8 * time.Minute)
			fx.reconcile()
		}
		var n struct{ Level int }
		raw := ""
		_ = fx.db.QueryRow(`SELECT value FROM app_settings WHERE key = $1`, tvremote.SettingSimulcastNotice).Scan(&raw)
		_ = json.Unmarshal([]byte(raw), &n)
		if n.Level != 80 || !fx.hasOpen(att) {
			t.Fatalf("at 80 %%: notice %s, output open %v", raw, fx.hasOpen(att))
		}
		// one bell notice for the 80 % crossing (32.13 AC9), not one per tick
		capNotices := func() []sentNotice {
			var out []sentNotice
			for _, x := range bell.all() {
				if x.kind == tvremote.NotifySimulcastCap {
					out = append(out, x)
				}
			}
			return out
		}
		if got := capNotices(); len(got) != 1 || got[0].p.Params["level"] != 80 || got[0].p.Severity != notify.SeverityWarn {
			t.Fatalf("80 %% bell notices %+v", got)
		}
		fx.advance(13 * time.Minute) // 61 minutes
		fx.reconcile()
		if fx.hasOpen(att) || fx.f.OutputCount() != 0 {
			t.Fatal("an always output kept running past the cap")
		}
		if o := fx.outputs(att)[0]; o.Detail != tvremote.ReasonCap {
			t.Errorf("closed with %q", o.Detail)
		}
		_ = fx.db.QueryRow(`SELECT value FROM app_settings WHERE key = $1`, tvremote.SettingSimulcastNotice).Scan(&raw)
		_ = json.Unmarshal([]byte(raw), &n)
		if n.Level != 100 {
			t.Errorf("notice %s", raw)
		}
		if got := capNotices(); len(got) != 2 || got[1].p.Params["level"] != 100 || got[1].p.Severity != notify.SeverityError ||
			got[1].p.Params["cap_hours"] != 1.0 {
			t.Fatalf("100 %% bell notices %+v", got)
		}
		// no new output starts
		att2 := fx.attach(ch2, d.ID, tvremote.ModeAlways, nil)
		fx.reconcile()
		if len(fx.pushes(ch2)) != 0 || fx.hasOpen(att2) {
			t.Fatal("a new output started at the cap")
		}
		v, _ := fx.c.SimulcastStatus(context.Background(), "")
		if !v.Usage.Capped || v.Usage.Hours < 1 {
			t.Errorf("usage %+v", v.Usage)
		}
		// the estimate counts the output minutes as delivered
		e, _ := fx.c.Estimate(context.Background(), tvremote.DefaultSettings(), fx.clock())
		if e.SimulcastMinutes < 60 {
			t.Errorf("estimate simulcast minutes %.1f", e.SimulcastMinutes)
		}
	})

	t.Run("a scheduled output finishes its window at the cap", func(t *testing.T) {
		fx := newSim(t)
		fx.simRules(map[string]any{"enabled": true, "max_hours_per_month": 1, "lead_seconds": 0, "tail_seconds": 0})
		ch := fx.chans[0]
		d := fx.dest(tvremote.KindRTMP, "rtmp://ingest.example.com/app", "key-bbbbbbbbbb")
		att := fx.attach(ch, d.ID, tvremote.ModeSchedule, []map[string]any{
			{"kind": "window", "start": "12:00", "end": "14:00"}, {"kind": "window", "start": "14:05", "end": "15:00"}})
		fx.reconcile()
		fx.goLive(ch)
		fx.reconcile()
		for i := 0; i < 7; i++ { // 70 minutes: past the cap, inside the window
			fx.advance(10 * time.Minute)
			fx.reconcile()
		}
		if o := fx.open(att); o.State != tvremote.OutLive {
			t.Fatalf("stopped mid-window at the cap: %+v", o)
		}
		fx.advance(51 * time.Minute) // 14:01
		fx.reconcile()
		if fx.hasOpen(att) {
			t.Fatal("still running after its window")
		}
		fx.advance(5 * time.Minute) // 14:06: the next window does not start
		fx.reconcile()
		if fx.hasOpen(att) || fx.f.OutputCount() != 0 {
			t.Fatal("a new window started at the cap")
		}
	})

	t.Run("budget block_new: no new output, running ones finish", func(t *testing.T) {
		fx := newSim(t)
		fx.simRules(map[string]any{"enabled": true, "lead_seconds": 0, "tail_seconds": 0})
		ch := fx.chans[0]
		d := fx.dest(tvremote.KindRTMP, "rtmp://ingest.example.com/app", "key-cccccccccc")
		att := fx.attach(ch, d.ID, tvremote.ModeSchedule, []map[string]any{
			{"kind": "window", "start": "12:00", "end": "12:30"}, {"kind": "window", "start": "13:00", "end": "13:30"}})
		fx.reconcile()
		fx.goLive(ch)
		fx.reconcile()
		// the storage plan alone is $5: a $5 budget is used up
		fx.rules(map[string]any{"monthly_budget_usd": 5})
		fx.advance(10 * time.Minute)
		fx.reconcile()
		if o := fx.open(att); o.State != tvremote.OutLive {
			t.Fatalf("the budget stopped a running window: %+v", o)
		}
		fx.advance(21 * time.Minute) // 12:31
		fx.reconcile()
		if fx.hasOpen(att) {
			t.Fatal("still running after its window")
		}
		fx.advance(30 * time.Minute) // 13:01
		fx.reconcile()
		if fx.hasOpen(att) || fx.f.OutputCount() != 0 {
			t.Fatal("a new output started with the budget used up")
		}
	})
}

// TC7: turning an attachment on needs the rights confirmation, stored and
// audited; nothing is simulcast unless attached.
func TestRightsConfirmationRequired(t *testing.T) {
	fx := newSim(t)
	ctx := context.Background()
	ch := fx.chans[0]
	d := fx.dest(tvremote.KindYouTube, "", "yt-key-zzzzzzzz")
	_, err := fx.c.SetChannelSimulcast(ctx, "admin-1", ch, []tvremote.AttachmentInput{
		{DestinationID: d.ID, Mode: tvremote.ModeAlways, Enabled: true}})
	var te *tvremote.TuneError
	if !errors.As(err, &te) || te.Status != 409 || te.Code != "rights_confirmation_required" {
		t.Fatalf("without confirmation: %v", err)
	}
	fx.reconcile()
	if len(fx.pushes(ch)) != 0 {
		t.Fatal("simulcast without an attachment")
	}
	// off attachments need no confirmation
	if _, err := fx.c.SetChannelSimulcast(ctx, "admin-1", ch, []tvremote.AttachmentInput{
		{DestinationID: d.ID, Mode: tvremote.ModeAlways}}); err != nil {
		t.Fatal(err)
	}
	as, err := fx.c.SetChannelSimulcast(ctx, "admin-1", ch, []tvremote.AttachmentInput{
		{DestinationID: d.ID, Mode: tvremote.ModeAlways, Enabled: true, RightsConfirmed: true}})
	if err != nil || len(as) != 1 || as[0].RightsConfirmedBy != "admin-1" || as[0].RightsConfirmedAt == nil {
		t.Fatalf("confirmed: %+v %v", as, err)
	}
	if a := fx.audited("tv_simulcast.rights_confirmed"); len(a) != 1 || a[0].actor != "admin-1" || a[0].target != as[0].ID {
		t.Errorf("audit %+v", a)
	}
	// saving it again while on needs no new confirmation
	if _, err := fx.c.SetChannelSimulcast(ctx, "admin-1", ch, []tvremote.AttachmentInput{
		{DestinationID: d.ID, Mode: tvremote.ModeAlways, Enabled: true}}); err != nil {
		t.Errorf("re-save: %v", err)
	}
	// too many destinations
	var many []tvremote.AttachmentInput
	for i := 0; i < 4; i++ {
		x := fx.dest(tvremote.KindRTMP, "rtmp://ingest.example.com/app", "key-many-"+string(rune('a'+i))+"xxxxxx")
		many = append(many, tvremote.AttachmentInput{DestinationID: x.ID, Mode: tvremote.ModeAlways})
	}
	if _, err := fx.c.SetChannelSimulcast(ctx, "admin-1", ch, many); !errors.As(err, &te) || te.Code != "too_many_outputs" {
		t.Errorf("4 destinations: %v", err)
	}
	// simulcast switched off: nothing starts
	fx.simRules(map[string]any{"enabled": false})
	fx.reconcile()
	if len(fx.pushes(ch)) != 0 {
		t.Error("a push with tv_simulcast.enabled off")
	}
}

// EC2-EC5 and the delete guard, one test each (a single test running them all
// as subtests passed the unit tier's per-test cap under -race on a busy runner).

// TestSimulcastEdgeSharedPush: two attachments share one push.
func TestSimulcastEdgeSharedPush(t *testing.T) {
	fx := newSim(t)
	ctx := context.Background()
	ch := fx.chans[0]
	d1 := fx.dest(tvremote.KindYouTube, "", "yt-key-11111111")
	d2 := fx.dest(tvremote.KindFacebook, "", "fb-key-22222222")
	if _, err := fx.c.SetChannelSimulcast(ctx, "admin-1", ch, []tvremote.AttachmentInput{
		{DestinationID: d1.ID, Mode: tvremote.ModeAlways, Enabled: true, RightsConfirmed: true},
		{DestinationID: d2.ID, Mode: tvremote.ModeAlways, Enabled: true, RightsConfirmed: true}}); err != nil {
		t.Fatal(err)
	}
	fx.reconcile()
	p := fx.goLive(ch)
	fx.reconcile()
	uid := fx.cfInput(p.ID)
	if len(fx.pushes(ch)) != 1 || len(fx.f.InputOutputs(uid)) != 2 {
		t.Fatalf("pushes %d outputs %d", len(fx.pushes(ch)), len(fx.f.InputOutputs(uid)))
	}
	// deleting a destination in use is refused; detaching deletes its output only
	var te *tvremote.TuneError
	if err := fx.c.DeleteDestination(ctx, "admin-1", d1.ID); !errors.As(err, &te) || te.Code != "destination_in_use" {
		t.Fatalf("delete in use: %v", err)
	}
	if _, err := fx.c.SetChannelSimulcast(ctx, "admin-1", ch, []tvremote.AttachmentInput{
		{DestinationID: d2.ID, Mode: tvremote.ModeAlways, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	fx.reconcile()
	outs := fx.f.InputOutputs(uid)
	if len(outs) != 1 || outs[0].StreamKey != "fb-key-22222222" {
		t.Fatalf("after detaching: %+v", outs)
	}
	if err := fx.c.DeleteDestination(ctx, "admin-1", d1.ID); err != nil {
		t.Fatalf("delete after detaching: %v", err)
	}
	// EC4: the channel is disabled: its outputs are deleted
	fx.exec(`UPDATE channels SET enabled = 0 WHERE id = $1`, ch)
	fx.reconcile()
	if fx.f.OutputCount() != 0 {
		t.Fatal("a disabled channel kept its output")
	}
}

// TestSimulcastEdgeNoFreeInput: no free input: waits, then starts.
func TestSimulcastEdgeNoFreeInput(t *testing.T) {
	fx := newSim(t)
	ctx := context.Background()
	fx.rules(map[string]any{"max_channels": 1})
	a, b := fx.chans[0], fx.chans[1]
	r, _ := fx.tune("u1", a, "")
	fx.goLive(a)
	d := fx.dest(tvremote.KindRTMP, "rtmp://ingest.example.com/app", "key-dddddddddd")
	att := fx.attach(b, d.ID, tvremote.ModeAlways, nil)
	fx.reconcile()
	if o := fx.open(att); o.State != tvremote.OutScheduled || o.Detail != tvremote.ReasonNoInput {
		t.Fatalf("waiting output %+v", o)
	}
	if len(fx.pushes(b)) != 0 {
		t.Fatal("a push beyond max_channels")
	}
	_ = fx.c.End(ctx, viewer("u1"), r.SessionID)
	fx.reconcile()
	fx.advance(3*time.Minute + time.Second)
	fx.reconcile()
	fx.endPush(a)
	fx.reconcile()
	if fx.last(b).State != tvremote.PushRequested {
		t.Fatalf("the freed input did not start the simulcast: %+v", fx.pushes(b))
	}
	fx.goLive(b)
	fx.reconcile()
	if o := fx.open(att); o.State != tvremote.OutLive {
		t.Fatalf("after the input freed %+v", o)
	}
}

// TestSimulcastEdgeIntegrationRemoved: integration removed: outputs go with the inputs.
func TestSimulcastEdgeIntegrationRemoved(t *testing.T) {
	fx := newSim(t)
	ch := fx.chans[0]
	fx.in.StopPushes = fx.c.StopAll
	d := fx.dest(tvremote.KindRTMP, "rtmp://ingest.example.com/app", "key-eeeeeeeeee")
	att := fx.attach(ch, d.ID, tvremote.ModeAlways, nil)
	fx.reconcile()
	fx.goLive(ch)
	fx.reconcile()
	if fx.f.OutputCount() != 1 {
		t.Fatal("no output")
	}
	if _, err := fx.in.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fx.f.OutputCount() != 0 || fx.hasOpen(att) {
		t.Fatalf("outputs after remove: %d open %v", fx.f.OutputCount(), fx.hasOpen(att))
	}
	fx.reconcile()
	if fx.hasOpen(att) {
		t.Fatal("an output reopened without the integration")
	}
}

// TestSimulcastEdgeOutputRefused: Cloudflare refuses the output: errored, retried.
func TestSimulcastEdgeOutputRefused(t *testing.T) {
	fx := newSim(t)
	ch := fx.chans[0]
	d := fx.dest(tvremote.KindRTMP, "rtmp://ingest.example.com/app", "key-ffffffffff")
	att := fx.attach(ch, d.ID, tvremote.ModeAlways, nil)
	fx.f.Mu.Lock()
	fx.f.OutputFail, fx.f.OutputFailMsg = 1, "bad url"
	fx.f.Mu.Unlock()
	fx.reconcile()
	fx.goLive(ch)
	fx.reconcile()
	if o := fx.open(att); o.State != tvremote.OutErrored || !strings.Contains(o.Error, "bad url") {
		t.Fatalf("errored %+v", o)
	}
	fx.advance(tvremote.OutputRetry + time.Second)
	fx.reconcile()
	if o := fx.open(att); o.State != tvremote.OutLive {
		t.Fatalf("not retried %+v", o)
	}
}

// TestSimulcastEdgeOutputStatus: Q13: a per-output status is shown when Cloudflare has one.
func TestSimulcastEdgeOutputStatus(t *testing.T) {
	fx := newSim(t)
	ch := fx.chans[0]
	fx.f.Mu.Lock()
	fx.f.OutputStatus = true
	fx.f.Mu.Unlock()
	d := fx.dest(tvremote.KindRTMP, "rtmp://ingest.example.com/app", "key-gggggggggg")
	fx.attach(ch, d.ID, tvremote.ModeAlways, nil)
	fx.reconcile()
	fx.goLive(ch)
	fx.reconcile()
	v, _ := fx.c.SimulcastStatus(context.Background(), ch)
	if o := v.Attachments[0].Output; o.CFStatus != "connected" || o.Platform {
		t.Fatalf("view %+v", o)
	}
}
