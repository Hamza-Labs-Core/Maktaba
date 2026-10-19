package frontdoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/verify"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
)

// Staging warm-up and verification (Epic 33 story 33.5, design §6). While a TV
// change is staged the change controller (the API) asks the front door to run
// its release on the third slot instance (StageStart, with the release binding
// the supervisor checks); the front door then keeps the staging warm set —
// the channels with demand on live, the preview viewers' channels (33.6), the
// probe channel and, in turn, each changed channel for verify_probe_seconds —
// within the encoder budget that live and the hot standby leave, and samples
// the staging slot's output of each warm channel (and the live slot's, to
// compare) every few seconds. The API reads the samples (GET
// /internal/tv/staging) and aggregates them with its database checks into the
// change's verify report. Staging never pushes and is never routed to viewers.

// StagingPlan is what the change controller stages.
type StagingPlan struct {
	ChangeID  string `json:"change_id"`
	ReleaseID string `json:"release_id"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	// Channels are the release's enabled channels; Changed the ones the change
	// touches (probed in turn); Removed the ones it removes (never warmed).
	Channels []string `json:"channels"`
	Changed  []string `json:"changed"`
	Removed  []string `json:"removed"`
	// Probe is the probe channel (tv_staging.probe_channel or the lowest number).
	Probe        string `json:"probe"`
	ProbeSeconds int    `json:"probe_seconds"`
	// MaxExtra caps the staging encodes (staging_max_extra_encodes; 0: what is left).
	MaxExtra int `json:"max_extra"`
}

// Warm reasons.
const (
	WhyDemand  = "demand"
	WhyPreview = "preview"
	WhyProbe   = "probe"
	WhyRolling = "rolling"
)

// previewTTL: a preview viewer keeps its channel warm on staging this long
// after its last request.
const previewTTL = 45 * time.Second

// stagingSampleEvery is the sampling period of the warm channels.
const stagingSampleEvery = 3 * time.Second

// sampleSeconds is how much of the newest output a sample covers (design
// §6.2: 10 s of TS).
const sampleSeconds = 10

// ChannelProbe is the latest measurement of one staging channel.
type ChannelProbe struct {
	Channel   string            `json:"channel"`
	Why       string            `json:"why"`
	WarmSince time.Time         `json:"warm_since"`
	At        time.Time         `json:"at"`
	Staging   *verify.Sample    `json:"staging,omitempty"`
	Live      *verify.Sample    `json:"live,omitempty"`
	Station   *slot.StationInfo `json:"station,omitempty"`
	LiveSt    *slot.StationInfo `json:"live_station,omitempty"`
	// StationAt is when the slots reported Station / LiveSt (their status probe).
	StationAt time.Time       `json:"station_at,omitempty"`
	Results   []verify.Result `json:"results"`
	// Cloudflare lists what a Cloudflare live input would refuse ("b_frames…"
	// only matters with low-latency HLS).
	Cloudflare []string `json:"cloudflare,omitempty"`
	// Params: the stream parameters that differ from live's.
	Params []string `json:"params,omitempty"`
	// FirstSegmentAfter: seconds from warm to the first segment listed.
	FirstSegmentAfter float64 `json:"first_segment_after,omitempty"`
}

// WarmEntry is one channel of the staging warm set.
type WarmEntry struct {
	Channel string    `json:"channel"`
	Why     string    `json:"why"`
	Since   time.Time `json:"since"`
}

// StagingView is GET /internal/tv/staging.
type StagingView struct {
	Active    bool         `json:"active"`
	Plan      *StagingPlan `json:"plan,omitempty"`
	Slot      string       `json:"slot,omitempty"`
	StartedAt time.Time    `json:"started_at,omitempty"`
	Healthy   bool         `json:"healthy"`
	Status    *slot.Status `json:"status,omitempty"`
	// Budget is the staging encoder budget; Capacity the host's; LiveOnAir and
	// StandbyWarm what live and the standby use (design §14).
	Budget      int                      `json:"budget"`
	Capacity    int                      `json:"capacity"`
	LiveOnAir   int                      `json:"live_on_air"`
	StandbyWarm int                      `json:"standby_warm"`
	Warm        []WarmEntry              `json:"warm"`
	Pending     []string                 `json:"pending"`  // changed channels not probed yet
	Probed      []string                 `json:"probed"`   // changed channels probed for probe_seconds
	Deferred    []string                 `json:"deferred"` // channels left out for want of budget
	Samples     map[string]*ChannelProbe `json:"samples"`
	Expect      verify.Expect            `json:"expect"`
	Now         time.Time                `json:"now"`
}

// stagingRun is the state of a staged change on this host.
type stagingRun struct {
	plan      StagingPlan
	started   time.Time
	expect    verify.Expect
	warm      []WarmEntry
	since     map[uuid.UUID]time.Time // in the warm set since
	probed    map[uuid.UUID]bool      // changed channels done with their rolling probe
	deferred  []string
	budget    int
	capacity  int
	liveOn    int
	stbyWarm  int
	samples   map[uuid.UUID]*ChannelProbe
	firstSeg  map[uuid.UUID]time.Time
	sampling  bool
	previewMu sync.Mutex
	previews  map[uuid.UUID]time.Time
}

func parseIDs(in []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		if id, err := uuid.Parse(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// Expectation is what a release's output settings promise (its tv_output over
// this host's environment, as the slot computes them) and the fixed TS layout.
func Expectation(rawOutput []byte) verify.Expect {
	out, _ := channel.ApplyOutputRules(channel.OutputSettingsFromEnv(), rawOutput)
	return verify.Expect{Width: out.Width, Height: out.Height, FPS: out.FPS, Segment: out.SegmentSeconds,
		Keyint: channel.EffectiveKeyint(out.KeyintSeconds, out.SegmentSeconds),
		PMTPID: channel.TSPMTPID, VideoPID: channel.TSVideoPID, AudioPID: channel.TSAudioPID,
		ServiceID: channel.TSServiceID, AudioCodec: out.AudioCodec}
}

// releaseExpect reads the release's tv_output.
func (s *Slots) releaseExpect(ctx context.Context, releaseID string) verify.Expect {
	var raw []byte
	if s.Pool != nil {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		var settings []byte
		if s.Pool.QueryRow(qctx, `SELECT settings::text FROM tv_releases WHERE id = $1`, releaseID).Scan(&settings) == nil {
			var m map[string]json.RawMessage
			if json.Unmarshal(settings, &m) == nil {
				raw = m["tv_output"]
			}
		}
		cancel()
	}
	return Expectation(raw)
}

// StageStart runs plan's release on the staging instance (idempotent: the
// controller sends the plan on every tick; a new change or release restarts it).
func (s *Slots) StageStart(ctx context.Context, plan StagingPlan) (StagingView, error) {
	if !s.settings(ctx).StagingActive() {
		return StagingView{}, errors.New("frontdoor: staging is off (rules.tv_staging.enabled, or not in this build)")
	}
	if _, err := uuid.Parse(plan.ReleaseID); err != nil {
		return StagingView{}, errors.New("frontdoor: a staging plan needs a release id")
	}
	s.mu.Lock()
	cur := s.stage
	same := cur != nil && cur.plan.ChangeID == plan.ChangeID && cur.plan.ReleaseID == plan.ReleaseID && s.staging != ""
	if same {
		cur.plan = plan // the channel lists may move with demand / edits
		s.mu.Unlock()
		return s.StagingView(), nil
	}
	s.mu.Unlock()
	if cur != nil {
		_ = s.StageStop(ctx, "")
	}
	run := &stagingRun{plan: plan, started: time.Now(), expect: s.releaseExpect(ctx, plan.ReleaseID),
		since: map[uuid.UUID]time.Time{}, probed: map[uuid.UUID]bool{}, samples: map[uuid.UUID]*ChannelProbe{},
		firstSeg: map[uuid.UUID]time.Time{}, previews: map[uuid.UUID]time.Time{}}
	s.mu.Lock()
	if s.staging == "" {
		s.staging = s.otherThan(s.live, s.standby)
	}
	name := s.staging
	if name != "" {
		s.stage = run
	}
	s.mu.Unlock()
	if name == "" {
		return StagingView{}, errors.New("frontdoor: no free slot instance for staging")
	}
	if err := s.persist(ctx); err != nil {
		return StagingView{}, err
	}
	if err := s.bindRelease(ctx, name, plan.ReleaseID); err != nil {
		return StagingView{}, err
	}
	body, _ := json.Marshal(map[string]string{"release_id": plan.ReleaseID, "version": plan.Version, "sha256": plan.SHA256})
	if err := s.superviseWith(ctx, name, true, body); err != nil {
		return StagingView{}, err
	}
	s.log().Info("tv: staging slot started for a change", "slot", name, "change_id", plan.ChangeID,
		"release_id", plan.ReleaseID, "version", plan.Version, "event", "tv_staging_started")
	return s.StagingView(), nil
}

// StageStop ends staging (changeID "" or the staged change's: any other id is a
// stale request and is ignored).
func (s *Slots) StageStop(ctx context.Context, changeID string) error {
	s.mu.Lock()
	run := s.stage
	if run != nil && changeID != "" && run.plan.ChangeID != changeID {
		s.mu.Unlock()
		return nil
	}
	s.stage = nil
	name := s.staging
	s.staging = ""
	s.mu.Unlock()
	if name == "" {
		return nil
	}
	s.supervise(ctx, name, false)
	err := s.persist(ctx)
	if berr := s.bindRelease(ctx, name, ""); err == nil {
		err = berr
	}
	ch := ""
	if run != nil {
		ch = run.plan.ChangeID
	}
	s.log().Info("tv: staging slot stopped", "slot", name, "change_id", ch, "event", "tv_staging_stopped")
	return err
}

// bindRelease writes the release a slot instance is bound to (tv_slots.release_id:
// a slot started for another release runs no station).
func (s *Slots) bindRelease(ctx context.Context, name, releaseID string) error {
	if s.Pool == nil {
		return nil
	}
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var rel any
	if releaseID != "" {
		rel = releaseID
	}
	_, err := s.Pool.Exec(qctx, `UPDATE tv_slots SET release_id = $3 WHERE host_id = $1 AND slot = $2`, s.Host, name, rel)
	return err
}

// superviseWith is supervise with a body (the release binding of a start).
func (s *Slots) superviseWith(ctx context.Context, name string, start bool, body []byte) error {
	if s.Supervisor == "" {
		return nil
	}
	verb := "stop"
	if start {
		verb = "start"
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sctx, http.MethodPost,
		strings.TrimRight(s.Supervisor, "/")+"/slots/"+name+"/"+verb, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("frontdoor: the supervisor did not take the staging start: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("frontdoor: the supervisor refused the staging slot: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// PreviewTouch counts a preview viewer of a staging channel (story 33.6 AC4:
// demand for the staging track only). It reports whether the channel is (or
// will be, at the next warm sync) warm on staging; false with the reason when
// the budget is used up.
func (s *Slots) PreviewTouch(ctx context.Context, id uuid.UUID) (bool, string) {
	s.mu.Lock()
	run := s.stage
	s.mu.Unlock()
	if run == nil {
		return false, "nothing is staged"
	}
	run.previewMu.Lock()
	_, had := run.previews[id]
	run.previews[id] = time.Now()
	run.previewMu.Unlock()
	s.mu.Lock()
	warm := false
	for _, w := range run.warm {
		if w.Channel == id.String() {
			warm = true
		}
	}
	s.mu.Unlock()
	if !warm && !had {
		s.SyncWarm(ctx)
		s.mu.Lock()
		for _, w := range run.warm {
			if w.Channel == id.String() {
				warm = true
			}
		}
		s.mu.Unlock()
	}
	if !warm {
		return false, "every staging encoder is in use (budget " + fmt.Sprint(run.budget) + ")"
	}
	return true, ""
}

// stagingBudget is what live and the standby leave (design §14), capped by
// staging_max_extra_encodes.
func stagingBudget(capacity, nvencMax, vtMax, liveOn, standbyWarm, maxExtra int) int {
	room := capacity - liveOn - standbyWarm
	if nvencMax > 0 {
		if hw := (nvencMax - 2*liveOn - 2*standbyWarm) / 2; hw < room {
			room = hw
		}
	}
	if vtMax > 0 {
		if hw := (vtMax - 2*liveOn - 2*standbyWarm) / 2; hw < room {
			room = hw
		}
	}
	if maxExtra > 0 && maxExtra < room {
		room = maxExtra
	}
	if room < 0 {
		room = 0
	}
	return room
}

// planStagingWarm orders the staging warm set (design §6.1): demand first, then
// preview viewers, the probe channel, then the rolling window over the changed
// channels not probed yet; removed channels never; cut to the budget.
func planStagingWarm(plan StagingPlan, demands []ChannelDemand, previews []uuid.UUID, probed map[uuid.UUID]bool,
	budget int) (warm []WarmEntry, deferred []string) {
	removed := map[uuid.UUID]bool{}
	for _, id := range parseIDs(plan.Removed) {
		removed[id] = true
	}
	seen := map[uuid.UUID]bool{}
	var cands []WarmEntry
	add := func(id uuid.UUID, why string) {
		if id == uuid.Nil || seen[id] || removed[id] {
			return
		}
		seen[id] = true
		cands = append(cands, WarmEntry{Channel: id.String(), Why: why})
	}
	ds := append([]ChannelDemand(nil), demands...)
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].Humans != ds[j].Humans {
			return ds[i].Humans > ds[j].Humans
		}
		return ds[i].Channel.String() < ds[j].Channel.String()
	})
	for _, d := range ds {
		add(d.Channel, WhyDemand)
	}
	for _, id := range previews {
		add(id, WhyPreview)
	}
	if p, err := uuid.Parse(plan.Probe); err == nil {
		add(p, WhyProbe)
	}
	for _, id := range parseIDs(plan.Changed) {
		if !probed[id] {
			add(id, WhyRolling)
		}
	}
	for i, c := range cands {
		if i < budget {
			warm = append(warm, c)
		} else {
			deferred = append(deferred, c.Channel)
		}
	}
	return warm, deferred
}

// syncStaging sends the staging slot its warm set (called by SyncWarm).
func (s *Slots) syncStaging(ctx context.Context, staging string, epoch int64, capacity, nvencMax, vtMax, liveOn, standbyWarm int,
	demands []ChannelDemand) {
	s.mu.Lock()
	run := s.stage
	s.mu.Unlock()
	if run == nil {
		_ = s.sendWarm(ctx, staging, slot.WarmRequest{Role: slot.RoleStaging, Epoch: epoch, Exclusive: true,
			Budget: slot.Budget{MaxActive: 1, NVENCMax: 1, VTMax: 1}})
		return
	}
	now := time.Now()
	budget := stagingBudget(capacity, nvencMax, vtMax, liveOn, standbyWarm, run.plan.MaxExtra)
	var previews []uuid.UUID
	run.previewMu.Lock()
	for id, at := range run.previews {
		if now.Sub(at) > previewTTL {
			delete(run.previews, id)
			continue
		}
		previews = append(previews, id)
	}
	run.previewMu.Unlock()
	sort.Slice(previews, func(i, j int) bool { return previews[i].String() < previews[j].String() })
	s.mu.Lock()
	// a rolling channel is done once it aired for probe_seconds and a sample
	// measured enough of it (min(probe_seconds, 10 s) of segments, less one)
	probeFor := time.Duration(maxInt(run.plan.ProbeSeconds, 5)) * time.Second
	enough := minInt(int(probeFor/time.Second), sampleSeconds) - maxInt(run.expect.Segment, 1)
	for id, at := range run.since {
		cp := run.samples[id]
		if _, ok := run.firstSeg[id]; ok && now.Sub(at) >= probeFor && cp != nil && cp.Staging != nil &&
			cp.Staging.TS.Duration >= float64(enough) {
			run.probed[id] = true
		}
	}
	probed := map[uuid.UUID]bool{}
	for k, v := range run.probed {
		probed[k] = v
	}
	s.mu.Unlock()
	warm, deferred := planStagingWarm(run.plan, demands, previews, probed, budget)
	s.mu.Lock()
	keep := map[uuid.UUID]time.Time{}
	for i := range warm {
		id, _ := uuid.Parse(warm[i].Channel)
		at, ok := run.since[id]
		if !ok {
			at = now
		}
		keep[id] = at
		warm[i].Since = at
	}
	run.since = keep
	for id := range run.firstSeg {
		if _, ok := keep[id]; !ok {
			delete(run.firstSeg, id)
		}
	}
	run.warm, run.deferred, run.budget = warm, deferred, budget
	run.capacity, run.liveOn, run.stbyWarm = capacity, liveOn, standbyWarm
	s.mu.Unlock()
	ch := make([]string, 0, len(warm))
	for _, w := range warm {
		ch = append(ch, w.Channel)
	}
	b := slot.Budget{MaxActive: maxInt(budget, 1)}
	if nvencMax > 0 {
		b.NVENCMax = maxInt(2*budget, 1)
	}
	if vtMax > 0 {
		b.VTMax = maxInt(2*budget, 1)
	}
	if st := s.sendWarm(ctx, staging, slot.WarmRequest{Role: slot.RoleStaging, Epoch: epoch, Channels: ch,
		Budget: b, Exclusive: true}); st != nil {
		s.remember(staging, *st)
	}
}

// StagingView is the staged change's state on this host.
func (s *Slots) StagingView() StagingView {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := StagingView{Slot: s.staging, Now: time.Now().UTC(), Samples: map[string]*ChannelProbe{},
		Warm: []WarmEntry{}, Pending: []string{}, Probed: []string{}, Deferred: []string{}}
	if h := s.health[s.staging]; h != nil && s.staging != "" {
		v.Healthy = h.ok
		if h.ok {
			st := h.status
			v.Status = &st
		}
	}
	run := s.stage
	if run == nil {
		return v
	}
	p := run.plan
	v.Active, v.Plan, v.StartedAt = true, &p, run.started.UTC()
	v.Budget, v.Capacity, v.LiveOnAir, v.StandbyWarm = run.budget, run.capacity, run.liveOn, run.stbyWarm
	v.Warm = append(v.Warm, run.warm...)
	v.Deferred = append(v.Deferred, run.deferred...)
	v.Expect = run.expect
	for _, id := range parseIDs(p.Changed) {
		if run.probed[id] {
			v.Probed = append(v.Probed, id.String())
		} else {
			v.Pending = append(v.Pending, id.String())
		}
	}
	for id, cp := range run.samples {
		v.Samples[id.String()] = cp
	}
	return v
}

// stationAt is a slot's last reported station info for a channel, whether the
// channel is on air there, and the time of the status it comes from.
func (s *Slots) stationAt(name string, id uuid.UUID) (*slot.StationInfo, bool, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.health[name]
	if h == nil || !h.ok {
		return nil, false, time.Time{}
	}
	onAir := false
	for _, c := range h.status.Stations {
		if c == id.String() {
			onAir = true
		}
	}
	for i := range h.status.Channels {
		if h.status.Channels[i].ID == id.String() {
			si := h.status.Channels[i]
			return &si, true, h.at
		}
	}
	return nil, onAir, h.at
}

// sampleStaging measures every warm channel once (on its own goroutine; one
// pass at a time).
func (s *Slots) sampleStaging(ctx context.Context) {
	s.mu.Lock()
	run := s.stage
	staging, live := s.staging, s.live
	if run == nil || run.sampling || staging == "" {
		s.mu.Unlock()
		return
	}
	run.sampling = true
	warm := append([]WarmEntry(nil), run.warm...)
	exp := run.expect
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		run.sampling = false
		s.mu.Unlock()
	}()
	var wg sync.WaitGroup
	for _, w := range warm {
		id, err := uuid.Parse(w.Channel)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func(w WarmEntry, id uuid.UUID) {
			defer wg.Done()
			cp := s.probeChannel(ctx, staging, live, id, exp)
			cp.Why, cp.WarmSince = w.Why, w.Since
			s.mu.Lock()
			if s.stage == run {
				if cp.Staging.Have() {
					if _, ok := run.firstSeg[id]; !ok {
						run.firstSeg[id] = cp.At
					}
				}
				if fs, ok := run.firstSeg[id]; ok {
					cp.FirstSegmentAfter = fs.Sub(w.Since).Seconds()
				}
				run.samples[id] = cp
			}
			s.mu.Unlock()
		}(w, id)
	}
	wg.Wait()
}

// probeChannel samples one channel on staging (and on live when live airs it:
// asking live for a channel it does not air would start an encode there).
func (s *Slots) probeChannel(ctx context.Context, staging, live string, id uuid.UUID, exp verify.Expect) *ChannelProbe {
	now := time.Now()
	cp := &ChannelProbe{Channel: id.String(), At: now.UTC()}
	// what the slots say they air, as of their last status (before the samples:
	// reading live.ts takes a few seconds)
	var at time.Time
	cp.Station, _, at = s.stationAt(staging, id)
	cp.StationAt = at.UTC()
	ls, liveOnAir, _ := s.stationAt(live, id)
	segs := (sampleSeconds + exp.Segment - 1) / maxInt(exp.Segment, 1)
	cp.Staging = s.sample(ctx, staging, id, maxInt(segs, 2), now, true)
	if liveOnAir {
		cp.LiveSt = ls
		cp.Live = s.sample(ctx, live, id, 2, now, false)
	}
	// the playlist's time: the wall-clock checks compare with when it was read
	cp.Results = verify.Evaluate(cp.Staging, cp.Live, exp, cp.Staging.At)
	if cp.Staging.Have() {
		cp.Cloudflare = verify.CloudflareIssues(cp.Staging.TS)
		if cp.Live.Have() {
			cp.Params = verify.ParamChanges(cp.Staging.TS, cp.Live.TS)
		}
	}
	return cp
}

// sample reads a slot's playlist of a channel and its newest n segments.
func (s *Slots) sample(ctx context.Context, name string, id uuid.UUID, n int, now time.Time, stream bool) *verify.Sample {
	smp := &verify.Sample{Slot: name, At: now.UTC()}
	base := s.Base(name)
	if base == "" {
		smp.Error = "no address for slot " + name
		return smp
	}
	get := func(path string) ([]byte, int, error) {
		gctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(gctx, http.MethodGet, base+path, nil)
		if err != nil {
			return nil, 0, err
		}
		resp, err := s.client().Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		return b, resp.StatusCode, err
	}
	body, code, err := get("/stream/channel/" + id.String() + "/index.m3u8")
	smp.Status = code
	smp.At = time.Now().UTC()
	if err != nil {
		smp.Error = err.Error()
		return smp
	}
	if code != http.StatusOK {
		smp.Error = strings.TrimSpace(string(body))
		if len(smp.Error) > 200 {
			smp.Error = smp.Error[:200]
		}
		return smp
	}
	pl, err := verify.ParsePlaylist(body)
	if err != nil {
		smp.Error = err.Error()
		return smp
	}
	from := len(pl.Segs) - n
	if from < 0 {
		from = 0
	}
	var segs [][]byte
	for i := from; i < len(pl.Segs); i++ {
		uri := pl.Segs[i].URI
		if strings.ContainsAny(uri, "/\\?") || !strings.HasPrefix(uri, "seg-") {
			continue
		}
		b, code, err := get("/stream/channel/" + id.String() + "/" + uri)
		if err != nil || code != http.StatusOK {
			continue // slid out meanwhile
		}
		one := verify.AnalyzeTS(b, verify.Options{Now: now})
		pl.Segs[i].Sampled, pl.Segs[i].FirstPTS, pl.Segs[i].FirstIDR = true, one.FirstPTS, one.FirstIDR
		pl.Segs[i].PSI = one.PATs > 0 && one.PMTs > 0
		segs = append(segs, b)
	}
	smp.Playlist = pl
	if len(segs) > 0 {
		smp.TS = verify.AnalyzeSegments(segs, verify.Options{Grid: s.gridOf(), Now: now})
	}
	if stream {
		smp.Stream = s.sampleStream(ctx, base, id, streamSampleFor, now)
	}
	return smp
}

// streamSampleFor is how much of a staging channel's live.ts a sample reads.
const streamSampleFor = 3 * time.Second

// sampleStream reads d of a slot's live.ts of a channel (the HDHomeRun path).
func (s *Slots) sampleStream(ctx context.Context, base string, id uuid.UUID, d time.Duration, now time.Time) verify.TSReport {
	sctx, cancel := context.WithTimeout(ctx, d+3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sctx, http.MethodGet, base+"/stream/channel/"+id.String()+"/live.ts", nil)
	if err != nil {
		return verify.TSReport{}
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return verify.TSReport{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return verify.TSReport{}
	}
	var buf bytes.Buffer
	deadline := time.Now().Add(d)
	chunk := make([]byte, 64<<10)
	started := false
	for time.Now().Before(deadline) || !started {
		n, err := resp.Body.Read(chunk)
		if n > 0 {
			if !started {
				started = true
				deadline = time.Now().Add(d)
			}
			buf.Write(chunk[:n])
		}
		if err != nil || buf.Len() > 16<<20 {
			break
		}
	}
	b := buf.Bytes()
	if i := bytes.IndexByte(b, 0x47); i > 0 {
		b = b[i:]
	}
	b = b[:len(b)-len(b)%188]
	return verify.AnalyzeTS(b, verify.Options{Grid: s.gridOf(), Now: now})
}

// gridOf is the staged release's keyframe grid.
func (s *Slots) gridOf() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stage != nil {
		return s.stage.expect.Keyint
	}
	return channel.DefaultKeyintSeconds
}

// StagingRelease is the staged change and release (preview), "" when none.
func (s *Slots) StagingRelease() (changeID, releaseID, slotName string, removed []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stage == nil {
		return "", "", "", nil
	}
	return s.stage.plan.ChangeID, s.stage.plan.ReleaseID, s.staging, append([]string(nil), s.stage.plan.Removed...)
}
