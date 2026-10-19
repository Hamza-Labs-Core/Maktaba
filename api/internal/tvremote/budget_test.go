package tvremote_test

import (
	"context"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

// recNotifier records the notices a controller sends to the bell.
type recNotifier struct {
	mu   sync.Mutex
	sent []sentNotice
}

type sentNotice struct {
	kind string
	p    notify.Payload
}

func (r *recNotifier) Notify(_ context.Context, kind string, p notify.Payload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, sentNotice{kind, p})
	return nil
}

func (r *recNotifier) all() []sentNotice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sentNotice(nil), r.sent...)
}

// TC1 (32.10): the rules section's ranges; a bad value keeps its default.
func TestRemoteSettingsValidation(t *testing.T) {
	d := tvremote.DefaultSettings()
	if d.MaxChannels != 22 || d.MaxStreamsPerUser != 2 || d.Viewers != "all" || d.StoragePlanMinutes != 1000 ||
		d.BudgetAction != "block_new" || d.IdleTimeout != 3*time.Minute || d.TokenTTL != time.Hour || d.MaxPushHours != 8 ||
		d.MonthlyBudgetUSD != 10 || d.Ingest != "rtmps" {
		t.Fatalf("owner defaults %+v", d)
	}
	s := tvremote.ParseSettings([]byte(`{"max_channels": 50, "max_streams_per_user": 10, "idle_timeout_minutes": 60,
		"token_ttl_minutes": 1440, "max_push_hours": 24, "monthly_budget_usd": 0, "allowed_countries": ["sa", "eg", "SA"],
		"viewers": "listed", "viewer_users": ["amal", " "], "budget_action": "warn", "ingest": "srt"}`))
	if len(s.Bad) != 0 || s.MaxChannels != 50 || s.MaxStreamsPerUser != 10 || s.TokenTTL != 24*time.Hour ||
		!reflect.DeepEqual(s.AllowedCountries, []string{"SA", "EG"}) || !reflect.DeepEqual(s.ViewerUsers, []string{"amal"}) ||
		s.BudgetAction != "warn" || s.Ingest != "srt" || s.MonthlyBudgetUSD != 0 {
		t.Fatalf("in range: %+v", s)
	}
	bad := tvremote.ParseSettings([]byte(`{"max_channels": 51, "max_streams_per_user": 0, "idle_timeout_minutes": 61,
		"token_ttl_minutes": 4, "max_push_hours": 25, "monthly_budget_usd": -1, "delivery_usd_per_1000_min": -2,
		"allowed_countries": ["SAU"], "viewers": "friends", "budget_action": "stop", "ingest": "webrtc", "max_channels_x": 1}`))
	want := []string{"allowed_countries", "budget_action", "delivery_usd_per_1000_min", "idle_timeout_minutes", "ingest",
		"max_channels", "max_channels_x", "max_push_hours", "max_streams_per_user", "monthly_budget_usd", "token_ttl_minutes", "viewers"}
	if !reflect.DeepEqual(bad.Bad, want) {
		t.Fatalf("bad %v", bad.Bad)
	}
	if bad.MaxChannels != 22 || bad.TokenTTL != time.Hour || bad.IdleTimeout != 3*time.Minute || bad.AllowedCountries != nil {
		t.Errorf("defaults not kept: %+v", bad)
	}
}

// TC2: the month's minutes come from Cloudflare's analytics for the video ids
// of this month's pushes.
func TestUsageFromAnalytics(t *testing.T) {
	fx := newRemote(t)
	ctx := context.Background()
	fx.liveTune("u", fx.chans[0])
	p := fx.last(fx.chans[0])
	fx.f.MinutesByUID = map[string]float64{p.Video: 250, "someone-elses-video": 9999}
	cl := fx.f.Client("acc1", "tok-secret")
	s := tvremote.DefaultSettings()
	if err := fx.c.PollUsage(ctx, cl, s, fx.clock()); err != nil {
		t.Fatal(err)
	}
	e, err := fx.c.Estimate(ctx, s, fx.clock())
	if err != nil {
		t.Fatal(err)
	}
	if e.Source != "analytics" || e.DeliveredMinutes != 250 {
		t.Fatalf("estimate %+v", e)
	}
	// later data never lowers the day (EC2)
	fx.f.MinutesByUID = map[string]float64{p.Video: 100}
	_ = fx.c.PollUsage(ctx, cl, s, fx.clock())
	e, _ = fx.c.Estimate(ctx, s, fx.clock())
	if e.DeliveredMinutes != 250 {
		t.Errorf("the estimate went down: %v", e.DeliveredMinutes)
	}
	var m float64
	_ = fx.db.QueryRow(`SELECT minutes_delivered FROM tv_remote_pushes WHERE id = $1`, p.ID).Scan(&m)
	if m != 100 && m != 250 {
		t.Errorf("push minutes %v", m)
	}
}

// TC2: without Account Analytics the minutes are the sessions' minutes.
func TestUsageFromSessionsFallback(t *testing.T) {
	fx := newRemote(t)
	ctx := context.Background()
	fx.f.AnalyticsDenied = true
	r := fx.liveTune("u", fx.chans[0])
	fx.advance(40 * time.Minute)
	_, _ = fx.c.Heartbeat(ctx, viewer("u"), r.SessionID)
	_ = fx.c.End(ctx, viewer("u"), r.SessionID)
	s := tvremote.DefaultSettings()
	if err := fx.c.PollUsage(ctx, fx.f.Client("acc1", "tok-secret"), s, fx.clock()); err != nil {
		t.Fatal(err)
	}
	e, _ := fx.c.Estimate(ctx, s, fx.clock())
	if e.Source != "sessions" || math.Abs(e.DeliveredMinutes-40) > 0.01 {
		t.Fatalf("estimate %+v", e)
	}
	// $5 storage + 40 min × $1/1000
	if math.Abs(e.TotalUSD-5.04) > 0.001 {
		t.Errorf("total %v", e.TotalUSD)
	}
}

// TC3: 79 %, 80 %, 100 %; block_new vs warn; budget 0; raising the budget.
func TestBudgetThresholds(t *testing.T) {
	ctx := context.Background()
	// storage $5 of a $10 budget is 50 %; each 1,000 minutes delivered adds 10 %
	at := func(fx *remoteFx, minutes float64) {
		fx.exec(`INSERT INTO tv_remote_usage_daily (day, minutes_delivered, source) VALUES ($1, $2, 'analytics')
			ON CONFLICT (day) DO UPDATE SET minutes_delivered = excluded.minutes_delivered`, fx.clock().Format("2006-01-02"), minutes)
	}
	notice := func(fx *remoteFx) int {
		var raw string
		if fx.db.QueryRow(`SELECT value FROM app_settings WHERE key = $1`, tvremote.SettingBudgetNotice).Scan(&raw) != nil {
			return 0
		}
		var n struct {
			Level int `json:"level"`
		}
		_ = jsonUnmarshal(raw, &n)
		return n.Level
	}
	fx := newRemote(t)
	bell := &recNotifier{}
	fx.c.Notifier = bell
	r := fx.liveTune("running", fx.chans[0])
	at(fx, 2900) // 79 %
	fx.reconcile()
	if notice(fx) != 0 || len(bell.all()) != 0 {
		t.Fatal("notice at 79 %")
	}
	if _, te := fx.tune("u2", fx.chans[1], ""); te != nil {
		t.Fatalf("79 %%: %v", te)
	}
	at(fx, 3000) // 80 %
	fx.reconcile()
	if notice(fx) != 80 {
		t.Fatalf("no 80 %% notice (%d)", notice(fx))
	}
	// the bell gets the crossing once (32.10 AC4): later ticks at 80 % send nothing
	fx.reconcile()
	fx.reconcile()
	if got := bell.all(); len(got) != 1 || got[0].kind != tvremote.NotifyBudget || got[0].p.Params["level"] != 80 ||
		got[0].p.Severity != notify.SeverityWarn || got[0].p.Link != "/admin/live-tv" || got[0].p.Subject == "" {
		t.Fatalf("80 %% bell notices %+v", got)
	}
	at(fx, 5000) // 100 %
	fx.reconcile()
	if notice(fx) != 100 {
		t.Fatal("no 100 % notice")
	}
	fx.reconcile()
	if got := bell.all(); len(got) != 2 || got[1].kind != tvremote.NotifyBudget || got[1].p.Params["level"] != 100 ||
		got[1].p.Severity != notify.SeverityError || got[1].p.Subject == got[0].p.Subject {
		t.Fatalf("100 %% bell notices %+v", got)
	}
	// a second API process over the same database repeats nothing this month
	other := &recNotifier{}
	c2 := &tvremote.Controller{DB: fx.db, Integration: fx.in, Now: fx.clock, TuneWait: -1, Notifier: other}
	_ = c2.Reconcile(ctx)
	if len(other.all()) != 0 {
		t.Fatalf("repeated by a second controller: %+v", other.all())
	}
	// a new tune is refused, also of a channel already pushing
	if _, te := fx.tune("u3", fx.chans[0], ""); te == nil || te.Status != 402 {
		t.Fatalf("100 %%: %+v", te)
	}
	// running sessions keep heartbeating and get fresh tokens
	fx.advance(31 * time.Minute)
	if b, err := fx.c.Heartbeat(ctx, viewer("running"), r.SessionID); err != nil || b.Token == "" {
		t.Fatalf("a running session was refused at 100 %%: %+v %v", b, err)
	}
	// warn: only a notice
	fx.rules(map[string]any{"budget_action": "warn"})
	if _, te := fx.tune("u3", fx.chans[0], ""); te != nil {
		t.Fatalf("warn: %v", te)
	}
	// raising the budget lifts the block at once (EC3); budget 0: no check
	fx.rules(map[string]any{"monthly_budget_usd": 100})
	if _, te := fx.tune("u4", fx.chans[0], ""); te != nil {
		t.Fatalf("raised budget: %v", te)
	}
	fx.rules(map[string]any{"monthly_budget_usd": 0})
	at(fx, 1e9)
	if _, te := fx.tune("u5", fx.chans[0], ""); te != nil {
		t.Fatalf("budget 0: %v", te)
	}
}

// TC4: the storage guard budgets each running push's whole reservation (its
// remaining minutes until rotation, not only its age), the recordings not yet
// deleted, what else the account stores and a margin, against the capacity
// Cloudflare reports.
func TestStorageGuard(t *testing.T) {
	ctx := context.Background()
	s := tvremote.DefaultSettings() // 8 h = 480 min per push, 30 min margin

	t.Run("two young pushes fill one 1,000-minute block", func(t *testing.T) {
		fx := newRemote(t)
		fx.liveTune("a", fx.chans[0])
		fx.advance(5 * time.Minute)
		fx.liveTune("b", fx.chans[1])
		fx.advance(5 * time.Minute)
		// stored now: about 15 minutes; the old guard (Σ age + 480) would let
		// a third and a fourth push start and run out of storage hours later
		_, te := fx.tune("c", fx.chans[2], "")
		if te == nil || te.Code != "remote_storage_full" {
			t.Fatalf("third push on one block: %+v", te)
		}
		g, err := fx.c.Storage(ctx, fx.f.Client("acc1", "tok-secret"), s, fx.clock())
		if err != nil {
			t.Fatal(err)
		}
		if g.CapacitySource != "cloudflare" || g.RunningPushes != 2 || g.RunningReservedMinutes != 960 || g.Fits {
			t.Errorf("guard %+v", g)
		}
		if g.StoredMinutes > 16 || g.StoredMinutes < 14 {
			t.Errorf("stored %v", g.StoredMinutes)
		}
	})

	t.Run("capacity from Cloudflare: 2,000 minutes take four", func(t *testing.T) {
		fx := newRemote(t)
		fx.f.StorageLimit = 2000
		for i := 0; i < 4; i++ {
			if _, te := fx.tune(string(rune('a'+i)), fx.chans[i], ""); te != nil {
				t.Fatalf("push %d: %v", i+1, te)
			}
		}
		fx.c.ForgetStorage()
		if _, te := fx.tune("e", fx.chans[4], ""); te == nil || te.Code != "remote_storage_full" {
			t.Fatalf("fifth: %+v", te)
		}
	})

	t.Run("recordings not yet deleted and other videos count", func(t *testing.T) {
		fx := newRemote(t)
		// an ended 300-minute broadcast whose recording is not deleted yet
		ch := fx.chans[5]
		fx.exec(`INSERT INTO tv_remote_inputs (id, cf_uid, name) VALUES ('i0', 'cf0', 'maktaba-x-0')`)
		fx.exec(`INSERT INTO tv_remote_pushes (id, channel_id, input_id, state, video_uid, started_at, state_at, live_at, ended_at)
			VALUES ('p0', $1, 'i0', 'done', 'v0', $2, $3, $2, $3)`, ch,
			fx.clock().Add(-300*time.Minute).Format(time.RFC3339Nano), fx.clock().Format(time.RFC3339Nano))
		cl := fx.f.Client("acc1", "tok-secret")
		g, _ := fx.c.Storage(ctx, cl, s, fx.clock())
		if g.PendingRecordingMinutes != 300 || !g.Fits { // 300 + 480 + 30 ≤ 1000
			t.Fatalf("guard %+v", g)
		}
		// the account also stores 200 minutes of other videos: 300 + 200 + 480 + 30 > 1000
		fx.f.StorageMinutes = 500
		fx.c.ForgetStorage()
		g, _ = fx.c.Storage(ctx, cl, s, fx.clock())
		if g.OtherMinutes != 200 || g.Fits {
			t.Fatalf("guard with other videos %+v", g)
		}
		// a lower max_push_hours fits
		s2 := s
		s2.MaxPushHours = 4
		g, _ = fx.c.Storage(ctx, cl, s2, fx.clock())
		if !g.Fits {
			t.Fatalf("4 h pushes %+v", g)
		}
	})

	t.Run("margin", func(t *testing.T) {
		fx := newRemote(t)
		fx.liveTune("a", fx.chans[0])
		s2 := s
		s2.StorageMarginMinutes = 41 // 480 + 480 + 41 > 1000
		g, _ := fx.c.Storage(ctx, fx.f.Client("acc1", "tok-secret"), s2, fx.clock())
		if g.Fits {
			t.Fatalf("margin ignored %+v", g)
		}
	})

	t.Run("Cloudflare unreadable: storage_plan_minutes", func(t *testing.T) {
		fx := newRemote(t)
		g, _ := fx.c.Storage(ctx, nil, s, fx.clock())
		if g.CapacitySource != "settings" || g.CapacityMinutes != 1000 || !g.Fits {
			t.Fatalf("%+v", g)
		}
	})
}
