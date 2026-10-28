package frontdoor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
)

// TC2: the staging budget is what live and the standby leave (design §14).
func TestWarmBudgetNVENC(t *testing.T) {
	cases := []struct {
		name                                     string
		capacity, nv, vt, live, standby, maxXtra int
		want                                     int
	}{
		{"cap 6, live 4: staging 2", 6, 0, 0, 4, 0, 0, 2},
		{"standby takes its share first", 8, 0, 0, 2, 2, 0, 4},
		{"NVENC sessions: two per channel", 8, 12, 0, 3, 1, 0, 2},
		{"VideoToolbox cap", 16, 0, 10, 2, 2, 0, 1},
		{"staging_max_extra_encodes caps it", 8, 0, 0, 1, 1, 2, 2},
		{"nothing left", 4, 0, 0, 3, 1, 0, 0},
		{"over budget never negative", 4, 0, 0, 4, 2, 0, 0},
	}
	for _, c := range cases {
		if got := stagingBudget(c.capacity, c.nv, c.vt, c.live, c.standby, c.maxXtra); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

// TC1: the warm set is demand first, then preview viewers, the probe channel and
// the changed channels not probed yet — never a removed channel — cut to the
// budget (the rest wait, rolling).
func TestWarmSetDemandProbeRolling(t *testing.T) {
	ids := make([]uuid.UUID, 7)
	for i := range ids {
		ids[i] = uuid.MustParse("00000000-0000-4000-8000-00000000000" + string(rune('1'+i)))
	}
	plan := StagingPlan{Probe: ids[2].String(), Changed: []string{ids[3].String(), ids[4].String(), ids[5].String()},
		Removed: []string{ids[6].String()}}
	demands := []ChannelDemand{{Channel: ids[1], Humans: 1}, {Channel: ids[0], Humans: 3}, {Channel: ids[6], Humans: 2}}
	warm, deferred := planStagingWarm(plan, demands, nil, map[uuid.UUID]bool{ids[3]: true}, 4, 4, nil)
	got := []string{}
	for _, w := range warm {
		got = append(got, w.Channel[len(w.Channel)-1:]+":"+w.Why)
	}
	if strings.Join(got, ",") != "1:demand,2:demand,3:probe,5:rolling" {
		t.Fatalf("warm %v", got)
	}
	if len(deferred) != 1 || deferred[0] != ids[5].String() {
		t.Fatalf("deferred %v", deferred)
	}
	// a quiet night (EC1): only the probe and the rolling probes
	warm, _ = planStagingWarm(plan, nil, nil, map[uuid.UUID]bool{}, 2, 2, nil)
	if len(warm) != 2 || warm[0].Why != WhyProbe || warm[1].Why != WhyRolling {
		t.Fatalf("quiet %v", warm)
	}
	// a preview viewer comes before the probe
	warm, _ = planStagingWarm(plan, nil, []uuid.UUID{ids[5]}, map[uuid.UUID]bool{}, 1, 1, nil)
	if len(warm) != 1 || warm[0].Why != WhyPreview {
		t.Fatalf("preview %v", warm)
	}
}

// stagingRig is the slot rig with an amber staging slot running a change.
func stagingRig(t *testing.T) (*slotRig, *fakeSlot, *cluster.Keyring, string) {
	t.Helper()
	g := newSlotRig(t)
	amber := newFakeSlot(t, "amber", 4000, g.ch.String())
	g.slots.Addrs["amber"] = amber.addr()
	change, release := uuid.NewString(), uuid.NewString()
	g.slots.mu.Lock()
	g.slots.staging = "amber"
	g.slots.stage = &stagingRun{plan: StagingPlan{ChangeID: change, ReleaseID: release, Channels: []string{g.ch.String()}},
		started: time.Now(), since: map[uuid.UUID]time.Time{}, probed: map[uuid.UUID]bool{},
		samples: map[uuid.UUID]*ChannelProbe{}, firstSeg: map[uuid.UUID]time.Time{}, previews: map[uuid.UUID]time.Time{},
		expect: Expectation(nil)}
	g.slots.mu.Unlock()
	key := []byte("0123456789abcdef0123456789abcdef")
	kr := cluster.NewKeyring(key)
	g.fd.Keyring = kr
	state := "ready"
	g.fd.PreviewLookup = func(_ context.Context, id string) (string, string) {
		if id == change {
			return state, release
		}
		return "", ""
	}
	r := chi.NewRouter()
	g.fd.Mount(r)
	g.fd.MountPreview(r)
	g.srv.Config.Handler = r
	g.slots.probeAll(context.Background())
	return g, amber, kr, change
}

// TC1 (33.6): a preview token works for an hour, for the staged change only, in
// verifying / ready / approved.
func TestPreviewTokenExpiry(t *testing.T) {
	g, _, _, change := stagingRig(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	path := "/stream/staging/channel/" + g.ch.String() + "/index.m3u8"
	try := func(tok string) int {
		code, _ := g.get(t, path+"?pt="+url.QueryEscape(tok))
		return code
	}
	if c := try(cluster.PreviewToken(key, change, time.Now().Add(time.Hour))); c != 200 {
		t.Fatalf("good token: %d", c)
	}
	if c := try(cluster.PreviewToken(key, change, time.Now().Add(-time.Second))); c != http.StatusForbidden {
		t.Errorf("expired token: %d", c)
	}
	if c := try(cluster.PreviewToken(key, change, time.Now().Add(3*time.Hour))); c != http.StatusForbidden {
		t.Errorf("a token past the hour: %d", c)
	}
	if c := try(cluster.PreviewToken([]byte("another key, another cluster.."), change, time.Now().Add(time.Hour))); c != http.StatusForbidden {
		t.Errorf("foreign key: %d", c)
	}
	if c := try(""); c != http.StatusForbidden {
		t.Errorf("no token: %d", c)
	}
	other := uuid.NewString()
	if c := try(cluster.PreviewToken(key, other, time.Now().Add(time.Hour))); c != http.StatusGone {
		t.Errorf("a change not staged: %d", c)
	}
}

// TC4 (33.6): a preview viewer warms the channel on the staging slot only — the
// front door's live demand does not move, no push starts — and the playlist's
// segments come from the staging slot with the token.
func TestPreviewNotLiveDemand(t *testing.T) {
	g, amber, _, change := stagingRig(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	tok := cluster.PreviewToken(key, change, time.Now().Add(time.Hour))
	code, body := g.get(t, "/stream/staging/channel/"+g.ch.String()+"/index.m3u8?pt="+url.QueryEscape(tok))
	if code != 200 {
		t.Fatalf("preview playlist %d %s", code, body)
	}
	if !strings.Contains(body, "s/amber/seg-") || !strings.Contains(body, "?pt=") || strings.Contains(body, "s/blue/") {
		t.Fatalf("playlist not from staging: %s", body)
	}
	if d := g.fd.Tracker.Demands(); len(d) != 0 {
		t.Fatalf("a preview made live demand: %+v", d)
	}
	amber.mu.Lock()
	warmed := false
	for _, w := range amber.warm {
		for _, c := range w.Channels {
			if c == g.ch.String() && w.Role == "staging" {
				warmed = true
			}
		}
	}
	amber.mu.Unlock()
	if !warmed {
		t.Fatal("the staging slot was never asked to warm the previewed channel")
	}
	v := g.slots.StagingView()
	if len(v.Warm) != 1 || v.Warm[0].Why != WhyPreview {
		t.Fatalf("staging warm %+v", v.Warm)
	}
	// a segment through the preview route comes from amber
	seg := ""
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "s/amber/") {
			seg = l
		}
	}
	code, got := g.get(t, "/stream/staging/channel/"+g.ch.String()+"/"+seg)
	if code != 200 || !strings.HasPrefix(got, "amber:seg-") {
		t.Fatalf("segment %d %q", code, got)
	}
	// without the token: refused
	if code, _ := g.get(t, "/stream/staging/channel/"+g.ch.String()+"/"+strings.Split(seg, "?")[0]); code != http.StatusForbidden {
		t.Fatalf("segment without a token: %d", code)
	}
	// the live path is unaffected: blue serves it
	if code, b := g.get(t, "/stream/channel/"+g.ch.String()+"/index.m3u8"); code != 200 || !strings.Contains(b, "s/blue/") {
		t.Fatalf("live %d %s", code, b)
	}
}

// EC2 (33.6): no encoder left for staging: 503 with Retry-After and the reason.
func TestPreviewBudgetExhausted(t *testing.T) {
	g, _, _, change := stagingRig(t)
	g.slots.Budget = func(context.Context) (int, int, int) { return 1, 0, 0 } // live airs the one channel
	key := []byte("0123456789abcdef0123456789abcdef")
	tok := cluster.PreviewToken(key, change, time.Now().Add(time.Hour))
	resp, err := http.Get(g.srv.URL + "/stream/staging/channel/" + g.ch.String() + "/index.m3u8?pt=" + url.QueryEscape(tok))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") == "" ||
		!strings.Contains(resp.Header.Get("X-Maktaba-Reason"), "budget 0") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
}

// TC5 (33.6): staging adds no HDHomeRun device: the front door's routes for a
// staged change are the preview's only; the discover/lineup stay the live
// device's (hdhr serves them from the live lineup; nothing here registers one).
func TestNoStagingSSDP(t *testing.T) {
	g, _, _, _ := stagingRig(t)
	r := chi.NewRouter()
	g.fd.MountPreview(r)
	for _, p := range []string{"/discover.json", "/lineup.json", "/staging/discover.json", "/staging/lineup.json"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d (a staging device?)", p, rec.Code)
		}
	}
}

// The sampler measures each warm channel on the staging slot (and on live when live
// airs it) and keeps the newest sample; the view carries the warm set, the budget,
// the rolling state and the samples (what GET /internal/tv/staging answers).
func TestStagingSampleAndView(t *testing.T) {
	g, _, _, change := stagingRig(t)
	g.slots.StreamSample = 150 * time.Millisecond
	g.slots.mu.Lock()
	g.slots.stage.plan.Probe = g.ch.String()
	g.slots.stage.plan.Changed = []string{uuid.NewString()}
	g.slots.mu.Unlock()
	g.slots.SyncWarm(context.Background())
	g.slots.sampleStaging(context.Background())
	v := g.slots.StagingView()
	if !v.Active || v.Plan.ChangeID != change || v.Slot != "amber" || !v.Healthy {
		t.Fatalf("view %+v", v)
	}
	if v.Budget != 7 || v.Capacity != 8 || v.LiveOnAir != 1 { // 8 - live 1 - standby 0 (nobody watches)
		t.Fatalf("budget %d capacity %d live %d", v.Budget, v.Capacity, v.LiveOnAir)
	}
	if len(v.Warm) != 2 || v.Warm[0].Why != WhyProbe || v.Warm[1].Why != WhyRolling || len(v.Pending) != 1 {
		t.Fatalf("warm %+v pending %v", v.Warm, v.Pending)
	}
	cp := v.Samples[g.ch.String()]
	if cp == nil || cp.Staging == nil || cp.Staging.Slot != "amber" || len(cp.Staging.Playlist.Segs) != 5 {
		t.Fatalf("sample %+v", cp)
	}
	if cp.Live == nil || cp.Live.Slot != "blue" {
		t.Fatalf("no live sample of a channel live airs: %+v", cp.Live)
	}
	if cp.Staging.Stream.Packets == 0 || cp.Staging.Stream.PATs == 0 {
		t.Fatalf("live.ts not sampled: %+v", cp.Staging.Stream)
	}
	found := map[string]bool{}
	for _, r := range cp.Results {
		found[r.Check] = true
	}
	for _, k := range []string{"channel.first_segment", "channel.wallclock", "hls.conformity", "ts.conformity", "splice.align"} {
		if !found[k] {
			t.Errorf("no %s in %+v", k, cp.Results)
		}
	}
	// a channel live does not air is never asked of live (that would start an encode)
	other := v.Samples[v.Warm[1].Channel]
	if other == nil || other.Live != nil {
		t.Fatalf("rolling sample %+v", other)
	}
	// stop: the staging slot is retired, the view empties
	if err := g.slots.StageStop(context.Background(), "another change"); err != nil || !g.slots.StagingView().Active {
		t.Fatal("a stale stop request ended the staging")
	}
	if err := g.slots.StageStop(context.Background(), change); err != nil || g.slots.StagingView().Active {
		t.Fatalf("stop: %v", err)
	}
}

// The internal staging routes are signed with the cluster stream key; StageStart
// needs staging on and built in.
func TestStagingInternalRoutes(t *testing.T) {
	g, _, kr, _ := stagingRig(t)
	r := chi.NewRouter()
	g.fd.MountInternal(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	do := func(method, path, body string, signed bool) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if signed {
			req.Header.Set(cluster.HeaderFront, kr.Sign(method, path, time.Minute))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := do("GET", "/internal/tv/staging", "", false); c != http.StatusForbidden {
		t.Fatalf("unsigned: %d", c)
	}
	if c := do("GET", "/internal/tv/staging", "", true); c != 200 {
		t.Fatalf("view: %d", c)
	}
	on := DefaultStagingSettings()
	on.Enabled = true
	g.slots.Settings = func(context.Context) StagingSettings { return on }
	plan := `{"change_id":"` + uuid.NewString() + `","release_id":"` + uuid.NewString() + `","version":"v"}`
	code := do("POST", "/internal/tv/staging/start", plan, true)
	if StagingBuild && code != 200 || !StagingBuild && code != http.StatusConflict {
		t.Fatalf("start: %d (staging built: %v)", code, StagingBuild)
	}
	if c := do("POST", "/internal/tv/staging/stop", `{}`, true); c != 200 {
		t.Fatalf("stop: %d", c)
	}
	if c := do("POST", "/internal/tv/staging/start", `{"change_id":"x"}`, true); c != http.StatusConflict {
		t.Fatalf("start without a release: %d", c)
	}
}

// stageRig is the slot rig with an amber instance and a supervisor that refuses
// the first fail starts (story 33.5 StageStart).
func stageRig(t *testing.T, fail int) (*slotRig, *atomic.Int32) {
	t.Helper()
	if !StagingBuild {
		t.Skip("staging is built with -tags tvstaging only")
	}
	g := newSlotRig(t)
	amber := newFakeSlot(t, "amber", 4000, g.ch.String())
	g.slots.Addrs["amber"] = amber.addr()
	var calls atomic.Int32
	sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n := calls.Add(1); int(n) <= fail {
			http.Error(w, "not now", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(sup.Close)
	g.slots.Supervisor = sup.URL
	return g, &calls
}

// A staging start the supervisor did not take is not remembered as staged: the
// controller's next tick (the same plan) asks the supervisor again.
func TestStageStartRetriesAfterAFailedStart(t *testing.T) {
	g, calls := stageRig(t, 1)
	plan := StagingPlan{ChangeID: uuid.NewString(), ReleaseID: uuid.NewString(), Channels: []string{g.ch.String()}}
	if _, err := g.slots.StageStart(context.Background(), plan); err == nil {
		t.Fatal("the refused start reported no error")
	}
	if c, _, _, _ := g.slots.StagingRelease(); c != "" {
		t.Fatalf("a failed start is staged (change %s)", c)
	}
	v, err := g.slots.StageStart(context.Background(), plan)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("supervisor asked %d times, want 2 (the retry must reach it)", n)
	}
	if !v.Active || v.Slot != "amber" {
		t.Fatalf("view after the retry: %+v", v)
	}
}

// The controller re-sends the plan on every tick while the probe loop plans the
// warm set from it: the plan is read under the lock (go test -race).
func TestStagingPlanUpdateNoRace(t *testing.T) {
	g, _ := stageRig(t, 0)
	plan := StagingPlan{ChangeID: uuid.NewString(), ReleaseID: uuid.NewString(), Channels: []string{g.ch.String()},
		Probe: g.ch.String(), ProbeSeconds: 5}
	if _, err := g.slots.StageStart(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			p := plan
			p.Changed = []string{uuid.NewString()}
			p.MaxExtra = i % 3
			if _, err := g.slots.StageStart(context.Background(), p); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for i := 0; i < 30; i++ {
		g.slots.SyncWarm(context.Background())
		g.slots.PreviewTouch(context.Background(), uuid.New())
	}
	close(done)
	wg.Wait()
}
