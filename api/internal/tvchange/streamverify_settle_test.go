package tvchange

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// coldStart is TV change #53 (2026-10-28) as the front door saw one channel of the
// staging slot: warmed at warm, its first segment firstAfter later, its output lag(t)
// seconds behind the wall clock at sample time t.
type coldStart struct {
	w          *svWorld
	warm       time.Time
	firstAfter float64
	lag        func(at time.Time) float64
	block      *OnAir
	live       *OnAir
}

func (cs *coldStart) install() {
	w := cs.w
	w.fs.view = func(p StagingPlan) *StagingView {
		at := w.sampled
		blockStart := time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)
		blk := &OnAir{Seq: 1, Kind: "program", StartAt: blockStart, EndAt: blockStart.Add(time.Hour), Title: "Tafsir 1"}
		if cs.block != nil {
			blk = cs.block
		}
		smp := &ChannelSample{Channel: w.ch, WarmSince: cs.warm, At: at, StationAt: at}
		if at.Sub(cs.warm).Seconds() < cs.firstAfter {
			smp.Results = []ProbeResult{{Check: "channel.first_segment", Result: "pending", Value: "no segment yet"}}
		} else {
			lag := cs.lag(at)
			reached := at.Add(-time.Duration(lag * float64(time.Second)))
			smp.Station = &StationInfo{ID: w.ch, Encoder: "videotoolbox", Reached: &reached, Block: blk}
			if cs.live != nil {
				smp.LiveStation = &StationInfo{ID: w.ch, Block: cs.live}
			}
			smp.FirstSegmentAfter = cs.firstAfter
			wc := ProbeResult{Check: "channel.wallclock", Result: "pass",
				Value: fmt.Sprintf("newest segment ends %+.3f s from now", -lag-1)}
			if lag+1 >= 5 { // hls_time 4 s + 1
				wc.Result = "fail"
			}
			smp.Results = append(passing(), wc)
		}
		return &StagingView{Active: true, Plan: &p, Slot: "amber", Healthy: true,
			Status: &SlotStatus{Slot: "amber", Role: "staging", Release: p.ReleaseID, Version: "v2", PID: 92139,
				Stations: []string{w.ch}},
			Budget: 28, Capacity: 28, Warm: []WarmEntry{{Channel: w.ch, Why: "probe", Since: cs.warm}},
			Samples: map[string]*ChannelSample{w.ch: smp}}
	}
}

// step moves the clock and the front door's sample by d and ticks.
func (w *svWorld) step(d time.Duration) {
	w.t.Helper()
	w.now = w.now.Add(d)
	w.sampled = w.now.Add(-500 * time.Millisecond)
	w.tick()
}

func checkResults(c *Change, name string) []string {
	var out []string
	xs, _ := c.VerifyReport["checks"].([]any)
	for _, x := range xs {
		m, _ := x.(map[string]any)
		if m["check"] == name {
			out = append(out, fmt.Sprint(m["result"])+": "+fmt.Sprint(m["value"]))
		}
	}
	return out
}

// TV change #53: a cold-started staging station 7-12 s behind the wall clock in its
// first seconds was failed on its second sample (channel.position, channel.wallclock),
// 10 s after its first segment, and the home's update rolled back. A station catching
// up is settling, not failing: the checks warn and hold the soak, and the change is
// ready once the output is on the wall clock.
func TestStreamVerificationLetsAColdStartSettle(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newSVWorld(t, be.DB)
			start := w.now
			// 12 s behind at its first segment, catching up half a second a second
			cs := &coldStart{w: w, warm: start, firstAfter: 9.5, lag: func(at time.Time) float64 {
				return max(0.3, 12-(at.Sub(start).Seconds()-9.5)/2)
			}}
			cs.install()
			c := w.staged("v2")
			w.sampled = w.now
			w.tick()                 // staged → verifying; no segment yet
			for i := 0; i < 3; i++ { // its samples 0, 10 and 20 s after the first segment: 12, 7, 2 s behind
				w.step(10 * time.Second)
				got := w.get(c.ID)
				if got.State != StateVerifying {
					t.Fatalf("sample %d (%v after the first segment): %s %v", i, w.sampled.Sub(start)-9500*time.Millisecond,
						got.State, got.VerifyReport["reasons"])
				}
				if i < 2 {
					pos := strings.Join(checkResults(got, "channel.position"), "; ")
					if !strings.HasPrefix(pos, "warn: output at -") || !strings.Contains(pos, "settling") {
						t.Fatalf("sample %d: channel.position %q", i, pos)
					}
					if s, _ := got.VerifyReport["status"].(string); !strings.Contains(s, "settling") {
						t.Fatalf("sample %d: the soak runs while settling: %q", i, s)
					}
				}
			}
			for i := 0; i < 4 && w.get(c.ID).State == StateVerifying; i++ {
				w.step(10 * time.Second)
			}
			got := w.get(c.ID)
			if got.State != StateReady {
				t.Fatalf("a station that caught up: %s %v", got.State, got.VerifyReport["reasons"])
			}
			if pos := checkResults(got, "channel.position"); len(pos) != 1 || !strings.HasPrefix(pos[0], "pass") {
				t.Fatalf("channel.position %v", pos)
			}
		})
	}
}

// Not weaker: a station that stays behind the wall clock past outputSettle fails
// channel.position and channel.wallclock on two samples in a row, as before.
func TestStreamVerificationFailsAStationThatStaysBehind(t *testing.T) {
	be := tvchangetest.Backends(t)[0]
	w := newSVWorld(t, be.DB)
	cs := &coldStart{w: w, warm: w.now, firstAfter: 9.5, lag: func(time.Time) float64 { return 12 }}
	cs.install()
	c := w.staged("v2")
	w.sampled = w.now
	w.tick()
	for i := 0; i < 8 && w.get(c.ID).State == StateVerifying; i++ {
		w.step(10 * time.Second)
	}
	got := w.get(c.ID)
	if got.State != StateDraft || failReason(t, w, c.ID) != "verification_failed" {
		t.Fatalf("12 s behind for a minute: %s", got.State)
	}
	why := fmt.Sprint(got.VerifyReport["reasons"])
	if !strings.Contains(why, "channel.position") || !strings.Contains(why, "channel.wallclock") ||
		strings.Contains(why, "settling") {
		t.Fatalf("reasons %s", why)
	}
	// failed on its first two samples past the settle window, not later
	if after := w.sampled.Sub(cs.warm) - 9500*time.Millisecond; after > outputSettle+20*time.Second {
		t.Fatalf("failed %v after the first segment", after)
	}
}

// TV change #53's channel.block: a programme whose file this host cannot read airs a
// filler in its place, as on live; the slot says which block the filler stands in
// for, and the check warns. A stand-in for another block, a synthetic block with no
// stand-in, or a stand-in where live airs the file itself still fail.
func TestStreamVerificationStandInForAnUnreadableFile(t *testing.T) {
	t0 := time.Date(2026, 6, 15, 20, 10, 0, 0, time.UTC)
	const tafsir1 = "22222222-2222-2222-2222-222222222222" // the scheduled programme's video
	filler := func(standIn int64) *OnAir {
		return &OnAir{Seq: 77, Kind: "filler", VideoID: "11111111-1111-1111-1111-111111111111", StartAt: t0,
			EndAt: t0.Add(36 * time.Second), Title: "a filler", Synthetic: true, StandInFor: standIn}
	}
	for _, c := range []struct {
		name  string
		block *OnAir
		live  *OnAir
		want  string
	}{
		{"stand-in for the scheduled block", filler(1), nil, ResultWarn},
		{"stand-in for another block", filler(2), nil, ResultFail},
		{"synthetic without a stand-in", filler(0), nil, ResultFail},
		{"live reads the file", filler(1), &OnAir{Seq: 1, Kind: "program",
			StartAt: time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC), EndAt: time.Date(2026, 6, 15, 21, 0, 0, 0, time.UTC),
			VideoID: tafsir1, Title: "Tafsir 1"}, ResultFail},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newSVWorld(t, tvchangetest.Backends(t)[0].DB)
			w.exec(`UPDATE channel_programs SET video_id = $1 WHERE channel_id = $2 AND seq = 1`, tafsir1, w.ch)
			cs := &coldStart{w: w, warm: w.now.Add(-time.Minute), firstAfter: 2, lag: func(time.Time) float64 { return 0.2 },
				block: c.block, live: c.live}
			cs.install()
			ch := w.staged("v2")
			w.sampled = w.now
			w.tick()
			w.step(5 * time.Second)
			got := w.get(ch.ID)
			blk := checkResults(got, "channel.block")
			if len(blk) != 1 || !strings.HasPrefix(blk[0], c.want+":") {
				t.Fatalf("channel.block %v, want %s (state %s)", blk, c.want, got.State)
			}
			if c.want == ResultWarn && !strings.Contains(blk[0], "cannot be read on this host") {
				t.Fatalf("channel.block %v", blk)
			}
		})
	}
}
