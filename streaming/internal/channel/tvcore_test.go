package channel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- the NVENC session gate ---------------------------------------------

func TestEncoderGate_CapsSessionsAcrossStations(t *testing.T) {
	g := NewEncoderGate(2)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	relA, ok1 := g.Acquire(context.Background(), a, 0)
	relB, ok2 := g.Acquire(context.Background(), b, 0)
	if !ok1 || !ok2 || g.Active() != 2 {
		t.Fatalf("two slots: %v %v active=%d", ok1, ok2, g.Active())
	}
	// a station holding no slot is answered at once (software for its block)
	start := time.Now()
	if _, ok := g.Acquire(context.Background(), c, time.Second); ok {
		t.Fatal("third session granted past the cap")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Error("a station holding no slot waited")
	}
	relA(false)
	relA(false) // idempotent
	if g.Active() != 1 {
		t.Fatalf("active after release = %d", g.Active())
	}
	relC, ok := g.Acquire(context.Background(), c, 0)
	if !ok {
		t.Fatal("freed slot not granted")
	}
	relB(false)
	relC(false)
	if g.Active() != 0 {
		t.Fatalf("active = %d", g.Active())
	}
}

func TestEncoderGate_PreStartOverlapCountsAndWaitsForOwnSlot(t *testing.T) {
	g := NewEncoderGate(2)
	st, other := uuid.New(), uuid.New()
	cur, _ := g.Acquire(context.Background(), st, 0) // the block on air
	_, _ = g.Acquire(context.Background(), other, 0) // another channel
	// The next block is pre-started: the gate is full, but this station's
	// current block ends at the boundary, so the next one waits for a slot.
	done := make(chan bool, 1)
	go func() {
		_, ok := g.Acquire(context.Background(), st, 2*time.Second)
		done <- ok
	}()
	select {
	case <-done:
		t.Fatal("the overlap was not counted: a third session was granted")
	case <-time.After(100 * time.Millisecond):
	}
	cur(false) // the boundary: the old block's ffmpeg exits
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("the next block did not get the freed slot")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not woken")
	}
	if g.Active() != 2 {
		t.Errorf("active = %d, want 2", g.Active())
	}
	// a wait that runs out falls back
	if _, ok := g.Acquire(context.Background(), st, 50*time.Millisecond); ok {
		t.Error("granted past the cap after the wait")
	}
	// a cancelled block stops waiting
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := g.Acquire(ctx, st, time.Second); ok {
		t.Error("granted to a cancelled block")
	}
}

func TestEncoderGate_RefusalPausesSetMaxAndUncapped(t *testing.T) {
	g := NewEncoderGate(DefaultNVENCMaxSessions)
	now := time.Now()
	g.now = func() time.Time { return now }
	rel, _ := g.Acquire(context.Background(), uuid.New(), 0)
	rel(true) // refused by the driver
	if _, ok := g.Acquire(context.Background(), uuid.New(), 0); ok {
		t.Fatal("NVENC tried during the refusal pause")
	}
	now = now.Add(nvencRefusedPause + time.Second)
	if _, ok := g.Acquire(context.Background(), uuid.New(), 0); !ok {
		t.Fatal("NVENC not tried again after the pause")
	}
	g.SetMax(1)
	if _, ok := g.Acquire(context.Background(), uuid.New(), 0); ok || g.Free() != 0 {
		t.Fatal("lowered cap not applied")
	}
	// the VideoToolbox gate defaults to no cap
	vt := NewEncoderGate(0)
	for i := 0; i < 40; i++ {
		if _, ok := vt.Acquire(context.Background(), uuid.New(), 0); !ok {
			t.Fatalf("uncapped gate refused session %d", i)
		}
	}
	if vt.Free() != -1 || DefaultVTGate().Max() != 0 || DefaultEncoderGate().Max() != DefaultNVENCMaxSessions {
		t.Errorf("defaults: vt free %d, vt max %d, nvenc max %d", vt.Free(), DefaultVTGate().Max(), DefaultEncoderGate().Max())
	}
}

// --- encoder args -------------------------------------------------------

func TestBlockArgs_VideoToolboxLive(t *testing.T) {
	in := BlockInput{Kind: "file", Source: "/m/a.mp4", DurationSec: 10, HasVideo: true, HasAudio: true, HWDecode: true}
	prof := OutputProfile{Encoder: EncoderVideoToolbox, VideoKbps: 4000, FPS: 25, SegmentSeconds: 4, VTPrioSpeed: true}
	got := joined(BlockArgs(prof, in))
	for _, want := range []string{"-hwaccel videotoolbox", "-c:v h264_videotoolbox", "-realtime 1", "-allow_sw 0",
		"-prio_speed 1", "-b:v 4000k", "-maxrate 4000k", "-bufsize 8000k", "-g 100", "-bf 0",
		"-force_key_frames expr:gte(t,n_forced*4)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "nvenc") || strings.Contains(got, "libx264") || strings.Contains(got, "cuda") {
		t.Errorf("wrong encoder flags: %s", got)
	}
	// no hardware encoder answered: Apple's software path; an older ffmpeg: no -prio_speed
	got = joined(BlockArgs(OutputProfile{Encoder: EncoderVideoToolbox, VTSoftware: true, SegmentSeconds: 2, FPS: 30}, in))
	if !strings.Contains(got, "-allow_sw 1") || strings.Contains(got, "prio_speed") || !strings.Contains(got, "-g 60") {
		t.Errorf("software VT args: %s", got)
	}
	if p := (OutputProfile{Encoder: EncoderVideoToolbox}).normalised(); p.Encoder != EncoderVideoToolbox {
		t.Errorf("normalised dropped videotoolbox: %s", p.Encoder)
	}
	// NVENC keeps CUDA decode
	if got := joined(BlockArgs(OutputProfile{Encoder: EncoderNVENC}, in)); !strings.Contains(got, "-hwaccel cuda") {
		t.Errorf("nvenc decode: %s", got)
	}
}

// fakeProbe answers encoder checks without ffmpeg.
type fakeProbe struct {
	encodes map[string]bool // "codec" or "codec allow_sw=0"
	options map[string]bool
	calls   int
}

func (f *fakeProbe) Encodes(_ context.Context, codec string, extra ...string) bool {
	f.calls++
	key := codec
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i] == "-allow_sw" {
			key += " allow_sw=" + extra[i+1]
		}
	}
	return f.encodes[key]
}

func (f *fakeProbe) HasOption(_ context.Context, codec, option string) bool {
	return f.options[codec+" "+option]
}

func TestDetectEncoderFor_PerOS(t *testing.T) {
	ctx := context.Background()
	mac := &fakeProbe{encodes: map[string]bool{"h264_videotoolbox allow_sw=0": true},
		options: map[string]bool{"h264_videotoolbox prio_speed": true}}
	if c := DetectEncoderFor(ctx, "darwin", "auto", mac); c != (EncoderChoice{Name: EncoderVideoToolbox, VTPrioSpeed: true}) {
		t.Errorf("mac auto: %+v", c)
	}
	// a Mac without the hardware encoder (a VM): libx264, never NVENC
	vm := &fakeProbe{encodes: map[string]bool{"h264_nvenc": true}}
	if c := DetectEncoderFor(ctx, "darwin", "auto", vm); c.Name != EncoderSoftware {
		t.Errorf("mac without VT: %+v", c)
	}
	pc := &fakeProbe{encodes: map[string]bool{"h264_nvenc": true, "h264_videotoolbox allow_sw=0": true}}
	if c := DetectEncoderFor(ctx, "windows", "auto", pc); c.Name != EncoderNVENC {
		t.Errorf("pc auto: %+v", c)
	}
	if c := DetectEncoderFor(ctx, "linux", "auto", &fakeProbe{}); c.Name != EncoderSoftware {
		t.Errorf("linux without GPU: %+v", c)
	}
	// explicit videotoolbox without hardware: Apple's software encoder
	if c := DetectEncoderFor(ctx, "darwin", "videotoolbox", &fakeProbe{}); c != (EncoderChoice{Name: EncoderVideoToolbox, VTSoftware: true}) {
		t.Errorf("explicit vt: %+v", c)
	}
	for pref, want := range map[string]string{"nvenc": EncoderNVENC, "software": EncoderSoftware, "libx264": EncoderSoftware} {
		if c := DetectEncoderFor(ctx, "darwin", pref, &fakeProbe{}); c.Name != want {
			t.Errorf("%s -> %+v", pref, c)
		}
	}
	probe := &fakeProbe{}
	r := &EncoderResolver{GOOS: "linux", Probe: probe}
	r.Resolve(ctx, "auto")
	r.Resolve(ctx, " AUTO ")
	r.Resolve(ctx, "")
	if probe.calls != 1 {
		t.Errorf("auto detected %d times, want once", probe.calls)
	}
}

// --- settings -----------------------------------------------------------

func TestOutputSettings_EnvThenRules(t *testing.T) {
	t.Setenv("MAKTABA_CHANNEL_RESOLUTION", "1280x720")
	t.Setenv("MAKTABA_CHANNEL_MAX_ACTIVE", "5")
	t.Setenv("MAKTABA_CHANNEL_ENCODER", "nvenc")
	base := OutputSettingsFromEnv()
	if base.Width != 1280 || base.MaxActive != 5 || base.Encoder != "nvenc" || base.NVENCMax != DefaultNVENCMaxSessions ||
		base.VTMax != 0 || base.WarmGrace != DefaultWarmGrace || base.FPS != 30 {
		t.Fatalf("env: %+v", base)
	}
	out, bad := ApplyOutputRules(base, []byte(`{"encoder":"videotoolbox","resolution":"1920x1080","fps":25,
		"video_kbps":4000,"audio_codec":"ac3","audio_kbps":256,"segment_seconds":2,"max_active_channels":12,
		"warm_grace_seconds":0,"nvenc_max_sessions":3,"videotoolbox_max_sessions":10,"unrelated":true}`))
	if len(bad) != 0 {
		t.Fatalf("bad: %v", bad)
	}
	want := OutputSettings{Encoder: "videotoolbox", Width: 1920, Height: 1080, FPS: 25, VideoKbps: 4000,
		AudioCodec: "ac3", AudioKbps: 256, SegmentSeconds: 2, KeyintSeconds: DefaultKeyintSeconds, MaxActive: 12,
		WarmGrace: 0, NVENCMax: 3, VTMax: 10}
	if out != want {
		t.Fatalf("rules:\n got %+v\nwant %+v", out, want)
	}
	p := out.Profile(EncoderChoice{Name: EncoderVideoToolbox, VTPrioSpeed: true})
	if p.Width != 1920 || p.FPS != 25 || p.AudioCodec != "ac3" || p.Encoder != EncoderVideoToolbox || p.SegmentSeconds != 2 || !p.VTPrioSpeed {
		t.Errorf("profile: %+v", p)
	}
	// a value that cannot be used keeps the fallback, the rest applies
	out, bad = ApplyOutputRules(base, []byte(`{"encoder":"quicksync","fps":500,"resolution":"big","max_active_channels":3}`))
	if len(bad) != 3 || out.Encoder != "nvenc" || out.FPS != 30 || out.Width != 1280 || out.MaxActive != 3 {
		t.Errorf("tolerant: %+v %v", out, bad)
	}
	if out, bad := ApplyOutputRules(base, []byte(`not json`)); out != base || bad != nil {
		t.Errorf("garbage: %+v %v", out, bad)
	}
}

func TestOutputSettings_AutoMaxActiveFollowsEncoder(t *testing.T) {
	t.Setenv("MAKTABA_CHANNEL_MAX_ACTIVE", "")
	o := OutputSettingsFromEnv()
	if o.MaxActive != 0 {
		t.Fatalf("unset env must leave the cap on auto: %d", o.MaxActive)
	}
	if o.EffectiveMaxActive(EncoderVideoToolbox) != 16 || o.EffectiveMaxActive(EncoderNVENC) != 8 ||
		o.EffectiveMaxActive(EncoderSoftware) != 8 {
		t.Errorf("auto caps: vt %d nvenc %d sw %d", o.EffectiveMaxActive(EncoderVideoToolbox),
			o.EffectiveMaxActive(EncoderNVENC), o.EffectiveMaxActive(EncoderSoftware))
	}
	o.MaxActive = 20
	if o.EffectiveMaxActive(EncoderVideoToolbox) != 20 {
		t.Error("a set cap must win")
	}
}

func TestTVSettings_Horizon(t *testing.T) {
	t.Setenv("MAKTABA_CHANNEL_HORIZON_DAYS", "")
	base := TVSettingsFromEnv()
	if base.HorizonDays != 7 {
		t.Fatalf("defaults: %+v", base)
	}
	tv, bad := ApplyTVRules(base, []byte(`{"horizon_days":14,"loudnorm":false}`))
	if len(bad) != 0 || tv.HorizonDays != 14 {
		t.Fatalf("rules: %+v %v", tv, bad)
	}
	if _, bad := ApplyTVRules(base, []byte(`{"horizon_days":0}`)); len(bad) != 1 {
		t.Error("horizon 0 accepted")
	}
	t.Setenv("MAKTABA_CHANNEL_HORIZON_DAYS", "3")
	if TVSettingsFromEnv().HorizonDays != 3 {
		t.Error("env fallback")
	}
}

func TestTVSettings_GuideRules(t *testing.T) {
	base := TVSettingsFromEnv()
	if base.GuideMaxPaddingSeconds != 120 || base.GuideFillerTitle != "فواصل" || base.GuideMinProgramSeconds != 60 {
		t.Fatalf("defaults: %+v", base)
	}
	tv, bad := ApplyTVRules(base, []byte(`{"guide_max_padding_s":300,"guide_filler_title":" Breaks ","guide_min_program_s":0}`))
	if len(bad) != 0 || tv.GuideMaxPaddingSeconds != 300 || tv.GuideFillerTitle != "Breaks" || tv.GuideMinProgramSeconds != 0 {
		t.Fatalf("rules: %+v %v", tv, bad)
	}
	tv, bad = ApplyTVRules(base, []byte(`{"guide_max_padding_s":-1,"guide_filler_title":5,"guide_min_program_s":4000}`))
	if len(bad) != 3 || tv.GuideMaxPaddingSeconds != 120 || tv.GuideFillerTitle != "فواصل" || tv.GuideMinProgramSeconds != 60 {
		t.Errorf("bad values kept the defaults? %+v %v", tv, bad)
	}
}

func TestSettingsStarter_ReadsOutputAtStart(t *testing.T) {
	gate, vt := NewEncoderGate(6), NewEncoderGate(0)
	set := StaticSettings{Outs: OutputSettings{Encoder: "auto", Width: 640, Height: 360, FPS: 25, VideoKbps: 900,
		AudioCodec: "aac", AudioKbps: 96, SegmentSeconds: 2, MaxActive: 3, NVENCMax: 2, VTMax: 5}}
	enc := &EncoderResolver{GOOS: "windows", Probe: &fakeProbe{encodes: map[string]bool{"h264_nvenc": true}}}
	t.Setenv(fakeFFmpegEnv, "1")
	bin, _ := os.Executable()
	start := SettingsStarter(StationConfig{FFmpeg: bin, Repo: &syncRepo{}, Prober: stubProber{},
		Resolver: NewYtDlp(""), Logger: testLogger(t), Gate: gate, VTGate: vt}, set, enc)
	p, err := start(context.Background(), uuid.New(), filepath.Join(t.TempDir(), "hls"))
	if err != nil {
		t.Fatal(err)
	}
	st := p.(*Station)
	defer func() { st.Stop(); <-st.Done() }()
	if pr := st.cfg.Profile; pr.Width != 640 || pr.FPS != 25 || pr.Encoder != EncoderNVENC || pr.SegmentSeconds != 2 {
		t.Errorf("profile: %+v", pr)
	}
	if gate.Max() != 2 || vt.Max() != 5 {
		t.Errorf("gate max = %d / %d, want the settings' 2 / 5", gate.Max(), vt.Max())
	}
}

// --- cluster identity ---------------------------------------------------

func TestDetectRole_HomeUnlessJoined(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	files := map[string]string{}
	read := func(p string) ([]byte, error) {
		if b, ok := files[p]; ok {
			return []byte(b), nil
		}
		return nil, os.ErrNotExist
	}
	macPath := NodeConfigPath(getenv, "darwin", "/Users/m")
	if want := filepath.Join("/Users/m", "Library", "Application Support", "Maktaba", "node.json"); macPath != want {
		t.Fatalf("mac path %s", macPath)
	}
	if role, _ := DetectRole(getenv, "darwin", "/Users/m", read); role != RoleHome {
		t.Errorf("no node config: %s", role)
	}
	files[macPath] = `{"node_id":"n-1","private_key":"k","database_url":""}`
	if role, _ := DetectRole(getenv, "darwin", "/Users/m", read); role != RoleHome {
		t.Errorf("asked to join but not approved: %s", role)
	}
	files[macPath] = `{"node_id":"n-1","private_key":"k","database_url":"postgres://x"}`
	if role, id := DetectRole(getenv, "darwin", "/Users/m", read); role != RoleNode || id != "n-1" {
		t.Errorf("joined: %s %s", role, id)
	}
	env["MAKTABA_CLUSTER_ROLE"] = "home"
	if role, _ := DetectRole(getenv, "darwin", "/Users/m", read); role != RoleHome {
		t.Errorf("override: %s", role)
	}
	env["MAKTABA_NODE_CONFIG"] = "/tmp/n.json"
	if NodeConfigPath(getenv, "linux", "/home/u") != "/tmp/n.json" {
		t.Error("MAKTABA_NODE_CONFIG ignored")
	}
	if !(HostIdentity{}).IsHome() || (HostIdentity{Role: RoleNode}).IsHome() {
		t.Error("IsHome")
	}
}

// --- engine limits + runtime -------------------------------------------

type runtimeRepo struct {
	fakeRepo
	rows        []Runtime
	clearedHost string
	owner       *Runtime
	ownerBeat   time.Time
	hosts       []HostStatus
}

func (r *runtimeRepo) SetRuntime(_ context.Context, rt Runtime) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, rt)
	return nil
}

func (r *runtimeRepo) ClearHostRuntime(_ context.Context, hostID, host string) (int64, error) {
	r.clearedHost = hostID + "/" + host
	return 2, nil
}

func (r *runtimeRepo) RuntimeOf(_ context.Context, id uuid.UUID) (Runtime, time.Time, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner == nil || r.owner.ChannelID != id {
		return Runtime{}, time.Time{}, false, nil
	}
	return *r.owner, r.ownerBeat, true, nil
}

func (r *runtimeRepo) SetHostStatus(_ context.Context, st HostStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts = append(r.hosts, st)
	return nil
}

type statsPipe struct{ *fakePipeline }

func (statsPipe) Stats() StationStats {
	return StationStats{Encoder: EncoderNVENC, Restarts: 2, NVENCFallbacks: 5}
}

func TestEngine_LimitsRuntimeAndStaleRows(t *testing.T) {
	repo := &runtimeRepo{}
	root := t.TempDir()
	start := func(_ context.Context, _ uuid.UUID, dir string) (Pipeline, error) {
		return statsPipe{newFakePipeline(dir, 0)}, nil
	}
	e := NewEngine(repo, fakeLayout{root: root}, start, 8, nil)
	e.Host = "pc"
	e.Identity = HostIdentity{ID: "home-1", Host: "pc", Role: RoleHome, StreamBaseURL: "http://pc:8781"}
	e.Status = func(context.Context) HostStatus { return HostStatus{Encoder: EncoderNVENC, NVENCFree: 4, VTFree: -1} }
	maxActive := 1
	e.Limits = func(context.Context) (int, time.Duration) { return maxActive, time.Hour }
	if n, err := e.ClearStaleRuntime(context.Background()); n != 2 || err != nil || repo.clearedHost != "home-1/pc" {
		t.Fatalf("stale clear: %d %v %q", n, err, repo.clearedHost)
	}
	a, b := uuid.New(), uuid.New()
	if _, err := e.Ensure(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.StopAll)
	e.reg.attach(a) // a viewer: not evictable
	if _, err := e.Ensure(context.Background(), b); err != ErrAtCapacity {
		t.Fatalf("cap from settings not applied: %v", err)
	}
	maxActive = 2
	if _, err := e.Ensure(context.Background(), b); err != nil {
		t.Fatalf("raised cap: %v", err)
	}
	if reaped := e.ReapIdle(context.Background()); len(reaped) != 0 {
		t.Errorf("the settings' warm grace not used: reaped %v", reaped)
	}
	e.SyncRuntime(context.Background())
	e.FlushRuntime(2 * time.Second) // rows are written off the engine lock
	repo.mu.Lock()
	defer repo.mu.Unlock()
	var got *Runtime
	for i := range repo.rows {
		if repo.rows[i].ChannelID == a {
			got = &repo.rows[i]
		}
	}
	if got == nil || got.Host != "pc" || got.HostID != "home-1" || got.StreamBaseURL != "http://pc:8781" ||
		got.Encoder != EncoderNVENC || got.Restarts != 2 ||
		got.NVENCFallbacks != 5 || got.ViewerCount != 1 || got.StartedAt.IsZero() {
		t.Fatalf("runtime row: %+v", got)
	}
	if len(repo.hosts) != 1 {
		t.Fatalf("host status rows: %d", len(repo.hosts))
	}
	if h := repo.hosts[0]; h.Identity.ID != "home-1" || h.MaxActive != 2 || h.ActiveChannels != 2 ||
		h.NVENCFree != 4 || h.VTFree != -1 || h.Encoder != EncoderNVENC {
		t.Errorf("host status: %+v", h)
	}
}

func TestEngine_RefusesAChannelLiveOnAnotherHost(t *testing.T) {
	repo := &runtimeRepo{}
	now := time.Now()
	start := func(_ context.Context, _ uuid.UUID, dir string) (Pipeline, error) {
		return newFakePipeline(dir, 0), nil
	}
	e := NewEngine(repo, fakeLayout{root: t.TempDir()}, start, 8, func() time.Time { return now })
	e.Identity = HostIdentity{ID: "home-1", Role: RoleHome}
	t.Cleanup(e.StopAll)
	ch := uuid.New()
	repo.owner = &Runtime{ChannelID: ch, HostID: "node-pc", Host: "pc", StreamBaseURL: "http://pc:8781"}
	repo.ownerBeat = now.Add(-5 * time.Second)
	_, err := e.Ensure(context.Background(), ch)
	var re *RunningElsewhereError
	if !errors.Is(err, ErrRunningElsewhere) || !errors.As(err, &re) || re.StreamBaseURL != "http://pc:8781" {
		t.Fatalf("live elsewhere: %v", err)
	}
	// the other host stopped heartbeating: its row no longer owns the channel
	repo.ownerBeat = now.Add(-time.Minute)
	if _, err := e.Ensure(context.Background(), ch); err != nil {
		t.Fatalf("stale owner blocked the start: %v", err)
	}
	// our own row never blocks
	ch2 := uuid.New()
	repo.owner = &Runtime{ChannelID: ch2, HostID: "home-1"}
	repo.ownerBeat = now
	if _, err := e.Ensure(context.Background(), ch2); err != nil {
		t.Fatalf("own row blocked: %v", err)
	}
}

// --- a station's blocks against the gate (fake ffmpeg) ------------------

func gatedStation(t *testing.T, mode string, gate *EncoderGate, blocks []ProgramBlock) *Station {
	t.Helper()
	t.Setenv(fakeFFmpegEnv, mode)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	st, err := StartStation(StationConfig{
		FFmpeg: bin, Profile: OutputProfile{Width: 320, Height: 180, FPS: 30, Encoder: EncoderNVENC,
			SegmentSeconds: 1, HLSListSize: 1},
		Repo: &syncRepo{blocks: blocks}, Prober: stubProber{}, Resolver: NewYtDlp(""),
		Logger: testLogger(t), Gate: gate, NVENCWait: 100 * time.Millisecond,
	}, uuid.New(), filepath.Join(t.TempDir(), "hls"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.Stop()
		<-st.Done()
	})
	return st
}

func blocksLog(st *Station) string {
	b, _ := os.ReadFile(filepath.Join(st.Dir(), "blocks.log"))
	return string(b)
}

func programBlocks(t *testing.T) []ProgramBlock {
	now := time.Now()
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	_ = os.WriteFile(clip, []byte("x"), 0o644)
	return []ProgramBlock{
		{Seq: 1, Kind: KindProgram, Path: clip, StartAt: now.Add(-time.Second), EndAt: now.Add(2 * time.Second),
			SourceDurationMS: 3_000},
		{Seq: 2, Kind: KindProgram, Path: clip, StartAt: now.Add(2 * time.Second), EndAt: now.Add(time.Minute),
			SourceDurationMS: 58_000},
	}
}

func TestStation_FullGateEncodesOneBlockInSoftwareThenBackToNVENC(t *testing.T) {
	if testing.Short() {
		// waits for two wall-clock block boundaries (~1-2 s): over the unit tier's
		// per-test cap, so it runs in the integration tier (no -short)
		t.Skip("block-boundary timing test skipped in -short mode")
	}
	gate := NewEncoderGate(1)
	release, _ := gate.Acquire(context.Background(), uuid.New(), 0) // another channel holds the only session
	now := time.Now()
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	_ = os.WriteFile(clip, []byte("x"), 0o644)
	blocks := []ProgramBlock{
		{Seq: 1, Kind: KindProgram, Path: clip, StartAt: now.Add(-time.Second), EndAt: now.Add(time.Second), SourceDurationMS: 2_000},
		{Seq: 2, Kind: KindProgram, Path: clip, StartAt: now.Add(time.Second), EndAt: now.Add(4 * time.Second), SourceDurationMS: 3_000},
		{Seq: 3, Kind: KindProgram, Path: clip, StartAt: now.Add(4 * time.Second), EndAt: now.Add(time.Minute), SourceDurationMS: 56_000},
	}
	st := gatedStation(t, "1", gate, blocks)
	waitForT(t, 3*time.Second, func() bool { return strings.Contains(blocksLog(st), "enc=software") })
	if st.Stats().NVENCFallbacks < 1 || st.Stats().Encoder != EncoderSoftware {
		t.Fatalf("stats: %+v", st.Stats())
	}
	release(false) // the session frees: a later block goes back to the GPU
	waitForT(t, 8*time.Second, func() bool { return strings.Contains(blocksLog(st), "enc=nvenc") })
	if st.Stats().Encoder != EncoderNVENC || gate.Active() != 1 {
		t.Errorf("back on NVENC: %+v active=%d\n%s", st.Stats(), gate.Active(), blocksLog(st))
	}
}

func TestStation_RefusedSessionFallsBackForTheBlock(t *testing.T) {
	gate := NewEncoderGate(4)
	st := gatedStation(t, "nvencfail", gate, programBlocks(t))
	waitForT(t, 3*time.Second, func() bool {
		l := blocksLog(st)
		return strings.Contains(l, "enc=nvenc") && strings.Contains(l, "enc=software")
	})
	if st.Stats().NVENCFallbacks < 1 {
		t.Errorf("fallback not counted: %+v", st.Stats())
	}
	waitForT(t, time.Second, func() bool { return gate.Active() == 0 })
	// the refusal pauses NVENC for everyone
	if _, ok := gate.Acquire(context.Background(), uuid.New(), 0); ok {
		t.Error("NVENC not paused after a refusal")
	}
}

func TestStation_SlateNeverTakesAnNVENCSession(t *testing.T) {
	gate := NewEncoderGate(1)
	now := time.Now()
	st := gatedStation(t, "1", gate, []ProgramBlock{{Seq: 1, Kind: KindSlate, StartAt: now, EndAt: now.Add(time.Minute)}})
	waitForT(t, 3*time.Second, func() bool { return strings.Contains(blocksLog(st), "slate") })
	if gate.Active() != 0 || st.Stats().NVENCFallbacks != 0 || !strings.Contains(blocksLog(st), "enc=software") {
		t.Errorf("slate: active=%d %+v\n%s", gate.Active(), st.Stats(), blocksLog(st))
	}
}
