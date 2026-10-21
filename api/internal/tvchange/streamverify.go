package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
)

// Stream verification of a staged change (story 33.5, design §6): the staging slot
// runs the change's release, the front door warms and samples it, and the
// controller turns the samples into verify_report.checks next to the database
// checks (lineup, guide coverage, the cut-free block invariant):
//
//	slot.health · channel.first_segment · channel.cadence · channel.wallclock ·
//	channel.block · ts.conformity · hls.conformity · splice.align · splice.compat ·
//	cloudflare.ingest · push.continuity · guide.vs_stream · encoders · encoders.budget
//
// A check that fails on two samples in a row fails the change at once: it goes back
// to draft with the reasons (failToDraft); otherwise the change is ready after
// verify_soak_seconds of samples with every warm channel measured and every changed
// channel probed. push.continuity is
// the one fail that does not end the change: it asks for the acknowledgement at the
// approval (param_change_push = approve) instead.

// Timeouts of a verification run.
const (
	slotStartTimeout    = 90 * time.Second
	firstSegmentTimeout = 75 * time.Second
	recheckEvery        = 60 * time.Second
	slotCrashLoop       = 3
)

// planFor builds the staging plan of a change from its release and diff.
func (c *Controller) planFor(ctx context.Context, cfg Config, ch *Change) (StagingPlan, []releaseChannel, error) {
	p := StagingPlan{ChangeID: ch.ID, ProbeSeconds: cfg.VerifyProbeSeconds, MaxExtra: cfg.StagingMaxExtra,
		Channels: []string{}, Changed: []string{}, Removed: []string{}}
	if ch.ReleaseID == nil {
		return p, nil, errors.New("change has no release")
	}
	p.ReleaseID = *ch.ReleaseID
	var ver, sum sql.NullString
	if err := c.DB.QueryRowContext(ctx, `SELECT streaming_version, streaming_sha256 FROM tv_releases WHERE id = $1`,
		p.ReleaseID).Scan(&ver, &sum); err != nil {
		return p, nil, err
	}
	p.Version, p.SHA256 = ver.String, sum.String
	chans, _, err := c.releaseChannels(ctx, p.ReleaseID)
	if err != nil {
		return p, nil, err
	}
	all := contains(ch.Kinds, "settings") || contains(ch.Kinds, "binary")
	changed := map[string]bool{}
	for _, id := range diffIDs(ch.Diff) {
		changed[id] = true
	}
	for _, rc := range chans {
		if !rc.Enabled {
			continue
		}
		p.Channels = append(p.Channels, rc.ID)
		if all || rc.SchedChanged {
			changed[rc.ID] = true
		}
	}
	inRelease := map[string]bool{}
	for _, id := range p.Channels {
		inRelease[id] = true
	}
	for _, rc := range chans { // in number order
		if changed[rc.ID] && inRelease[rc.ID] {
			p.Changed = append(p.Changed, rc.ID)
		}
	}
	if lu, ok := ch.Diff["lineup"].(map[string]any); ok {
		for _, x := range asList(lu["removed"]) {
			if id, _ := x["id"].(string); id != "" {
				p.Removed = append(p.Removed, id)
			}
		}
	}
	for _, rc := range chans {
		if !rc.Enabled {
			continue
		}
		if cfg.ProbeChannel != "" && rc.Slug == cfg.ProbeChannel {
			p.Probe = rc.ID
			break
		}
		if p.Probe == "" && cfg.ProbeChannel == "" {
			p.Probe = rc.ID // the lowest number (chans are in number order)
		}
	}
	if p.Probe == "" && len(p.Channels) > 0 {
		p.Probe = p.Channels[0]
	}
	return p, chans, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func asList(v any) []map[string]any {
	xs, _ := v.([]any)
	out := make([]map[string]any, 0, len(xs))
	for _, x := range xs {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// diffIDs are the channels a change's diff touches (added, renumbered, renamed,
// changed, art, schedules).
func diffIDs(d map[string]any) []string {
	var out []string
	if lu, ok := d["lineup"].(map[string]any); ok {
		for _, k := range []string{"added", "renumbered", "renamed", "changed"} {
			for _, x := range asList(lu[k]) {
				if id, _ := x["id"].(string); id != "" {
					out = append(out, id)
				}
			}
		}
	}
	for _, k := range []string{"art", "schedules"} {
		for _, x := range asList(d[k]) {
			if id, _ := x["id"].(string); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// stageSlot starts (or keeps) the staging slot of a staged change and moves it to
// verifying once the slot is healthy on the release. ErrNoStagingRuntime: the
// interim (database-only) verification applies.
func (c *Controller) stageSlot(ctx context.Context, cfg Config, ch *Change) error {
	plan, _, err := c.planFor(ctx, cfg, ch)
	if err != nil {
		return err
	}
	v, err := c.Staging.Start(ctx, plan)
	if errors.Is(err, ErrNoStagingRuntime) {
		return err
	}
	since := ch.UpdatedAt
	if ch.StagedAt != nil {
		since = *ch.StagedAt
	}
	if err == nil && slotHealthy(v, plan) {
		return c.moveQuiet(ctx, ch, StateVerifying, ActionStartVerify,
			map[string]any{"slot": v.Slot, "release_id": plan.ReleaseID, "version": v.Status.Version}, nil)
	}
	if c.now().Sub(since) < slotStartTimeout {
		return nil // starting
	}
	why := "the staging slot did not become healthy on the release"
	if err != nil {
		why = "the staging slot did not start: " + err.Error()
	} else if v != nil && v.Status != nil && v.Status.Mismatch {
		why = "the staging slot runs another release (release_mismatch)"
	}
	report := map[string]any{"checks": []Check{{Check: "slot.health", Result: ResultFail, Value: why}},
		"passed": false, "warnings": 0, "failures": 1, "at": c.now().Format(time.RFC3339), "runtime": "slots"}
	if err := c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"verify_report": report}); err != nil {
		return err
	}
	return c.failToDraft(ctx, ch, map[string]any{"report": why}, "slot_not_healthy")
}

// moveQuiet is Move where a concurrent admin action (cancel) wins silently.
func (c *Controller) moveQuiet(ctx context.Context, ch *Change, to, action string, detail, fields map[string]any) error {
	err := c.Store.Move(ctx, c.DB, ch, to, action, nil, detail, fields)
	var ce *ConflictError
	if errors.As(err, &ce) {
		return nil
	}
	return err
}

// slotHealthy: the staging slot answers, is bound to the change's release and runs
// its binary version.
func slotHealthy(v *StagingView, plan StagingPlan) bool {
	if v == nil || !v.Active || !v.Healthy || v.Status == nil || v.Plan == nil || v.Plan.ChangeID != plan.ChangeID {
		return false
	}
	st := v.Status
	if st.Role != "staging" || st.Mismatch || st.Release != plan.ReleaseID {
		return false
	}
	return plan.Version == "" || st.Version == plan.Version
}

// runState is what a verification run keeps between ticks (verify_report.run).
type runState struct {
	Started    time.Time          `json:"started"`
	SoakFrom   *time.Time         `json:"soak_from,omitempty"`
	Streaks    map[string]int     `json:"streaks"`
	LastAt     map[string]string  `json:"last_at"`
	PIDs       []int              `json:"pids"`
	Restarts0  map[string]int     `json:"restarts0"`
	RecheckAt  *time.Time         `json:"recheck_at,omitempty"`
	Samples    int                `json:"samples"`
	FirstAfter map[string]float64 `json:"first_after,omitempty"`
}

func loadRun(report map[string]any) *runState {
	raw, ok := report["run"]
	if !ok {
		return nil
	}
	b, _ := json.Marshal(raw)
	var r runState
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	if r.Streaks == nil {
		r.Streaks = map[string]int{}
	}
	if r.LastAt == nil {
		r.LastAt = map[string]string{}
	}
	if r.Restarts0 == nil {
		r.Restarts0 = map[string]int{}
	}
	return &r
}

// verifyingSince is when the change last entered verifying.
func (c *Controller) verifyingSince(ctx context.Context, ch *Change) time.Time {
	var at nullTime
	if err := c.DB.QueryRowContext(ctx, `SELECT MAX(at) FROM tv_change_events WHERE change_id = $1 AND to_state = $2`,
		ch.ID, StateVerifying).Scan(&at); err == nil && !at.T.IsZero() {
		return at.T
	}
	return ch.UpdatedAt
}

// streamChecks turns the front door's view into checks (one evaluation).
type streamEval struct {
	checks  []Check
	pending []string // what is still being measured
	warm    int
	budget  string
}

func remoteFlags(ctx context.Context, db *sql.DB) (lowLatency bool) {
	m, err := rules.Effective(ctx, db, "tv_remote")
	if err == nil {
		lowLatency, _ = m["low_latency"].(bool)
	}
	return lowLatency
}

// pushedChannels are the channels with a running remote push.
func (c *Controller) pushedChannels(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	rows, err := c.DB.QueryContext(ctx, `SELECT DISTINCT channel_id FROM tv_remote_pushes
		WHERE state IN ('requested', 'connecting', 'live') AND ended_at IS NULL`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// fullBlock is a block of a generation at an instant with its title.
type fullBlock struct {
	video sql.NullString
	start nullTime
	end   nullTime
	kind  string
	title string
}

func (c *Controller) blockWithTitle(ctx context.Context, channelID string, gen int64, at time.Time) (*fullBlock, error) {
	var b fullBlock
	var snap sql.NullString
	err := c.DB.QueryRowContext(ctx, `SELECT video_id, start_at, end_at, kind, CAST(title_snapshot AS TEXT) FROM channel_programs
		WHERE channel_id = $1 AND sched_gen = $2 AND start_at <= $3 AND end_at > $3
		ORDER BY start_at DESC LIMIT 1`, channelID, gen, tsParam(c.DB, at)).Scan(&b.video, &b.start, &b.end, &b.kind, &snap)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if snap.Valid {
		var m map[string]any
		if json.Unmarshal([]byte(snap.String), &m) == nil {
			b.title, _ = m["title"].(string)
		}
	}
	return &b, nil
}

func sameBlock(a *OnAir, video string, start, end time.Time) bool {
	if a == nil || a.Synthetic {
		return false
	}
	return a.VideoID == video && a.StartAt.Equal(start) && a.EndAt.Equal(end)
}

// evaluate reads the view into checks for this tick.
func (c *Controller) evaluate(ctx context.Context, cfg Config, plan StagingPlan, v *StagingView,
	gen int64, run *runState) (streamEval, error) {
	now := c.now()
	var ev streamEval
	add := func(k Check) { ev.checks = append(ev.checks, k) }
	st := v.Status
	if st == nil {
		st = &SlotStatus{}
	}
	add(Check{Check: "slot.health", Result: ResultPass, Value: fmt.Sprintf("%s healthy on release %s, version %s, pid %d",
		v.Slot, short(plan.ReleaseID), st.Version, st.PID)})
	pushed := c.pushedChannels(ctx)
	lowLatency := remoteFlags(ctx, c.DB)
	inRelease, removed, changed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range plan.Channels {
		inRelease[id] = true
	}
	for _, id := range plan.Removed {
		removed[id] = true
	}
	for _, id := range plan.Changed {
		changed[id] = true
	}
	var live int64
	_ = c.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT live_gen FROM tv_routing WHERE scope = 'cluster'), 0)`).Scan(&live)
	ids := make([]string, 0, len(v.Warm))
	for _, w := range v.Warm {
		ids = append(ids, w.Channel)
	}
	sort.Strings(ids)
	ev.warm = len(ids)
	stagingStations := len(st.Stations)
	// the warm channels, and the changed ones already probed (their last sample)
	type subject struct {
		w      WarmEntry
		probed bool
	}
	var subjects []subject
	for _, w := range v.Warm {
		subjects = append(subjects, subject{w: w})
	}
	for _, id := range v.Probed {
		warmNow := false
		for _, w := range v.Warm {
			warmNow = warmNow || w.Channel == id
		}
		if smp := v.Samples[id]; smp != nil && !warmNow {
			subjects = append(subjects, subject{w: WarmEntry{Channel: id, Why: "rolling", Since: smp.WarmSince}, probed: true})
		}
	}
	for _, sj := range subjects {
		w := sj.w
		id := w.Channel
		if !inRelease[id] && !removed[id] {
			add(Check{Check: "lineup.sanity", ChannelID: id, Result: ResultFail,
				Value: "on the air for viewers but missing from the release (the change does not remove it)"})
			continue
		}
		smp := v.Samples[id]
		if smp == nil || (!sj.probed && smp.At.Before(w.Since)) {
			ev.pending = append(ev.pending, "sample of "+short(id))
			if now.Sub(w.Since) > firstSegmentTimeout {
				add(Check{Check: "channel.first_segment", ChannelID: id, Result: ResultFail,
					Value: fmt.Sprintf("not measured %.0f s after the warm-up began", now.Sub(w.Since).Seconds())})
			}
			continue
		}
		sampled := smp.At
		for _, r := range smp.Results {
			k := Check{Check: r.Check, ChannelID: id, Result: r.Result, Value: r.Value}
			if r.Result == "pending" {
				if sj.probed {
					continue
				}
				if r.Check == "channel.first_segment" && now.Sub(w.Since) > firstSegmentTimeout {
					k.Result = ResultFail
					k.Value = fmt.Sprintf("no segment %.0f s after the warm-up began (%s)", now.Sub(w.Since).Seconds(), r.Value)
					add(k)
					continue
				}
				ev.pending = append(ev.pending, r.Check+" of "+short(id))
				continue
			}
			if r.Check == "channel.first_segment" && r.Result == ResultPass && smp.FirstSegmentAfter > 0 {
				k.Value += fmt.Sprintf(", first after %.1f s", smp.FirstSegmentAfter)
				if run.FirstAfter == nil {
					run.FirstAfter = map[string]float64{}
				}
				run.FirstAfter[id] = smp.FirstSegmentAfter
			}
			add(k)
		}
		haveSeg := false
		for _, r := range smp.Results {
			if r.Check == "channel.first_segment" && r.Result == ResultPass {
				haveSeg = true
			}
		}
		if !haveSeg {
			continue
		}
		// the block on the air: N+1's at now; for unchanged channels live's too
		stn := smp.Station
		if stn == nil || stn.Block == nil {
			ev.pending = append(ev.pending, "block of "+short(id))
		} else {
			at := sampled
			if stn.Reached != nil {
				at = *stn.Reached
				ref := smp.StationAt
				if ref.IsZero() {
					ref = sampled
				}
				d := ref.Sub(at).Seconds()
				pos := Check{Check: "channel.position", ChannelID: id, Result: ResultPass,
					Value: fmt.Sprintf("output at %+.3f s from the wall clock", -d)}
				if math.Abs(d) > 2 {
					pos.Result = ResultFail
				}
				add(pos)
			}
			nb, err := c.blockWithTitle(ctx, id, gen, at)
			if err != nil {
				return ev, err
			}
			blk := stn.Block
			switch {
			case nb == nil && blk.Synthetic:
				add(Check{Check: "channel.block", ChannelID: id, Result: ResultPass, Value: "a slate where generation " +
					fmt.Sprint(gen) + " has nothing scheduled"})
			case nb == nil:
				add(Check{Check: "channel.block", ChannelID: id, Result: ResultFail,
					Value: fmt.Sprintf("airs %q but generation %d has nothing at %s", blk.Title, gen, at.Format(time.RFC3339))})
			case !sameBlock(blk, nb.video.String, nb.start.T, nb.end.T):
				if blk.Synthetic && nb.video.String == "" {
					add(Check{Check: "channel.block", ChannelID: id, Result: ResultPass, Value: "a slate, as scheduled"})
					break
				}
				add(Check{Check: "channel.block", ChannelID: id, Result: ResultFail,
					Value: fmt.Sprintf("airs %q (%s–%s), generation %d schedules %q (%s–%s)", blk.Title,
						blk.StartAt.Format("15:04:05"), blk.EndAt.Format("15:04:05"), gen, nb.title,
						nb.start.T.Format("15:04:05"), nb.end.T.Format("15:04:05"))})
			default:
				val := fmt.Sprintf("the block of generation %d at now (seq %d)", gen, blk.Seq)
				res := ResultPass
				if !changed[id] && smp.LiveStation != nil && smp.LiveStation.Block != nil {
					lb := smp.LiveStation.Block
					if lb.Synthetic != blk.Synthetic || lb.VideoID != blk.VideoID || !lb.StartAt.Equal(blk.StartAt) {
						res, val = ResultFail, fmt.Sprintf("unchanged channel airs %q on staging, %q on live", blk.Title, lb.Title)
					} else {
						val += ", the same block as live"
					}
				}
				add(Check{Check: "channel.block", ChannelID: id, Result: res, Value: val})
			}
			if nb != nil && !blk.Synthetic {
				gv := Check{Check: "guide.vs_stream", ChannelID: id, Result: ResultPass,
					Value: fmt.Sprintf("guide %q = stream %q", nb.title, blk.Title)}
				if nb.title != blk.Title {
					gv.Result = ResultFail
				}
				add(gv)
			}
		}
		// Cloudflare ingest (fail for a pushed channel, warn otherwise)
		var issues []string
		for _, s := range smp.Cloudflare {
			if strings.HasPrefix(s, "b_frames") && !lowLatency {
				continue
			}
			issues = append(issues, s)
		}
		cf := Check{Check: "cloudflare.ingest", ChannelID: id, Result: ResultPass,
			Value: "H.264, AAC, closed GOP 2–10 s, 2–14 Mb/s"}
		if len(issues) > 0 {
			cf.Result, cf.Value = ResultWarn, strings.Join(issues, "; ")
			if pushed[id] {
				cf.Result = ResultFail
			}
		}
		add(cf)
		if pushed[id] {
			pc := Check{Check: "push.continuity", ChannelID: id, Result: ResultPass, Value: "seamless: the push keeps its session"}
			if len(smp.Params) > 0 {
				pc.Value = "param_change: " + strings.Join(smp.Params, ", ")
				if cfg.ParamChangePush == "defer" {
					pc.Result, pc.Value = ResultWarn, pc.Value+" (deferred until the push ends)"
				} else {
					pc.Result, pc.Value = ResultFail, pc.Value+" (simulcast outputs may reconnect: acknowledge it in the approval)"
				}
			}
			add(pc)
		}
		// encoders: no muxer restart, no hardware fallback during the run
		if stn != nil {
			if _, ok := run.Restarts0[id]; !ok {
				run.Restarts0[id] = stn.Restarts
			}
			ec := Check{Check: "encoders", ChannelID: id, Result: ResultPass,
				Value: fmt.Sprintf("%s, no muxer restart, no fallback", orDash(stn.Encoder))}
			if d := stn.Restarts - run.Restarts0[id]; d > 0 || stn.NVENCFallbacks > 0 {
				ec.Result = ResultWarn
				ec.Value = fmt.Sprintf("%s, %d muxer restarts, %d hardware fallbacks", orDash(stn.Encoder), d, stn.NVENCFallbacks)
			}
			add(ec)
		}
	}
	// the encoder budget (design §14): staging gets what live and the standby leave
	bud := Check{Check: "encoders.budget", Result: ResultPass,
		Value: fmt.Sprintf("staging %d of %d (capacity %d: live %d, standby %d)", stagingStations, v.Budget, v.Capacity,
			v.LiveOnAir, v.StandbyWarm)}
	if stagingStations > v.Budget && v.Budget >= 0 {
		bud.Result = ResultFail
		bud.Value = "over budget: " + bud.Value
	}
	if len(v.Deferred) > 0 {
		bud.Value += fmt.Sprintf("; %d channels wait for an encoder (rolling)", len(v.Deferred))
	}
	if v.Budget == 0 {
		bud.Result = ResultWarn
		bud.Value = "no encoder left for staging: " + bud.Value
	}
	add(bud)
	ev.budget = bud.Value
	for _, id := range v.Pending {
		ev.pending = append(ev.pending, "rolling probe of "+short(id))
	}
	return ev, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// verifyStream is one tick of a verifying change on the staging runtime.
func (c *Controller) verifyStream(ctx context.Context, cfg Config, ch *Change) error {
	plan, _, err := c.planFor(ctx, cfg, ch)
	if err != nil {
		return err
	}
	v, err := c.Staging.Start(ctx, plan) // idempotent: keeps the plan current after a front-door restart
	if err != nil {
		return err
	}
	now := c.now()
	since := c.verifyingSince(ctx, ch)
	run := loadRun(ch.VerifyReport)
	if run == nil || run.Started.Before(since.Add(-time.Second)) {
		run = &runState{Started: now, Streaks: map[string]int{}, LastAt: map[string]string{}, Restarts0: map[string]int{}}
	}
	if !slotHealthy(v, plan) {
		// a crash: the watchdog restarts it; three during a run fail the change
		if now.Sub(run.Started) > slotStartTimeout && (v == nil || !v.Healthy) {
			return c.failRun(ctx, ch, run, []Check{{Check: "slot.health", Result: ResultFail,
				Value: "the staging slot is down"}}, "slot_not_healthy")
		}
		return c.saveRun(ctx, ch, run, nil, "the staging slot is not answering")
	}
	if pid := v.Status.PID; len(run.PIDs) == 0 || run.PIDs[len(run.PIDs)-1] != pid {
		run.PIDs = append(run.PIDs, pid)
		if len(run.PIDs) > slotCrashLoop {
			return c.failRun(ctx, ch, run, []Check{{Check: "slot.health", Result: ResultFail,
				Value: fmt.Sprintf("the staging slot restarted %d times during verification", len(run.PIDs)-1)}}, "slot_crash_loop")
		}
	}
	db, _, _, err := c.Verify(ctx, ch)
	if err != nil {
		return err
	}
	var gen int64
	_ = c.DB.QueryRowContext(ctx, `SELECT sched_gen FROM tv_releases WHERE id = $1`, plan.ReleaseID).Scan(&gen)
	ev, err := c.evaluate(ctx, cfg, plan, v, gen, run)
	if err != nil {
		return err
	}
	checks := ev.checks
	if dbc, ok := db["checks"].([]Check); ok {
		for _, k := range dbc {
			switch k.Check {
			case "slot.health":
			case "channel.block":
				// the database's cut-free invariant (N+1 = N for the block on air)
				k.Check = "schedule.boundary"
				checks = append(checks, k)
			default:
				checks = append(checks, k)
			}
		}
	}
	// streaks: a fail on two samples in a row ends the run
	sampleAt := map[string]string{}
	for id, s := range v.Samples {
		sampleAt[id] = s.At.Format(time.RFC3339Nano)
	}
	var hard []Check
	for _, k := range checks {
		key := k.Check + "|" + k.ChannelID
		at := sampleAt[k.ChannelID]
		if k.ChannelID == "" {
			at = now.Format(time.RFC3339Nano)
		}
		if k.Result != ResultFail || k.Check == "push.continuity" {
			delete(run.Streaks, key)
			continue
		}
		if run.LastAt[key] != at {
			run.Streaks[key]++
			run.LastAt[key] = at
		}
		if run.Streaks[key] >= 2 || dbOnly(k.Check) {
			hard = append(hard, k)
		}
	}
	if len(hard) > 0 {
		return c.failRun(ctx, ch, run, checks, "verification_failed")
	}
	run.Samples++
	if len(ev.pending) == 0 && ev.warm > 0 && run.SoakFrom == nil {
		t := now
		run.SoakFrom = &t
	}
	soak := time.Duration(cfg.VerifySoakSeconds) * time.Second
	if run.SoakFrom != nil && now.Sub(*run.SoakFrom) >= soak && len(ev.pending) == 0 {
		failed := false
		for _, k := range checks {
			if k.Result == ResultFail && k.Check != "push.continuity" {
				failed = true
			}
		}
		if failed {
			return c.failRun(ctx, ch, run, checks, "verification_failed")
		}
		report := c.report(checks, run, v, "")
		report["passed"] = true
		if err := c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"verify_report": report}); err != nil {
			return err
		}
		ch.VerifyReport = report
		return c.moveQuiet(ctx, ch, StateReady, ActionPass, map[string]any{"soak_seconds": cfg.VerifySoakSeconds,
			"slot": v.Slot}, map[string]any{"ready_at": c.now()})
	}
	limit := soak + time.Duration(len(plan.Changed)+3)*time.Duration(maxInt(cfg.VerifyProbeSeconds, 5))*time.Second +
		3*firstSegmentTimeout
	if now.Sub(run.Started) > limit {
		checks = append(checks, Check{Check: "soak", Result: ResultFail,
			Value: "verification did not finish: still measuring " + strings.Join(ev.pending, ", ")})
		return c.failRun(ctx, ch, run, checks, "verification_timeout")
	}
	status := "soaking"
	if run.SoakFrom == nil {
		status = "warming: " + strings.Join(firstN(ev.pending, 4), ", ")
	}
	return c.saveRun(ctx, ch, run, checks, status)
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return append(append([]string{}, xs[:n]...), fmt.Sprintf("+%d", len(xs)-n))
	}
	return xs
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// dbOnly checks do not change between samples: one fail is enough.
func dbOnly(check string) bool {
	switch check {
	case "lineup.sanity", "guide.coverage", "schedule.boundary":
		return true
	}
	return false
}

// report is a verify_report of a slot run.
func (c *Controller) report(checks []Check, run *runState, v *StagingView, status string) map[string]any {
	sortChecks(checks)
	pass, warn, fail := 0, 0, 0
	for _, k := range checks {
		switch k.Result {
		case ResultPass:
			pass++
		case ResultWarn:
			warn++
		case ResultFail:
			fail++
		}
	}
	out := map[string]any{
		"checks":   checks,
		"passed":   false,
		"warnings": warn,
		"failures": fail,
		"summary":  map[string]int{"pass": pass, "warn": warn, "fail": fail},
		"at":       c.now().Format(time.RFC3339),
		"runtime":  "slots",
		"run":      run,
	}
	if v != nil {
		out["slot"] = v.Slot
		out["warm"] = v.Warm
		out["budget"] = map[string]int{"staging": v.Budget, "capacity": v.Capacity, "live": v.LiveOnAir,
			"standby": v.StandbyWarm}
	}
	if status != "" {
		out["status"] = status
	}
	if run.SoakFrom != nil {
		out["soak_started_at"] = run.SoakFrom.Format(time.RFC3339)
	}
	return out
}

func sortChecks(checks []Check) {
	rank := map[string]int{ResultFail: 0, ResultWarn: 1, ResultPass: 2, ResultSkip: 3}
	sort.SliceStable(checks, func(i, j int) bool {
		if rank[checks[i].Result] != rank[checks[j].Result] {
			return rank[checks[i].Result] < rank[checks[j].Result]
		}
		if checks[i].Check != checks[j].Check {
			return checks[i].Check < checks[j].Check
		}
		return checks[i].ChannelID < checks[j].ChannelID
	})
}

func (c *Controller) saveRun(ctx context.Context, ch *Change, run *runState, checks []Check, status string) error {
	report := c.report(checks, run, nil, status)
	if checks == nil {
		if old, ok := ch.VerifyReport["checks"]; ok {
			report["checks"] = old
		}
	}
	ch.VerifyReport = report
	return c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"verify_report": report})
}

func (c *Controller) failRun(ctx context.Context, ch *Change, run *runState, checks []Check, reason string) error {
	report := c.report(checks, run, nil, "")
	var why []string
	for _, k := range checks {
		if k.Result == ResultFail && k.Check != "push.continuity" {
			s := k.Check
			if k.ChannelID != "" {
				s += " (" + short(k.ChannelID) + ")"
			}
			why = append(why, s+": "+k.Value)
		}
	}
	report["reasons"] = why
	if err := c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"verify_report": report}); err != nil {
		return err
	}
	ch.VerifyReport = report
	return c.failToDraft(ctx, ch, map[string]any{"report": "failed checks", "reasons": why}, reason)
}

// recheck is the ready subset every minute (AC9): slot health, wallclock, block. A
// fail moves the change back to verifying (a new run).
func (c *Controller) recheck(ctx context.Context, cfg Config, ch *Change) error {
	run := loadRun(ch.VerifyReport)
	if run == nil {
		return nil // verified on the database (interim)
	}
	now := c.now()
	if run.RecheckAt != nil && now.Sub(*run.RecheckAt) < recheckEvery {
		return nil
	}
	plan, _, err := c.planFor(ctx, cfg, ch)
	if err != nil {
		return err
	}
	v, err := c.Staging.Start(ctx, plan)
	if err != nil {
		return err
	}
	t := now
	run.RecheckAt = &t
	var bad []string
	if !slotHealthy(v, plan) {
		bad = append(bad, "slot.health: the staging slot is down")
	} else {
		var gen int64
		_ = c.DB.QueryRowContext(ctx, `SELECT sched_gen FROM tv_releases WHERE id = $1`, plan.ReleaseID).Scan(&gen)
		ev, err := c.evaluate(ctx, cfg, plan, v, gen, run)
		if err != nil {
			return err
		}
		for _, k := range ev.checks {
			switch k.Check {
			case "slot.health", "channel.wallclock", "channel.block", "channel.position":
				if k.Result == ResultFail {
					bad = append(bad, k.Check+" ("+short(k.ChannelID)+"): "+k.Value)
				}
			}
		}
	}
	ch.VerifyReport["run"] = run
	if err := c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"verify_report": ch.VerifyReport}); err != nil {
		return err
	}
	if len(bad) > 0 {
		return c.moveQuiet(ctx, ch, StateVerifying, ActionRecheckFail, map[string]any{"reasons": bad}, nil)
	}
	return nil
}

// reconcileStaging stops the staging slot when no change it serves is in flight
// (cancelled, failed, live, rolled back) — whatever moved it there.
func (c *Controller) reconcileStaging(ctx context.Context) {
	if c.Staging == nil {
		return
	}
	v, err := c.Staging.View(ctx)
	if err != nil || v == nil || !v.Active || v.Plan == nil {
		return
	}
	cur, err := c.Store.InFlightChange(ctx, c.DB)
	if err != nil {
		return
	}
	keep := cur != nil && cur.ID == v.Plan.ChangeID && cur.State != StateSwapping
	if cur != nil && cur.ID == v.Plan.ChangeID && cur.State == StateSwapping {
		keep = true // the swap (story 33.7) switches to it
	}
	if keep {
		return
	}
	if err := c.Staging.Stop(ctx, v.Plan.ChangeID); err != nil {
		c.Logger.Warn("tv staging slot stop failed", "change_id", v.Plan.ChangeID, "error", err.Error())
		return
	}
	c.Logger.Info("tv staging slot stopped: its change left staging", "change_id", v.Plan.ChangeID,
		"event", "tv_staging_stopped")
}
