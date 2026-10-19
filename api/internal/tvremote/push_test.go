package tvremote_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

// TC1: inputs are made on demand up to max_channels, with the pool's settings;
// their keys go to home_secrets, never to the table.
func TestPoolCreatesLazilyUpToMax(t *testing.T) {
	fx := newRemote(t)
	fx.rules(map[string]any{"max_channels": 2, "low_latency": true})
	if len(fx.f.Inputs) != 0 {
		t.Fatal("inputs before any tune")
	}
	for i := 0; i < 2; i++ {
		if _, te := fx.tune("u"+string(rune('a'+i)), fx.chans[i], "phone"); te != nil {
			t.Fatalf("tune %d: %v", i, te)
		}
	}
	if _, te := fx.tune("uc", fx.chans[2], "phone"); te == nil || te.Status != 429 || te.Code != "remote_channels_full" {
		t.Fatalf("third channel: %+v", te)
	}
	if len(fx.f.Inputs) != 2 {
		t.Fatalf("inputs at Cloudflare = %d, want 2", len(fx.f.Inputs))
	}
	names := map[string]bool{}
	for uid, in := range fx.f.Inputs {
		names[in.Name()] = true
		if in.Recording == nil || in.Recording.Mode != "automatic" || !in.Recording.RequireSignedURLs || in.Recording.TimeoutSeconds != 30 {
			t.Errorf("input recording %+v", in.Recording)
		}
		if !in.PreferLowLatency {
			t.Error("low_latency not passed")
		}
		if !fx.f.Enabled(uid) {
			t.Error("an assigned input is enabled")
		}
		b, err := fx.store.Get(context.Background(), tvremote.SecretInputPrefix+uid)
		if err != nil || !strings.Contains(string(b), in.RTMPS.StreamKey) {
			t.Errorf("stream key not in home_secrets: %v", err)
		}
	}
	if !names["maktaba-1a2b3c4d-1"] || !names["maktaba-1a2b3c4d-2"] {
		t.Errorf("names %v", names)
	}
	var ingest string
	_ = fx.db.QueryRow(`SELECT group_concat(ingest) FROM tv_remote_inputs`).Scan(&ingest)
	for _, in := range fx.f.Inputs {
		if strings.Contains(ingest, in.RTMPS.StreamKey) {
			t.Error("a stream key is in tv_remote_inputs")
		}
	}
	// the running push has its ingest secret for the relay
	var n int
	_ = fx.db.QueryRow(`SELECT count(*) FROM tv_push_secrets`).Scan(&n)
	if n != 2 {
		t.Errorf("push secrets = %d", n)
	}
}

// TC1: lowering max_channels deletes idle inputs above it.
func TestPoolShrinkDeletesIdle(t *testing.T) {
	fx := newRemote(t)
	fx.f.StorageLimit = 10000
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = fx.tune("u"+string(rune('a'+i)), fx.chans[i], "")
	}
	// end everything: sessions, pushes (never went live: retried then failed)
	fx.exec(`UPDATE tv_remote_sessions SET ended_at = '2026-10-19T12:00:00Z'`)
	fx.exec(`UPDATE tv_remote_pushes SET state = 'done'`)
	fx.exec(`UPDATE tv_remote_inputs SET state = 'idle', channel_id = NULL`)
	fx.rules(map[string]any{"max_channels": 1})
	fx.reconcile()
	if len(fx.f.Inputs) != 1 {
		t.Fatalf("inputs at Cloudflare = %d, want 1", len(fx.f.Inputs))
	}
	var rows int
	_ = fx.db.QueryRow(`SELECT count(*) FROM tv_remote_inputs`).Scan(&rows)
	if rows != 1 {
		t.Errorf("pool rows = %d", rows)
	}
	for uid := range fx.f.Inputs {
		if ok, _ := fx.store.Has(ctx, tvremote.SecretInputPrefix+uid); !ok {
			t.Error("the kept input lost its key")
		}
	}
}

// TC2 + TC5: requested → connecting → live → stopping → cleanup → done; the
// recording is deleted once it left live-inprogress; the input is idle again.
func TestPushStateMachine(t *testing.T) {
	fx := newRemote(t)
	ch := fx.chans[0]
	r, te := fx.tune("u1", ch, "phone")
	if te != nil || r.State != "starting" {
		t.Fatalf("tune %+v %v", r, te)
	}
	p := fx.last(ch)
	if p.State != tvremote.PushRequested {
		t.Fatalf("state %s", p.State)
	}
	uid := fx.cfInput(p.ID)
	fx.relay(p.ID, "connecting")
	fx.reconcile() // not live yet at Cloudflare
	if fx.last(ch).State != tvremote.PushConnecting {
		t.Fatalf("state %s", fx.last(ch).State)
	}
	video := fx.f.GoLive(uid)
	fx.reconcile()
	p = fx.last(ch)
	if p.State != tvremote.PushLive || p.Video != video {
		t.Fatalf("live: %+v", p)
	}
	// the viewer leaves; the push lives on for idle_timeout (3 min)
	if err := fx.c.End(context.Background(), viewer("u1"), r.SessionID); err != nil {
		t.Fatal(err)
	}
	fx.reconcile()
	fx.advance(2 * time.Minute)
	fx.reconcile()
	if fx.last(ch).State != tvremote.PushLive {
		t.Fatal("stopped before idle_timeout")
	}
	fx.advance(61 * time.Second)
	fx.reconcile()
	if p = fx.last(ch); p.State != tvremote.PushStopping || p.Reason != "idle" {
		t.Fatalf("after idle: %+v", p)
	}
	fx.relay(p.ID, "stopped")
	fx.reconcile()
	if p = fx.last(ch); p.State != tvremote.PushCleanup {
		t.Fatalf("after relay stop: %+v", p)
	}
	if fx.f.Enabled(uid) {
		t.Error("the input stays enabled after the push")
	}
	var secrets int
	_ = fx.db.QueryRow(`SELECT count(*) FROM tv_push_secrets`).Scan(&secrets)
	if secrets != 0 {
		t.Error("the push's ingest secret survived the push")
	}
	// the broadcast is still in progress at Cloudflare: wait
	fx.reconcile()
	if fx.f.Videos[video] == nil {
		t.Fatal("recording deleted while still live-inprogress")
	}
	fx.f.EndLive(uid)
	fx.reconcile()
	if fx.f.Videos[video] != nil {
		t.Fatal("recording not deleted")
	}
	if p = fx.last(ch); p.State != tvremote.PushDone {
		t.Fatalf("final %+v", p)
	}
	var st string
	_ = fx.db.QueryRow(`SELECT state FROM tv_remote_inputs`).Scan(&st)
	if st != tvremote.InputIdle {
		t.Errorf("input %s", st)
	}
}

// AC8: a recording stuck in live-inprogress is deleted after RecordingWait.
func TestRecordingDeletedAfterStop(t *testing.T) {
	fx := newRemote(t)
	ch := fx.chans[0]
	r, _ := fx.tune("u1", ch, "")
	p := fx.goLive(ch)
	_ = fx.c.End(context.Background(), viewer("u1"), r.SessionID)
	_ = fx.c.StopPush(context.Background(), p.ID)
	fx.advance(tvremote.StopTimeout + time.Second) // the relay never said "stopped"
	fx.reconcile()
	if fx.last(ch).State != tvremote.PushCleanup {
		t.Fatalf("state %s", fx.last(ch).State)
	}
	fx.advance(tvremote.RecordingWait + time.Second)
	fx.reconcile()
	if fx.f.Videos[p.Video] != nil || fx.last(ch).State != tvremote.PushDone {
		t.Fatalf("recording %v, state %s", fx.f.Videos[p.Video] != nil, fx.last(ch).State)
	}
}

// AC8: the startup sweep adopts Maktaba's inputs and deletes leftover recordings.
func TestStartupSweep(t *testing.T) {
	fx := newRemote(t)
	off := false
	cl := fx.f.Client("acc1", "tok-secret")
	ctx := context.Background()
	mine, _ := cl.CreateLiveInput(ctx, cf.CreateLiveInput{Meta: map[string]any{"name": tvremote.PoolInputName}, Enabled: &off})
	other, _ := cl.CreateLiveInput(ctx, cf.CreateLiveInput{Meta: map[string]any{"name": "maktaba-ffffffff-1"}, Enabled: &off})
	stale := fx.f.AddVideo(mine.UID, "ready")
	foreign := fx.f.AddVideo(other.UID, "ready")
	if err := fx.c.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = fx.db.QueryRow(`SELECT count(*) FROM tv_remote_inputs WHERE cf_uid = $1`, mine.UID).Scan(&n)
	if n != 1 {
		t.Fatal("the 32.6 pool input was not adopted")
	}
	_ = fx.db.QueryRow(`SELECT count(*) FROM tv_remote_inputs WHERE cf_uid = $1`, other.UID).Scan(&n)
	if n != 0 {
		t.Fatal("another home's input was adopted")
	}
	if fx.f.Videos[stale.UID] != nil {
		t.Error("a leftover recording of a Maktaba input survived the sweep")
	}
	if fx.f.Videos[foreign.UID] == nil {
		t.Error("another home's recording was deleted")
	}
	if ok, _ := fx.store.Has(ctx, tvremote.SecretInputPrefix+mine.UID); !ok {
		t.Error("the adopted input's key is not in home_secrets")
	}
	// the next tune uses the adopted input
	_, _ = fx.tune("u1", fx.chans[0], "")
	if len(fx.f.Inputs) != 2 || fx.cfInput(fx.last(fx.chans[0]).ID) != mine.UID {
		t.Errorf("the adopted input was not reused (%d inputs)", len(fx.f.Inputs))
	}
}

// TC4 / AC6: concurrent tunes of one channel make one push.
func TestOnePushPerChannel(t *testing.T) {
	fx := newRemote(t)
	ch := fx.chans[0]
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := fx.c.Tune(context.Background(), viewer("user-"+string(rune('a'+i))), ch, "tv"); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if ps := fx.pushes(ch); len(ps) != 1 {
		t.Fatalf("pushes %d, want 1", len(ps))
	}
	var n int
	_ = fx.db.QueryRow(`SELECT count(DISTINCT push_id) FROM tv_remote_sessions`).Scan(&n)
	if n != 1 || len(fx.f.Inputs) != 1 {
		t.Errorf("sessions on %d pushes, %d inputs", n, len(fx.f.Inputs))
	}
}

// TC6 / AC9: a broadcast past max_push_hours with viewers is rotated: the
// next one on the same input after the recording is gone; sessions follow and
// get the new video's token at their next heartbeat.
func TestRotateAtMaxPushHours(t *testing.T) {
	fx := newRemote(t)
	ch := fx.chans[0]
	ctx := context.Background()
	r, _ := fx.tune("u1", ch, "")
	first := fx.goLive(ch)
	uid := fx.cfInput(first.ID)
	for i := 0; i < 8; i++ { // eight hours of heartbeats
		fx.advance(time.Hour)
		if _, err := fx.c.Heartbeat(ctx, viewer("u1"), r.SessionID); err != nil {
			t.Fatal(err)
		}
		fx.reconcile()
	}
	p := fx.pushes(ch)[0]
	if p.State != tvremote.PushStopping || p.Reason != "rotate" {
		t.Fatalf("after 8 h: %+v", p)
	}
	fx.relay(p.ID, "stopped")
	fx.reconcile()
	fx.f.EndLive(uid)
	fx.reconcile()
	ps := fx.pushes(ch)
	if len(ps) != 2 || ps[0].State != tvremote.PushDone || ps[1].State != tvremote.PushRequested || ps[1].Input != first.Input {
		t.Fatalf("rotation: %+v", ps)
	}
	if fx.f.Videos[first.Video] != nil {
		t.Error("the rotated broadcast's recording survived")
	}
	b, err := fx.c.Heartbeat(ctx, viewer("u1"), r.SessionID)
	if err != nil || b.State != "starting" {
		t.Fatalf("heartbeat during the rotation: %+v %v", b, err)
	}
	second := fx.goLive(ch)
	b, err = fx.c.Heartbeat(ctx, viewer("u1"), r.SessionID)
	if err != nil || b.Token == "" {
		t.Fatalf("no new token after the rotation: %+v %v", b, err)
	}
	if c, _ := cfClaims(b.Token); c.Sub != second.Video || second.Video == first.Video {
		t.Errorf("token for %s, want the new video %s", c.Sub, second.Video)
	}
}

// AC10: a failover (the placement moves) keeps the push; the row follows.
func TestPushFollowsPlacement(t *testing.T) {
	fx := newRemote(t)
	ch := fx.chans[0]
	_, _ = fx.tune("u1", ch, "")
	fx.exec(`INSERT INTO channel_placements (channel_id, host_id, generation, state) VALUES ($1, 'node-pc', 7, 'live')`, ch)
	p := fx.goLive(ch)
	fx.exec(`UPDATE channel_placements SET host_id = 'home', generation = 9, reason = 'failover' WHERE channel_id = $1`, ch)
	fx.reconcile()
	var host string
	var gen int64
	_ = fx.db.QueryRow(`SELECT host_id, generation FROM tv_remote_pushes WHERE id = $1`, p.ID).Scan(&host, &gen)
	if host != "home" || gen != 9 {
		t.Fatalf("push on %s/%d", host, gen)
	}
	if q := fx.last(ch); q.ID != p.ID || q.State != tvremote.PushLive || q.Video != p.Video {
		t.Fatalf("the push changed on failover: %+v", q)
	}
}

// AC5: no "live" within 30 s → one retry on the same input, then failed.
func TestConnectTimeoutRetriesOnce(t *testing.T) {
	fx := newRemote(t)
	ch := fx.chans[0]
	_, _ = fx.tune("u1", ch, "")
	p := fx.last(ch)
	fx.relay(p.ID, "connecting")
	fx.advance(tvremote.ConnectTimeout + time.Second)
	fx.reconcile()
	ps := fx.pushes(ch)
	if len(ps) != 2 || ps[0].Reason != "retried" || ps[1].Attempt != 2 || ps[1].Input != ps[0].Input {
		t.Fatalf("retry: %+v", ps)
	}
	fx.relay(ps[1].ID, "connecting")
	fx.advance(tvremote.ConnectTimeout + time.Second)
	fx.reconcile()
	if q := fx.last(ch); q.State != tvremote.PushFailed || q.Reason != "failed" {
		t.Fatalf("second timeout: %+v", q)
	}
	// "remote failed" for 10 minutes
	if _, te := fx.tune("u2", ch, ""); te == nil || te.Code != "remote_failed" {
		t.Fatalf("tune after failure: %+v", te)
	}
	fx.advance(tvremote.FailCooldown + time.Second)
	if _, te := fx.tune("u2", ch, ""); te != nil {
		t.Fatalf("tune after the cooldown: %+v", te)
	}
}

// TC8: each ERR_* code maps to its action.
func TestErrorsMapToActions(t *testing.T) {
	for code, want := range map[string]string{
		"ERR_STORAGE_QUOTA_EXHAUSTED": tvremote.ActionUnavailable,
		"ERR_MISSING_SUBSCRIPTION":    tvremote.ActionUnavailable,
		"ERR_GOP_OUT_OF_RANGE":        tvremote.ActionFreshInput,
		"ERR_UNSUPPORTED_VIDEO_CODEC": tvremote.ActionFreshInput,
		"ERR_UNSUPPORTED_AUDIO_CODEC": tvremote.ActionFreshInput,
		"ERR_UNHEALTHY":               tvremote.ActionFreshInput,
		"ERR_SOMETHING_NEW":           tvremote.ActionNone,
	} {
		if got := tvremote.Action(code); got != want {
			t.Errorf("%s → %q, want %q", code, got, want)
		}
	}

	t.Run("codec error: retry on a fresh input", func(t *testing.T) {
		fx := newRemote(t)
		ch := fx.chans[0]
		_, _ = fx.tune("u1", ch, "")
		p := fx.last(ch)
		uid := fx.cfInput(p.ID)
		fx.relay(p.ID, "connecting")
		fx.f.Mu.Lock()
		fx.f.Status = map[string]json.RawMessage{uid: json.RawMessage(`{"current":{"state":"errored","reason":"ERR_GOP_OUT_OF_RANGE"}}`)}
		fx.f.Mu.Unlock()
		fx.reconcile()
		ps := fx.pushes(ch)
		if len(ps) != 2 || ps[1].Input == ps[0].Input || ps[1].Attempt != 2 {
			t.Fatalf("pushes %+v", ps)
		}
		if _, ok := fx.f.Inputs[uid]; ok {
			t.Error("the errored input is still at Cloudflare")
		}
		var st, e string
		_ = fx.db.QueryRow(`SELECT state, error FROM tv_remote_inputs WHERE cf_uid = $1`, uid).Scan(&st, &e)
		if st != tvremote.InputErrored || e != "ERR_GOP_OUT_OF_RANGE" {
			t.Errorf("errored input row %s %s", st, e)
		}
	})

	t.Run("storage quota: remote unavailable", func(t *testing.T) {
		fx := newRemote(t)
		ch := fx.chans[0]
		_, _ = fx.tune("u1", ch, "")
		p := fx.last(ch)
		uid := fx.cfInput(p.ID)
		fx.relay(p.ID, "connecting")
		// the webhook path: the error is recorded on the input
		fx.exec(`UPDATE tv_remote_inputs SET error = 'ERR_STORAGE_QUOTA_EXHAUSTED', error_at = $1 WHERE cf_uid = $2`,
			fx.clock().Add(time.Second).Format(time.RFC3339Nano), uid)
		fx.reconcile()
		if q := fx.last(ch); q.State != tvremote.PushFailed {
			t.Fatalf("push %+v", q)
		}
		_, te := fx.tune("u2", fx.chans[1], "")
		if te == nil || te.Code != "remote_unavailable" || !strings.Contains(te.Detail, "storage") {
			t.Fatalf("tune: %+v", te)
		}
	})
}

// a relay that cannot keep up reports a fatal error: the push fails at once.
func TestRelayFatalFailsPush(t *testing.T) {
	fx := newRemote(t)
	ch := fx.chans[0]
	_, _ = fx.tune("u1", ch, "")
	p := fx.goLive(ch)
	fx.exec(`UPDATE tv_remote_pushes SET relay_state = 'error', relay_detail = 'fatal: uplink too slow for 6128 kbit/s' WHERE id = $1`, p.ID)
	fx.reconcile()
	var st, d string
	_ = fx.db.QueryRow(`SELECT state, detail FROM tv_remote_pushes WHERE id = $1`, p.ID).Scan(&st, &d)
	if st != tvremote.PushFailed || !strings.Contains(d, "uplink too slow") {
		t.Fatalf("%s %s", st, d)
	}
}
