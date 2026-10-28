package frontdoor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/verify"
)

// TV change #54 (2026-10-28): a station's first sample read its live.ts while the
// station started (and restarted under the read), kept a few packets without a PAT,
// and failed ts.conformity ("live.ts: no PAT/PMT") on that one read. A live.ts read now
// goes on for its first PAT/PMT, and one cut short is too short to judge; a stream that
// really carries none still fails.

// liveFrame is one video frame of a test live.ts: a PES with a PCR, PAT and PMT before
// it when psi (psiCC their continuity counter). The stream clock runs 3000 ticks
// (33 ms) a frame.
func liveFrame(n int64, psi bool, psiCC, cc byte) []byte {
	var b []byte
	if psi {
		b = append(b, patPMT(psiCC)...)
	}
	pts := (n * 3000) % (1 << 33)
	pes := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5}
	pes = append(pes, ptsBytes(pts)...)
	nal := byte(0x41)
	if n%5 == 0 {
		nal = 0x65
	}
	pes = append(pes, 0, 0, 0, 1, nal, 0x88)
	p := make([]byte, 188)
	p[0], p[1], p[2] = 0x47, 0x40|0x01, 0x00 // PID 0x100, payload unit start
	p[3] = 0x30 | cc&0x0f                    // adaptation + payload
	base := pts - 9000
	af := []byte{7, 0x10, byte(base >> 25), byte(base >> 17), byte(base >> 9), byte(base >> 1), byte(base<<7) | 0x7e, 0}
	copy(p[4:], af)
	n2 := copy(p[4+len(af):], pes)
	for i := 4 + len(af) + n2; i < 188; i++ {
		p[i] = 0xff
	}
	return append(b, p...)
}

// liveTSServer serves a channel's live.ts a frame every 10 ms of wall clock. psi says
// whether frame i carries a PAT/PMT; stallAfter > 0 stops sending after that many
// frames and holds the connection (a station restarting under the read).
func liveTSServer(t *testing.T, psi func(i int) bool, stallAfter int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/live.ts") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		base := time.Now().UnixNano() / int64(10*time.Millisecond)
		var psiCC byte
		for i := 0; ; i++ {
			if stallAfter > 0 && i == stallAfter {
				<-r.Context().Done()
				return
			}
			p := psi(i)
			if _, err := w.Write(liveFrame(base+int64(i), p, psiCC, byte(i))); err != nil {
				return
			}
			if p {
				psiCC++
			}
			fl.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// tsVerdict is ts.conformity of a staging sample with good segments and live.ts st.
func tsVerdict(t *testing.T, st verify.TSReport) verify.Result {
	t.Helper()
	var good []byte
	for i := int64(0); i < 90; i++ {
		good = append(good, liveFrame(1000+i, i%10 == 0, byte(i/10), byte(i))...)
	}
	now := time.Now()
	smp := &verify.Sample{Status: 200, At: now, TS: verify.AnalyzeTS(good, verify.Options{}), Stream: st,
		Playlist: verify.Playlist{Target: 2, Independent: true, AllPDT: true,
			Segs: []verify.Seg{{URI: "seg-1.ts", PDT: now.Add(-2 * time.Second), Dur: 2}}}}
	for _, r := range verify.Evaluate(smp, nil, verify.Expect{Segment: 2}, now) {
		if r.Check == "ts.conformity" {
			return r
		}
	}
	t.Fatal("no ts.conformity")
	return verify.Result{}
}

func TestLiveTSReadAtAStationsStart(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	s := &Slots{StreamPSIWait: 600 * time.Millisecond}
	read := func(base string) verify.TSReport {
		return s.sampleStream(ctx, base, id, 150*time.Millisecond, time.Now())
	}

	// the read joins before the muxer's first PAT/PMT (they come 300 ms in): it reads
	// on until it has them, and the stream passes
	late := read(liveTSServer(t, func(i int) bool { return i >= 30 && i%10 == 0 }, 0))
	if late.PATs == 0 || late.PMTs == 0 {
		t.Fatalf("the read stopped before the first PAT/PMT: %+v", late)
	}
	if r := tsVerdict(t, late); r.Result != verify.Pass {
		t.Fatalf("a start-of-stream read: %+v", r)
	}

	// the station restarts under the read: a few packets, no PAT, then nothing: too
	// short to judge (pending: measured again), not a fail
	cut := read(liveTSServer(t, func(int) bool { return false }, 3))
	if cut.Packets == 0 || cut.PATs != 0 || verify.LiveTSJudged(cut) {
		t.Fatalf("cut read %+v", cut)
	}
	if r := tsVerdict(t, cut); r.Result != verify.Pending || !strings.Contains(r.Value, "too short") {
		t.Fatalf("a read cut short: %+v", r)
	}

	// a stream that carries no PAT/PMT at all: the read gives up after StreamPSIWait
	// with seconds of stream clock read, and ts.conformity fails
	start := time.Now()
	none := read(liveTSServer(t, func(int) bool { return false }, 0))
	if took := time.Since(start); took < 700*time.Millisecond || took > 3*time.Second {
		t.Fatalf("the read took %s (want the sample and the PSI wait)", took)
	}
	if none.PATs != 0 || !verify.LiveTSJudged(none) {
		t.Fatalf("no-PSI read %+v", none)
	}
	if r := tsVerdict(t, none); r.Result != verify.Fail || !strings.Contains(r.Value, "live.ts: no PAT/PMT") {
		t.Fatalf("a stream without PAT/PMT: %+v", r)
	}
}

// A rolling probe ends on a settled sample only: a channel whose last sample failed (or
// is pending) stays warm and is measured again, so a second sample decides (the API fails
// a check on two fails in a row); it ends once a sample passes.
func TestRollingProbeEndsOnASettledSample(t *testing.T) {
	g, _, _, _ := stagingRig(t)
	rolling := uuid.New()
	g.slots.mu.Lock()
	run := g.slots.stage
	run.plan.Probe = g.ch.String()
	run.plan.ProbeSeconds = 5
	run.plan.Changed = []string{rolling.String()}
	g.slots.mu.Unlock()
	g.slots.SyncWarm(context.Background())

	sample := func(ts verify.Result) {
		g.slots.mu.Lock()
		run.since[rolling] = time.Now().Add(-time.Minute)
		run.firstSeg[rolling] = time.Now().Add(-50 * time.Second)
		run.samples[rolling] = &ChannelProbe{Channel: rolling.String(), At: time.Now(),
			Staging: &verify.Sample{Status: 200, TS: verify.TSReport{Duration: 10}},
			Results: []verify.Result{{Check: "channel.first_segment", Result: verify.Pass}, ts}}
		g.slots.mu.Unlock()
		g.slots.SyncWarm(context.Background())
	}
	warm := func() bool {
		for _, w := range g.slots.StagingView().Warm {
			if w.Channel == rolling.String() {
				return true
			}
		}
		return false
	}
	if !warm() {
		t.Fatal("the rolling channel is not warm")
	}
	sample(verify.Result{Check: "ts.conformity", Result: verify.Fail, Value: "live.ts: no PAT/PMT in 4.0 s"})
	if !warm() || len(g.slots.StagingView().Probed) != 0 {
		t.Fatal("a rolling probe ended on a failed sample")
	}
	sample(verify.Result{Check: "ts.conformity", Result: verify.Pending, Value: "live.ts: too short"})
	if !warm() {
		t.Fatal("a rolling probe ended on a pending sample")
	}
	sample(verify.Result{Check: "ts.conformity", Result: verify.Pass, Value: "ok"})
	if v := g.slots.StagingView(); warm() || len(v.Probed) != 1 || v.Probed[0] != rolling.String() {
		t.Fatalf("a passing sample did not end the probe: warm %v probed %v", warm(), v.Probed)
	}
}
