//go:build broadcastrehearsal

package rehearsal

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	viewers "github.com/Hamza-Labs-Core/Maktaba/tools/broadcast-viewers"
)

// The scenarios with a body (scenarios_cloud_test.go has the ones driven by the cloud's posts). A
// scenario listed in expected-failures.txt runs and is reported XFAIL, never as a pass.
var bodies = map[string]func(*stack){
	"broadcast-fanout":           fanout,
	"broadcast-home":             homeOffline,
	"broadcast-home-offline-24h": offline24h,
	"broadcast-plans":            plans,
	"broadcast-publishable":      publishable,
	"broadcast-access":           access,
	"broadcast-kill":             kill,
	"broadcast-takedown":         takedown,
	"broadcast-hetzner-down":     hetznerDown,
	"broadcast-edge-deploy":      edgeDeploy,
	"broadcast-observe":          observe,
}

// TestBroadcast runs the scenarios (one with -run 'TestBroadcast/<name>$'):
//
//	docker build -f tools/rehearsal-cloud/broadcast/Dockerfile.edge -t maktaba-broadcast-edge:rehearsal .
//	docker build -f tools/rehearsal-cloud/broadcast/Dockerfile.home -t maktaba-broadcast-home:rehearsal .
//	cd tools/broadcast-viewers && go test -tags broadcastrehearsal -run 'TestBroadcast/broadcast-fanout$' -timeout 40m ./rehearsal/
//
// BROADCAST_VIEWERS (default 100) is the fan-out's top step; BROADCAST_STEP_SECONDS (default 60)
// each step's length. The nightly set runs 1,000 viewers for 300 s a step.
func TestBroadcast(t *testing.T) {
	f, err := os.Open(filepath.Join(repoRoot(t), "tools/rehearsal-cloud/broadcast/expected-failures.txt"))
	if err != nil {
		t.Fatal(err)
	}
	exp, err := ParseExpected(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range Scenarios {
		_, has := bodies[sc.Name]
		if has = has || sc.Name == "broadcast-fanout-planted"; has != sc.Built {
			t.Fatalf("catalog.go says %s built=%v, but its body is %v: keep them together", sc.Name, sc.Built, has)
		}
	}
	for _, sc := range Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			r := Result{Scenario: sc.Name, Story: sc.Story}
			start := time.Now()
			body, built := bodies[sc.Name]
			if sc.Name == "broadcast-fanout-planted" {
				body, built = planted, true
			}
			if built {
				run(t, &r, body)
			} else {
				r.Notes = append(r.Notes, "not built yet: "+exp[sc.Name])
			}
			r.Seconds = time.Since(start).Seconds()
			Finish(&r, built, exp)
			if err := Write(outDir(t), r); err != nil {
				t.Error(err)
			}
			switch r.Status {
			case Fail:
				t.Errorf("%s failed: %+v", sc.Name, r.Failed())
			case XPass:
				t.Errorf("%s passed but is listed in expected-failures.txt: remove its line", sc.Name)
			case XFail:
				t.Skipf("XFAIL (expected failure: %s)", exp[sc.Name])
			}
		})
	}
	var all []Result
	entries, _ := os.ReadDir(outDir(t))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			if r, err := ReadResult(filepath.Join(outDir(t), e.Name())); err == nil {
				all = append(all, r)
			}
		}
	}
	_ = os.WriteFile(filepath.Join(outDir(t), "report.md"), []byte(Report(all)), 0o644)
}

func run(t *testing.T, r *Result, body func(*stack)) {
	defer func() {
		if p := recover(); p != nil {
			if _, ok := p.(abort); !ok {
				r.Checks = append(r.Checks, Check{Name: "the scenario ran", OK: false, Counter: "panic", Detail: fmt.Sprint(p)})
			}
		}
	}()
	env := []string{}
	if r.Scenario == "broadcast-fanout-planted" {
		env = append(env, "REHEARSAL_PLANT=per-viewer-fetch")
	}
	if r.Scenario == "broadcast-observe" {
		env = append(env, observeEnv(t)...)
	}
	body(up(t, r, r.Scenario, env...))
}

// ---- broadcast-fanout (50.21) ------------------------------------------------------------

// fanout: one channel × 3 rungs from the home; viewers 1 → 10 → 100 (→ 1,000 nightly) across 5
// locations. At every step the home's PUTs are segments × rungs exactly (its own count, the
// proxy count and the edge's), R2 is read at most twice per object per location (Miniflare has
// no cache lock), no viewer errors, and the copy rung's segments arrive within 1.5 s.
func fanout(s *stack) {
	top := envInt("BROADCAST_VIEWERS", 100)
	stepFor := time.Duration(envInt("BROADCAST_STEP_SECONDS", 60)) * time.Second
	const locations = 5
	// the runner's CPU, with each step's load (FanoutRow.Load): a slow step can be read against it
	s.note("runner: %s, %d CPUs", runnerCPU(), runtime.NumCPU())
	ch := newChannel("fanout")
	s.publish(ch)
	s.waitLive(ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.follow(ctx)
	fl := s.fleet(ch, locations)
	total := 0
	var last viewers.Summary
	for _, n := range []int{1, 10, 100, 1000} {
		if n > top {
			break
		}
		fl.Add(ctx, n-total)
		total = n
		time.Sleep(8 * time.Second) // the new players start (spread over a target duration)
		c0, p0, t0, l0 := s.counters(), fl.Snapshot(), time.Now(), s.loadMark()
		time.Sleep(stepFor)
		c1, p1, t1, l1 := s.counters(), fl.Snapshot(), time.Now(), s.loadMark()
		time.Sleep(6 * time.Second) // the step's last segments reach every rung and the log
		row, checks := fanoutStep(s, ch, n, locations, t0, t1, c0, c1, p0, p1)
		row.Load = load(l0, l1)
		s.r.Fanout = append(s.r.Fanout, row)
		for _, c := range checks {
			s.check(fmt.Sprintf("%d viewers: %s", n, c.Name), c.OK, c.Counter, c.Detail)
		}
		last = Summarise(p1, p0)
	}
	cancel()
	fl.Wait()
	s.r.Latency = Latency(last)

	// the three counts of the home's upload agree: the uploader's own report, the edge's answers
	// as the home's HTTP client (the proxy) saw them, and the edge's own count of stored segments
	// the test pattern stopped, nothing new is made; the broadcaster's next report (every 10 s)
	// then counts all it sent
	_, _, _ = s.do(http.MethodPost, s.home+"/encoder/stop", nil, nil)
	time.Sleep(22 * time.Second)
	m := s.homeMetrics()
	c := s.counters()
	puts := make([]HomePut, 0, len(m.Puts))
	for _, p := range m.Puts {
		puts = append(puts, HomePut{Rung: p.Rung, Seq: p.Seq, Status: p.Status, TookMS: p.TookMS, Err: p.Err})
	}
	for _, r := range rungs {
		edge := 0
		for _, w := range c.Workers {
			edge += w.IngestPutByRung[r]
		}
		// each segment stored once; an extra stored segment only where the home's PUT ended
		// with no answer (TallyRung)
		t := TallyRung(puts, r, reportCount(m.Report, r), edge)
		s.check("the home's counts agree ("+r+")", t.OK(), "home_puts", t.String())
	}
}

// Summarise is the fleet's summary over a step.
func Summarise(now, before []viewers.PlayerStats) viewers.Summary {
	return viewers.Summarise(now, before)
}

func reportCount(rep map[string]any, rung string) int {
	segs, _ := rep["segments"].(map[string]any)
	n, _ := segs[rung].(float64)
	return int(n)
}

func fanoutStep(s *stack, ch string, n, locations int, t0, t1 time.Time, c0, c1 edgeCounters,
	p0, p1 []viewers.PlayerStats) (FanoutRow, []Check) {
	m := s.homeMetrics()
	// the step's segments: the copy rung's made in it; each must be PUT exactly once per rung
	seqs := map[int64]bool{}
	for _, p := range m.Puts {
		at := time.UnixMilli(p.AtMS)
		if p.Rung == "src" && p.Status == http.StatusCreated && at.After(t0) && !at.After(t1) {
			seqs[p.Seq] = true
		}
	}
	perRung := map[string]map[int64]int{}
	// each segment's answers in order (a PUT sent twice names why: 429, 503, a client error)
	answers := map[string][]string{}
	var took []time.Duration
	var bytes int64
	puts := 0
	for _, p := range m.Puts {
		if !seqs[p.Seq] || p.Rung == "slate" {
			continue
		}
		puts++
		took = append(took, time.Duration(p.TookMS)*time.Millisecond)
		a := strconv.Itoa(p.Status)
		if p.Err != "" {
			a = p.Err
		}
		k := fmt.Sprintf("%s/%d", p.Rung, p.Seq)
		answers[k] = append(answers[k], a)
		bytes += p.Bytes
		if perRung[p.Rung] == nil {
			perRung[p.Rung] = map[int64]int{}
		}
		perRung[p.Rung][p.Seq]++
	}
	exact := len(seqs) > 0
	var odd []string
	for _, r := range rungs {
		for seq := range seqs {
			if perRung[r][seq] != 1 {
				exact = false
				odd = append(odd, fmt.Sprintf("%s/%d×%d %v", r, seq, perRung[r][seq], answers[fmt.Sprintf("%s/%d", r, seq)]))
			}
		}
	}
	edgePuts := 0
	for i, w := range c1.Workers {
		for _, r := range rungs {
			edgePuts += w.IngestPutByRung[r]
			if i < len(c0.Workers) {
				edgePuts -= c0.Workers[i].IngestPutByRung[r]
			}
		}
	}
	reads := segmentReads(c0, c1, ch)
	r2, maxPer := 0, 0
	worst := ""
	for key, byLoc := range reads {
		for loc, k := range byLoc {
			r2 += k
			if k > maxPer {
				maxPer, worst = k, fmt.Sprintf("%s at location %d", key, loc)
			}
		}
	}
	doGets := 0
	for i, w := range c1.Workers {
		doGets += w.DOGet
		if i < len(c0.Workers) {
			doGets -= c0.Workers[i].DOGet
		}
	}
	all := viewers.Summarise(p1, p0)
	// the copy rung's latency (software rungs are slower in CI; the targets hold on copy)
	var c1src, c0src []viewers.PlayerStats
	for i, p := range p1 {
		if i%len(rungs) == 0 {
			c1src = append(c1src, p)
			if i < len(p0) {
				c0src = append(c0src, p0[i])
			} else {
				c0src = append(c0src, viewers.PlayerStats{})
			}
		}
	}
	src := viewers.Summarise(c1src, c0src)
	segAnswers := make([]int, len(SegAnswerBoundsMS)+1)
	for i, w := range c1.Workers {
		for b, k := range w.SegAnswerMS {
			if b < len(segAnswers) {
				segAnswers[b] += k
				if i < len(c0.Workers) && b < len(c0.Workers[i].SegAnswerMS) {
					segAnswers[b] -= c0.Workers[i].SegAnswerMS[b]
				}
			}
		}
	}
	edgeAnswer := BucketQuantile(segAnswers, SegAnswerBoundsMS, 0.95)
	if math.IsInf(edgeAnswer, 1) {
		edgeAnswer = -1
	}
	row := FanoutRow{Viewers: n, Seconds: int(t1.Sub(t0).Seconds()), Segments: len(seqs), Rungs: len(rungs),
		HomePuts: puts, HomeBytes: bytes, EdgePuts: edgePuts, R2Reads: r2, R2MaxPerLoc: maxPer, DOGets: doGets,
		ViewerSegs: all.Segments, ViewerErrors: all.Errors, FetchP95: src.Fetch.P95, AfterHomeP95: src.AfterHome.P95,
		FirstByteP95: src.FirstByte.P95, EdgeAnswerP95: edgeAnswer, ReloadLateP95: all.Late.P95}
	// a viewer finds a segment at its next playlist (a target duration at most, the playlist's
	// 1 s cache) and then has 1.5 s to fetch it
	afterHomeMax := 4000.0 + 1000 + 1500
	putTook := viewers.PercentilesOf(took)
	checks := []Check{
		{Name: "home PUTs = segments × rungs", OK: exact && puts == len(seqs)*len(rungs), Counter: "home_puts",
			Detail: fmt.Sprintf("%d PUTs for %d segments × %d rungs; the edge answered a PUT in p95 %.0f ms, max %.0f ms %s",
				puts, len(seqs), len(rungs), putTook.P95, putTook.Max, strings.Join(odd, " "))},
		{Name: "R2 reads ≤ 2 per object per location", OK: maxPer <= 2, Counter: "r2_reads",
			Detail: fmt.Sprintf("%d reads of %d objects, at most %d (%s); bound %d locations × %d rungs per segment", r2, len(reads), maxPer, worst, locations, len(rungs))},
		{Name: "no viewer error", OK: all.Errors == 0, Counter: "viewer_errors",
			Detail: fmt.Sprintf("%d errors %v", all.Errors, all.FirstErrors)},
		{Name: "every viewer plays", OK: all.Segments >= n, Counter: "viewer_segments",
			Detail: fmt.Sprintf("%d segments for %d viewers", all.Segments, n)},
		{Name: "copy-rung segment fetch p95 ≤ 1.5 s", OK: src.Fetch.N == 0 && n < len(rungs) || src.Fetch.P95 <= 1500, Counter: "viewer_fetch_p95",
			Detail: fmt.Sprintf("p95 %.0f ms over %d", src.Fetch.P95, src.Fetch.N)},
		{Name: "copy-rung segment ≤ 1.5 s after its playlist names it", OK: src.AfterHome.N == 0 || src.AfterHome.P95 <= afterHomeMax, Counter: "viewer_after_home_p95",
			Detail: fmt.Sprintf("p95 %.0f ms after the home's PUT over %d (bound %.0f ms)", src.AfterHome.P95, src.AfterHome.N, afterHomeMax)},
	}
	return row, checks
}

// ---- broadcast-fanout-planted (T03) ------------------------------------------------------

// planted is the fan-out against an edge whose cache is off (every viewer's segment an R2
// read): it passes only when the fan-out fails, and fails on the R2 counter.
func planted(s *stack) {
	inner := &Result{Scenario: "broadcast-fanout (planted)"}
	outer := s.r
	s.r = inner
	func() {
		defer func() {
			if p := recover(); p != nil {
				if _, ok := p.(abort); !ok {
					panic(p)
				}
			}
		}()
		s.t.Setenv("BROADCAST_VIEWERS", "100")
		s.t.Setenv("BROADCAST_STEP_SECONDS", strings.TrimSpace(fmt.Sprint(envInt("BROADCAST_PLANTED_STEP_SECONDS", 30))))
		fanout(s)
	}()
	s.r = outer
	s.r.Fanout = inner.Fanout
	var on []string
	homeOK := true
	for _, c := range inner.Failed() {
		on = append(on, c.Counter)
		if c.Counter == "home_puts" || c.Counter == "setup" {
			homeOK = false
		}
	}
	sort.Strings(on)
	s.check("the planted per-viewer fetch fails the fan-out", len(inner.Failed()) > 0, "selftest", "failed on:", on)
	s.check("it fails naming the R2 counter", strings.Contains(strings.Join(on, " "), "r2_reads"), "selftest", on)
	s.check("the home's upload still holds (it does not depend on the cache)", homeOK, "selftest", on)
}

// ---- broadcast-home (50.22) --------------------------------------------------------------

// homeOffline: the channel plays; its encoder dies for 30 s, then starts again. The slate within
// 15 s, live again after the restart, and no player error.
func homeOffline(s *stack) {
	ch := newChannel("home")
	s.publish(ch)
	s.waitLive(ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.follow(ctx)
	fl := s.fleet(ch, 5)
	fl.Add(ctx, 30)
	time.Sleep(12 * time.Second)

	_, _, _ = s.do(http.MethodPost, s.home+"/encoder/stop", nil, nil)
	killed := time.Now()
	slate, ok := waitFor(40*time.Second, func() bool {
		code, _, pl := s.playlist(ch, "src")
		return code == http.StatusOK && len(pl.Segments) > 0 && pl.Segments[len(pl.Segments)-1].Slate
	})
	s.timing("slate_after_kill", slate)
	s.check("the slate within 15 s of the encoder dying", ok && slate <= 15*time.Second, "slate_s", slate.Round(100*time.Millisecond))
	if wait := 30*time.Second - time.Since(killed); wait > 0 {
		time.Sleep(wait)
	}
	_, _, _ = s.do(http.MethodPost, s.home+"/encoder/start", nil, nil)
	back, ok := waitFor(90*time.Second, func() bool { return s.liveOnEveryRung(ch) })
	s.timing("live_after_restart", back)
	s.check("live again after the restart", ok, "live_again_s", back.Round(100*time.Millisecond))
	time.Sleep(10 * time.Second) // the players' next playlists
	cancel()
	fl.Wait()
	sum := viewers.Summarise(fl.Snapshot(), nil)
	sawSlate, again := 0, 0
	for _, p := range fl.Snapshot() {
		if !p.FirstSlate.IsZero() {
			sawSlate++
		}
		if !p.LiveAgain.IsZero() {
			again++
		}
	}
	s.check("every player saw the slate, then live again", sawSlate == sum.Viewers && again == sum.Viewers, "players",
		fmt.Sprintf("%d of %d saw the slate, %d live again", sawSlate, sum.Viewers, again))
	s.check("no player error", sum.Errors == 0, "viewer_errors", sum.Errors, sum.FirstErrors)
	s.r.Latency = Latency(sum)
}

// ---- broadcast-home-offline-24h (50.21) --------------------------------------------------

// offline24h: the home stops; the slate, then off_air once a day has passed (the fast clock).
func offline24h(s *stack) {
	ch := newChannel("away")
	s.publish(ch)
	s.waitLive(ch)
	_, err := compose(s.t, s.project, nil, "stop", "home")
	s.must("the home stops", err == nil, err)
	d, ok := waitFor(40*time.Second, func() bool {
		code, _, pl := s.playlist(ch, "src")
		return code == http.StatusOK && len(pl.Segments) > 0 && pl.Segments[len(pl.Segments)-1].Slate
	})
	s.check("the slate when the home stops", ok, "slate_s", d.Round(100*time.Millisecond))
	advance := func(dur time.Duration) {
		code, raw, err := s.do(http.MethodPost, fmt.Sprintf("%s/__rehearsal/clock?advance=%d", s.edge, dur.Milliseconds()), nil, nil)
		s.must("the fast clock moves", err == nil && code == http.StatusOK, code, string(raw))
		s.skew += dur
		time.Sleep(1500 * time.Millisecond) // the cached playlist's second
	}
	advance(23*time.Hour + 50*time.Minute)
	code, why, pl := s.playlist(ch, "src")
	slate := len(pl.Segments) > 0 && pl.Segments[len(pl.Segments)-1].Slate
	s.check("still the slate at 23 h 50", code == http.StatusOK && slate, "off_air_24h", code, why, "slate:", slate)
	advance(15 * time.Minute)
	code, why, _ = s.playlist(ch, "src")
	s.check("off_air once 24 h have passed", code == http.StatusServiceUnavailable && why == "off_air", "off_air_24h", code, why)
}

// ---- broadcast-kill (50.25) --------------------------------------------------------------

// kill: 200 viewers. The cloud's list takes the channel off air (stop ≤ 60 s for every viewer,
// the home stops uploading), then a new list lifts it (live again). Then, as with the cloud's
// API down, the edge's break-glass variable stops it (≤ 60 s) and its removal lifts it.
func kill(s *stack) {
	ch := newChannel("kill")
	viewersN := envInt("BROADCAST_KILL_VIEWERS", 200)
	s.publish(ch)
	s.waitLive(ch)

	stopped := func(fl *viewers.Fleet, since time.Time) (time.Duration, int, int) {
		var worst time.Duration
		off := 0
		snap := fl.Snapshot()
		for _, p := range snap {
			if !p.FirstOffAir.IsZero() && !p.FirstOffAir.Before(since) {
				off++
				worst = max(worst, p.FirstOffAir.Sub(since))
			}
		}
		return worst, off, len(snap)
	}
	again := func(fl *viewers.Fleet) (int, int) {
		n, snap := 0, fl.Snapshot()
		for _, p := range snap {
			if !p.LiveAgain.IsZero() {
				n++
			}
		}
		return n, len(snap)
	}

	// 1. the cloud's list
	ctx, cancel := context.WithCancel(context.Background())
	fl := s.fleet(ch, 5)
	fl.Add(ctx, viewersN)
	time.Sleep(15 * time.Second)
	t0 := time.Now()
	s.installList(1, []viewers.RevocationEntry{{ID: "r-rehearsal-1", Kind: "channel", Ref: ch, Action: "off_air", Reason: "notice"}})
	_, all := waitFor(75*time.Second, func() bool { _, off, n := stopped(fl, t0); return off == n })
	worst, off, n := stopped(fl, t0)
	s.timing("stop_list", worst)
	s.check("the list stops every viewer within 60 s", all && worst <= 60*time.Second, "stop_s_list",
		fmt.Sprintf("%d of %d off air, the last after %s", off, n, worst.Round(100*time.Millisecond)))
	time.Sleep(10 * time.Second)
	m := s.homeMetrics()
	lastPut := int64(0)
	for _, p := range m.Puts {
		if p.Status == http.StatusCreated && p.Rung != "slate" {
			lastPut = max(lastPut, p.AtMS)
		}
	}
	s.check("the home stops uploading", lastPut < t0.Add(60*time.Second).UnixMilli() && m.State == "revoked", "home_puts_after_stop",
		fmt.Sprintf("home %s %s, last segment stored %s after the list", m.State, m.Detail, time.UnixMilli(lastPut).Sub(t0).Round(time.Second)))

	// the lift
	t1 := time.Now()
	s.installList(2, nil)
	d, ok := waitFor(120*time.Second, func() bool { a, n := again(fl); return a == n })
	a, total := again(fl)
	s.timing("live_after_lift", d)
	s.check("live again after the lift", ok, "live_again_list", fmt.Sprintf("%d of %d playing again after %s", a, total, d.Round(100*time.Millisecond)))
	s.check("the home broadcasts again", s.homeMetrics().State == "live", "home_state", s.homeMetrics().State)
	_ = t1
	cancel()
	fl.Wait()
	sum := viewers.Summarise(fl.Snapshot(), nil)
	s.check("no player error (list)", sum.Errors == 0, "viewer_errors", sum.Errors, sum.FirstErrors)

	// 2. break-glass: no cloud in the way (the edge's own variable, a redeploy)
	_, ok = waitFor(60*time.Second, func() bool { return s.liveOnEveryRung(ch) })
	s.must("live before break-glass", ok)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	fl = s.fleet(ch, 5)
	fl.Add(ctx, viewersN)
	time.Sleep(15 * time.Second)
	t2 := time.Now()
	dep := s.deploy(map[string]string{"BROADCAST_OFF_CHANNELS": ch})
	s.timing("break_glass_deploy", dep)
	_, all = waitFor(75*time.Second, func() bool { _, off, n := stopped(fl, t2); return off == n })
	worst, off, n = stopped(fl, t2)
	s.timing("stop_break_glass", worst)
	s.check("break-glass stops every viewer within 60 s", all && worst <= 60*time.Second, "stop_s_break_glass",
		fmt.Sprintf("%d of %d off air, the last after %s (the redeploy took %s)", off, n, worst.Round(100*time.Millisecond), dep.Round(100*time.Millisecond)))
	s.deploy(map[string]string{"BROADCAST_OFF_CHANNELS": ""})
	if st := s.homeMetrics().State; st != "live" {
		// the API's next sync gives a stopped home its channel back (here: the scenario)
		s.note("break-glass left the home %s; published again as the API's sync does", st)
		s.publish(ch)
	}
	d, ok = waitFor(120*time.Second, func() bool { a, n := again(fl); return a == n })
	a, total = again(fl)
	s.timing("live_after_break_glass", d)
	s.check("live again after break-glass is lifted", ok, "live_again_break_glass", fmt.Sprintf("%d of %d after %s", a, total, d.Round(100*time.Millisecond)))
	cancel()
	fl.Wait()
	sum = viewers.Summarise(fl.Snapshot(), nil)
	// a redeploy drops connections for a moment; a player retries within its buffer
	s.check("no player error (break-glass)", sum.Errors == 0, "viewer_errors", sum.Errors, sum.FirstErrors)
}

// ---- broadcast-edge-deploy (50.21) -------------------------------------------------------

// edgeDeploy: 100 viewers; a new edge version rolls out gradually (10 %, 50 %, 100 % of
// sessions), then every Worker is redeployed (the Durable Objects' code too). No viewer error;
// the channel's Durable Object keeps its state (the playlist goes on, nothing restarts).
func edgeDeploy(s *stack) {
	ch := newChannel("deploy")
	s.publish(ch)
	s.waitLive(ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.follow(ctx)
	fl := s.fleet(ch, 5)
	fl.Add(ctx, envInt("BROADCAST_DEPLOY_VIEWERS", 100))
	time.Sleep(15 * time.Second)
	_, _, before := s.playlist(ch, "src")
	for _, pct := range []int{10, 50, 100} {
		code, raw, err := s.do(http.MethodPost, fmt.Sprintf("%s/__rehearsal/rollout?pct=%d", s.edge, pct), nil, nil)
		s.must(fmt.Sprintf("the rollout reaches %d %%", pct), err == nil && code == http.StatusOK, code, string(raw))
		time.Sleep(15 * time.Second)
	}
	dep := s.deploy(map[string]string{"EDGE_VERSION": "2"})
	s.timing("redeploy", dep)
	time.Sleep(30 * time.Second)
	_, _, after := s.playlist(ch, "src")
	cancel()
	fl.Wait()
	sum := viewers.Summarise(fl.Snapshot(), nil)
	s.check("no viewer error through the rollout and the redeploy", sum.Errors == 0, "viewer_errors", sum.Errors, sum.FirstErrors)
	s.check("every viewer still live", sum.States[viewers.StateLive] == sum.Viewers, "players", sum.States)
	firstBefore, lastAfter := int64(0), int64(0)
	if len(before.Segments) > 0 && len(after.Segments) > 0 {
		firstBefore, _ = viewers.SegmentSeq(before.Segments[len(before.Segments)-1].URI)
		lastAfter, _ = viewers.SegmentSeq(after.Segments[len(after.Segments)-1].URI)
	}
	s.check("the Durable Object kept its state (the playlist went on)",
		after.Sequence >= before.Sequence && lastAfter > firstBefore && !after.Segments[len(after.Segments)-1].Slate,
		"do_state", fmt.Sprintf("media sequence %d → %d, newest segment %d → %d", before.Sequence, after.Sequence, firstBefore, lastAfter))
	s.r.Latency = Latency(sum)
}
