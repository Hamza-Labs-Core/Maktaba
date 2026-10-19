package frontdoor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
)

// Stories 33.7 and 33.9 in the front door, against fake slots whose output is
// splice-ready (story 33.4): PTS = wall-clock ticks, a frame every 1/30 s on the
// wall-clock frame grid, an IDR every 2 s on the wall-clock grid, 2 s HLS
// segments with program dates on the same grid. Each slot marks its frames (the
// byte after the NAL header), so a test sees which slot a client is on.

const alignedFrame = 3000 // ticks at 30 fps

type alignedSlot struct {
	// keyEvery: frames between IDRs (60: every 2 s, the default grid)
	keyEvery int64
	name     string
	mark     byte
	release  string
	srv      *httptest.Server
	mu       sync.Mutex
	role     string
	epoch    int64
	warm     []string
	drained  bool
}

func alignedVideo(pts int64, key bool, cc byte, mark byte) []byte {
	pes := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5}
	pes = append(pes, ptsBytes(pts)...)
	nal := byte(0x41)
	if key {
		nal = 0x65
	}
	pes = append(pes, 0, 0, 0, 1, nal, mark)
	return tsPacket(0x100, true, cc, pes, key)
}

func newAlignedSlot(t *testing.T, name string, mark byte, release string, stations ...string) *alignedSlot {
	f := &alignedSlot{name: name, mark: mark, release: release, role: slot.RoleIdle, warm: stations, keyEvery: 60}
	r := chi.NewRouter()
	r.Get("/internal/tv/slot/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, f.status())
	})
	r.Post("/internal/tv/slot/warm", func(w http.ResponseWriter, r *http.Request) {
		var req slot.WarmRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.role, f.epoch = req.Role, req.Epoch
		f.mu.Unlock()
		writeJSON(w, 200, f.status())
	})
	r.Post("/internal/tv/slot/drain", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.drained = true
		f.mu.Unlock()
		writeJSON(w, 200, f.status())
	})
	r.Get("/stream/channel/{id}/index.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		// the segments that ended by now, on the 2 s wall-clock grid
		end := time.Now().Truncate(2 * time.Second)
		start := end.Add(-10 * time.Second)
		_, _ = w.Write(slotPlaylist(start.Unix()/2, 5, start, 2, false))
	})
	r.Get("/stream/channel/{id}/{seg}", func(w http.ResponseWriter, r *http.Request) {
		if chi.URLParam(r, "seg") != "live.ts" {
			_, _ = io.WriteString(w, f.name+":"+chi.URLParam(r, "seg"))
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		var cc byte
		// frames on the wall-clock grid from now (a real slot sends from its
		// muxer's current position: about now): frame n is at n/30 s since the
		// Unix epoch, PTS n*3000, an IDR every 60 frames (2 s)
		now := time.Now()
		n := now.Unix()*30 + int64(now.Nanosecond())*30/int64(time.Second) + 1
		for {
			at := time.Unix(n/30, (n%30)*int64(time.Second)/30)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Until(at)):
			}
			pts := (n * alignedFrame) % (1 << 33)
			f.mu.Lock()
			key := n%f.keyEvery == 0
			f.mu.Unlock()
			var b []byte
			if key || n%10 == 0 {
				b = append(b, patPMT(cc)...)
			}
			b = append(b, alignedVideo(pts, key, cc, f.mark)...)
			cc++
			if _, err := w.Write(b); err != nil {
				return
			}
			fl.Flush()
			n++
		}
	})
	f.srv = httptest.NewServer(r)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *alignedSlot) status() slot.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := slot.Status{Slot: f.name, Role: f.role, Epoch: f.epoch, Release: f.release, Stations: []string{}}
	if !f.drained {
		st.Stations = append(st.Stations, f.warm...)
	}
	return st
}

func (f *alignedSlot) isDrained() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.drained
}

func (f *alignedSlot) addr() string { return strings.TrimPrefix(f.srv.URL, "http://") }

func (f *alignedSlot) crash() {
	f.srv.CloseClientConnections()
	f.srv.Close()
}

type swapRig struct {
	fd                 *Handler
	slots              *Slots
	srv                *httptest.Server
	blue, green, amber *alignedSlot
	ch                 uuid.UUID
	tracker            *Tracker
}

func newSwapRig(t *testing.T) *swapRig {
	ch := uuid.New()
	g := &swapRig{ch: ch}
	g.blue = newAlignedSlot(t, "blue", 0xb1, "rel-1", ch.String())
	g.green = newAlignedSlot(t, "green", 0x61, "rel-1", ch.String())
	g.amber = newAlignedSlot(t, "amber", 0xa1, "rel-2", ch.String())
	store := placement.NewMemStore()
	store.SetHosts([]placement.Host{{ID: "home", Name: "mac", Role: placement.RoleHome, Enabled: true, Capacity: 8,
		Weight: 1, CanLive: true}})
	s := placement.DefaultSettings()
	hosts := &placement.Hosts{Source: store, Settings: settings{s}}
	hosts.Refresh(context.Background())
	svc := &placement.Service{Store: store, Hosts: hosts, Settings: settings{s}, Self: "home"}
	g.tracker = NewTracker()
	g.slots = &Slots{Host: "home", Addrs: map[string]string{"blue": g.blue.addr(), "green": g.green.addr(),
		"amber": g.amber.addr()}, Demand: g.tracker.Demands, ProbeEvery: 50 * time.Millisecond,
		Budget: func(context.Context) (int, int, int) { return 8, 0, 0 }}
	if err := g.slots.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.slots.probeAll(context.Background())
	if err := g.slots.AdoptStaging(context.Background(), "amber"); err != nil {
		t.Fatal(err)
	}
	g.slots.probeAll(context.Background())
	g.fd = &Handler{Svc: svc, Settings: settings{s}, Self: "home", Tracker: g.tracker, Slots: g.slots,
		SpliceRetry: 20 * time.Millisecond}
	r := chi.NewRouter()
	g.fd.Mount(r)
	g.srv = httptest.NewServer(r)
	t.Cleanup(g.srv.Close)
	return g
}

// tsClient reads a TS connection: per video frame its PTS and the slot mark.
type tsClient struct {
	mu     sync.Mutex
	pts    []int64
	marks  []byte
	ccBad  int
	closed error
	done   chan struct{}
}

func (g *swapRig) openTS(ctx context.Context, t *testing.T) *tsClient {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, g.srv.URL+"/stream/channel/"+g.ch.String()+"/live.ts", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	c := &tsClient{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		defer resp.Body.Close()
		r := bufio.NewReader(resp.Body)
		buf := make([]byte, 188)
		cc := map[uint16]int{}
		for {
			if _, err := io.ReadFull(r, buf); err != nil {
				c.mu.Lock()
				c.closed = err
				c.mu.Unlock()
				return
			}
			pid := uint16(buf[1]&0x1f)<<8 | uint16(buf[2])
			if buf[3]&0x10 != 0 {
				v := int(buf[3] & 0x0f)
				if last, ok := cc[pid]; ok && v != (last+1)&0x0f {
					c.mu.Lock()
					c.ccBad++
					c.mu.Unlock()
				}
				cc[pid] = v
			}
			if v, ok := readPTS(buf); ok {
				off := 4
				if buf[3]&0x20 != 0 {
					off += 1 + int(buf[4])
				}
				mark := buf[off+9+5+5]
				c.mu.Lock()
				c.pts = append(c.pts, v)
				c.marks = append(c.marks, mark)
				c.mu.Unlock()
			}
		}
	}()
	return c
}

func (c *tsClient) snapshot() ([]int64, []byte, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.pts...), append([]byte(nil), c.marks...), c.ccBad, c.closed
}

func (g *swapRig) playlist(t *testing.T) string {
	t.Helper()
	resp, err := http.Get(g.srv.URL + "/stream/channel/" + g.ch.String() + "/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// plEntry is one segment a playlist lists.
type plEntry struct {
	seq  int64
	uri  string
	pdt  time.Time
	dur  float64
	disc bool
}

func parseStitched(body string) []plEntry {
	var out []plEntry
	var seq int64
	var cur plEntry
	i := int64(0)
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"):
			seq, _ = strconv.ParseInt(strings.TrimPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case l == "#EXT-X-DISCONTINUITY":
			cur.disc = true
		case strings.HasPrefix(l, pdtTag):
			cur.pdt, _ = parsePDT(strings.TrimPrefix(l, pdtTag))
		case strings.HasPrefix(l, "#EXTINF:"):
			cur.dur, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(l, "#EXTINF:"), ","), 64)
		case l != "" && !strings.HasPrefix(l, "#"):
			cur.seq, cur.uri = seq+i, l
			i++
			out = append(out, cur)
			cur = plEntry{}
		}
	}
	return out
}

// TC1 + TC2 (unit, aligned): a swap of a watched channel at its instant — the
// TS client's connection is spliced onto the target at the IDR both slots have
// there (no closed connection, PTS continuing frame by frame, continuity
// counters continued, one change of slot); the HLS playlist continues on the
// target after one discontinuity, the media sequence one by one, the first
// target segment starting where the last live one ended; the operation ends
// with the target live and the old live slot previous (warm rollback target),
// the old standby drained (story 33.7 AC4, AC6, AC11).
func TestSwapSplicesAtTheChannelsInstant(t *testing.T) {
	if testing.Short() {
		t.Skip("real-time 2 s grid (TestSwapSplicesTSFast is the unit-tier variant)")
	}
	g := newSwapRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := g.openTS(ctx, t)
	g.tracker.TouchHLS(g.ch, httptest.NewRequest(http.MethodGet, "/", nil))
	var seen []plEntry
	addSeen := func(body string) {
		for _, e := range parseStitched(body) {
			if len(seen) == 0 || e.seq > seen[len(seen)-1].seq {
				seen = append(seen, e)
			}
		}
	}
	addSeen(g.playlist(t))
	time.Sleep(500 * time.Millisecond)

	// the instant: the next 2 s grid instant at least 3 s away (a block boundary
	// of both generations: both slots have an IDR there)
	at := time.Now().Add(3 * time.Second).Truncate(2 * time.Second).Add(2 * time.Second)
	g.slots.runOp(ctx, opRow{id: 7, kind: OpSwap, change: "c1", release: "rel-2", created: time.Now()}, "amber",
		[]switchRow{{ch: g.ch, at: at, state: switchPlanned}}, time.Now())
	if err := g.slots.Switch(ctx, "green", 99); err == nil || !strings.Contains(err.Error(), "swap_in_progress") {
		t.Fatalf("a routing switch during a swap: %v (want swap_in_progress, AC12)", err)
	}
	if r := g.slots.Route(g.ch); r != "blue" {
		t.Fatalf("before the instant the channel reads %s", r)
	}
	for time.Now().Before(at.Add(5 * time.Second)) {
		addSeen(g.playlist(t))
		g.slots.advance(ctx, time.Now())
		time.Sleep(300 * time.Millisecond)
	}
	g.slots.advance(ctx, at.Add(swapSettle+time.Second))
	if live, _ := g.slots.Live(); live != "amber" {
		t.Fatalf("after the swap live is %s", live)
	}
	if prev, until := g.slots.Previous(); prev != "blue" || !until.After(time.Now()) {
		t.Fatalf("previous %q until %v (want blue, the grace)", prev, until)
	}
	if !g.green.isDrained() {
		t.Fatalf("the old standby was not drained")
	}

	pts, marks, ccBad, closed := ts.snapshot()
	if closed != nil {
		t.Fatalf("the TS connection ended: %v", closed)
	}
	changes, switchAt := 0, -1
	for i := 1; i < len(marks); i++ {
		if marks[i] != marks[i-1] {
			changes++
			switchAt = i
		}
	}
	if changes != 1 || marks[0] != 0xb1 || marks[len(marks)-1] != 0xa1 {
		t.Fatalf("slot changes in the TS: %d (first %x last %x), want blue → amber once", changes, marks[0], marks[len(marks)-1])
	}
	for i := 1; i < len(pts); i++ {
		if d := pts[i] - pts[i-1]; d != alignedFrame {
			t.Fatalf("PTS step %d at frame %d (switch at %d): not continuous", d, i, switchAt)
		}
	}
	wall := channel.PTSToWall(pts[switchAt], time.Now())
	if d := wall.Sub(at); d < -spliceWallSlack || d > 2*time.Second {
		t.Fatalf("spliced at %v, %v from the instant %v", wall, d, at)
	}
	if ccBad != 0 {
		t.Fatalf("%d continuity-counter breaks", ccBad)
	}

	discs, firstAmber := 0, -1
	for i, e := range seen {
		if i > 0 && e.seq != seen[i-1].seq+1 {
			t.Fatalf("media sequence %d after %d", e.seq, seen[i-1].seq)
		}
		if e.disc && i > 0 {
			discs++
		}
		if firstAmber < 0 && strings.HasPrefix(e.uri, "s/amber/") {
			firstAmber = i
		}
	}
	if discs != 1 || firstAmber <= 0 || !seen[firstAmber].disc {
		t.Fatalf("stitched playlist: %d discontinuities, first amber segment %d: %+v", discs, firstAmber, seen)
	}
	last := seen[firstAmber-1]
	if gap := seen[firstAmber].pdt.Sub(last.pdt.Add(time.Duration(last.dur * float64(time.Second)))); gap != 0 {
		t.Fatalf("the first amber segment starts %v off the last blue one's end", gap)
	}
	if end := last.pdt.Add(time.Duration(last.dur * float64(time.Second))); end.After(at) {
		t.Fatalf("a blue segment ending after the instant was listed (%v > %v)", end, at)
	}
	// AC5: an old playlist's blue segment is still served while blue is previous
	resp, err := http.Get(g.srv.URL + "/stream/channel/" + g.ch.String() + "/" + last.uri)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(b), "blue:") {
		t.Fatalf("old segment %s: %q", last.uri, b)
	}
	t.Logf("TS spliced at %s (%v after the instant), %d frames, PTS continuous; HLS: one discontinuity at %s",
		wall.Format("15:04:05.000"), wall.Sub(at), len(pts), seen[firstAmber].pdt.Format("15:04:05.000"))
}

// The TS splice on a fast grid (an IDR every 200 ms) for the unit tier: one
// connection, blue up to the instant's IDR, amber from its IDR with the same
// PTS, frame-continuous timestamps.
func TestSwapSplicesTSFast(t *testing.T) {
	g := newSwapRig(t)
	for _, f := range []*alignedSlot{g.blue, g.amber} {
		f.mu.Lock()
		f.keyEvery = 6
		f.mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := g.openTS(ctx, t)
	time.Sleep(250 * time.Millisecond) // joined at blue's first IDR
	grid := 200 * time.Millisecond
	at := time.Now().Add(150 * time.Millisecond).Truncate(grid).Add(grid)
	g.slots.runOp(ctx, opRow{id: 9, kind: OpSwap, release: "rel-2", created: time.Now()}, "amber",
		[]switchRow{{ch: g.ch, at: at, state: switchPlanned}}, time.Now())
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, marks, _, _ := ts.snapshot()
		if len(marks) > 0 && marks[len(marks)-1] == 0xa1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	pts, marks, ccBad, closed := ts.snapshot()
	if closed != nil || len(marks) == 0 {
		t.Fatalf("TS: %d frames, closed %v", len(marks), closed)
	}
	switchAt := -1
	for i := 1; i < len(marks); i++ {
		if marks[i] != marks[i-1] {
			if switchAt >= 0 {
				t.Fatal("more than one change of slot")
			}
			switchAt = i
		}
	}
	if switchAt < 0 || marks[len(marks)-1] != 0xa1 {
		t.Fatalf("never spliced onto amber (%d frames)", len(marks))
	}
	for i := 1; i < len(pts); i++ {
		if d := pts[i] - pts[i-1]; d != alignedFrame {
			t.Fatalf("PTS step %d at frame %d (switch at %d)", d, i, switchAt)
		}
	}
	if wall := channel.PTSToWall(pts[switchAt], time.Now()); !wall.Equal(at) {
		t.Fatalf("spliced at %v, the instant is %v", wall, at)
	}
	if ccBad != 0 {
		t.Fatalf("%d continuity-counter breaks", ccBad)
	}
}

// Story 33.9 AC5 / AC5a: inside the grace a crash of the new live slot rolls
// back to the previous slot at once (never to a staged release); the channels
// read the previous slot again.
func TestAutoRollbackOnCrashInGrace(t *testing.T) {
	g := newSwapRig(t)
	ctx := context.Background()
	g.slots.runOp(ctx, opRow{id: 1, kind: OpSwap, release: "rel-2", created: time.Now()}, "amber",
		[]switchRow{{ch: g.ch, at: time.Now(), state: switchPlanned}}, time.Now())
	g.slots.advance(ctx, time.Now().Add(swapSettle+time.Second))
	if live, _ := g.slots.Live(); live != "amber" {
		t.Fatalf("live %s after the swap", live)
	}
	g.slots.probeAll(ctx)
	g.amber.crash()
	if !g.slots.Failover(ctx, "amber", "test") {
		t.Fatal("no automatic rollback")
	}
	if r := g.slots.Route(g.ch); r != "blue" {
		t.Fatalf("after the crash the channel reads %s (want the previous slot)", r)
	}
	g.slots.advance(ctx, time.Now().Add(swapSettle+time.Second))
	if live, _ := g.slots.Live(); live != "blue" {
		t.Fatalf("live %s after the rollback", live)
	}
	if prev, _ := g.slots.Previous(); prev != "" {
		t.Fatalf("a previous slot %q after the rollback", prev)
	}
	if _, standby, _, _ := g.slots.Roles(); standby != "green" {
		t.Fatalf("standby %q after the rollback (want green, restarted on the live release)", standby)
	}
}

// TC4 of 33.9: outside the grace a crash of the live slot fails over to the hot
// standby (the live release), not to a rollback.
func TestNoAutoRollbackAfterGrace(t *testing.T) {
	g := newSwapRig(t)
	ctx := context.Background()
	g.slots.runOp(ctx, opRow{id: 1, kind: OpSwap, release: "rel-2", created: time.Now()}, "amber",
		[]switchRow{{ch: g.ch, at: time.Now(), state: switchPlanned}}, time.Now())
	g.slots.advance(ctx, time.Now().Add(swapSettle+time.Second))
	g.slots.mu.Lock()
	g.slots.previousUntil = time.Now().Add(-time.Second)
	g.slots.mu.Unlock()
	g.slots.graceCheck(ctx, time.Now())
	if prev, _ := g.slots.Previous(); prev != "" {
		t.Fatalf("previous %q after the grace", prev)
	}
	if !g.blue.isDrained() {
		t.Fatal("the previous slot was not drained at the end of the grace")
	}
	if _, standby, _, _ := g.slots.Roles(); standby != "green" {
		t.Fatalf("standby %q after the grace", standby)
	}
	g.slots.probeAll(ctx)
	g.amber.crash()
	g.slots.Failover(ctx, "amber", "test")
	if live, _ := g.slots.Live(); live != "green" {
		t.Fatalf("after the grace a crash goes to %s (want the standby)", live)
	}
}

// Decision 11 / story 33.1 AC10: a crash of the live slot while a change is
// staged (an unapproved release on the staging slot) fails over to the hot
// standby running the live release — never to staging.
func TestCrashWithStagedChangeGoesToStandby(t *testing.T) {
	g := newSwapRig(t)
	ctx := context.Background()
	if _, _, staging, _ := g.slots.Roles(); staging != "amber" {
		t.Fatalf("staging %q", staging)
	}
	g.blue.crash()
	g.slots.Failover(ctx, "blue", "test")
	live, standby, staging, _ := g.slots.Roles()
	if live != "green" || staging != "amber" || standby == "amber" {
		t.Fatalf("after the crash: live %s standby %s staging %s", live, standby, staging)
	}
	if r := g.slots.Route(g.ch); r != "green" {
		t.Fatalf("the channel reads %s", r)
	}
}

// Story 33.7 EC2: the target down at a channel's instant — tried once more,
// then skipped (the channel stays on the old live slot).
func TestSwitchSkippedWhenTargetDown(t *testing.T) {
	g := newSwapRig(t)
	ctx := context.Background()
	now := time.Now()
	g.slots.runOp(ctx, opRow{id: 3, kind: OpSwap, release: "rel-2", created: now}, "amber",
		[]switchRow{{ch: g.ch, at: now, state: switchPlanned}}, now)
	g.slots.mu.Lock()
	g.slots.health["amber"].ok = false
	g.slots.mu.Unlock()
	g.slots.advance(ctx, now.Add(time.Millisecond))
	if r := g.slots.Route(g.ch); r != "blue" {
		t.Fatalf("retrying: the channel reads %s", r)
	}
	g.slots.advance(ctx, now.Add(switchRetry+time.Second))
	if _, ok := g.slots.Plan(g.ch); ok {
		t.Fatal("a skipped channel still has a plan")
	}
	if r := g.slots.Route(g.ch); r != "blue" {
		t.Fatalf("skipped: the channel reads %s", r)
	}
}

// TC5: a rolling swap keeps the target's extra encodes within its budget: the
// watched channels switch budget at a time, rollingLead apart; with room for
// all, nothing moves.
func TestRollingSwapBudget(t *testing.T) {
	now := time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)
	op := &swapOp{at: map[uuid.UUID]time.Time{}, state: map[uuid.UUID]string{}, removed: map[uuid.UUID]bool{}}
	watched := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	for i := 0; i < 5; i++ {
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
		ids = append(ids, id)
		op.at[id], op.state[id] = now, switchPlanned
		watched[id] = true
	}
	if planRolling(op, watched, 8, "auto", now) {
		t.Fatal("rolling with room for every channel")
	}
	if !planRolling(op, watched, 2, "auto", now) {
		t.Fatal("no rolling swap with a budget of 2 for 5 channels")
	}
	per := map[time.Time]int{}
	for _, id := range ids {
		per[op.at[id]]++
	}
	if len(per) != 3 {
		t.Fatalf("instants %v: want 3 rounds of at most 2", per)
	}
	for at, n := range per {
		if n > 2 || at.Before(now.Add(rollingLead)) {
			t.Fatalf("round at %v has %d channels", at, n)
		}
	}
	// in a rolling swap the target warms only the channels near their instant
	g := &Slots{live: "blue", op: op, health: map[string]*slotHealth{}}
	op.To, op.From, op.rolling = "amber", "blue", true
	_, _, warm, _, _ := g.opWarm(ids, nil, now.Add(rollingLead))
	if len(warm) != 2 {
		t.Fatalf("the target warms %d channels at the first round (want 2)", len(warm))
	}
}

// TC1 of 33.7 (stitcher): a planned switch lists the current source up to the
// instant, then the target from the segment that ends the last one listed,
// after one discontinuity; a removed channel's playlist ends.
func TestStitcherPlannedSwitch(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)
	mk := func(first int64, n int, start time.Time) upPlaylist {
		pl, _ := parseMediaPlaylist(slotPlaylist(first, n, start, 2, false))
		return pl
	}
	st := &Stitcher{}
	st.Update("blue", mk(100, 5, t0)) // 20:00:00 .. 20:00:10
	cut := t0.Add(13 * time.Second)   // inside the segment 20:00:12-14
	st.Plan("amber", cut)
	st.Update("blue", mk(102, 5, t0.Add(4*time.Second))) // to 20:00:14
	if !st.Reached() {
		t.Fatal("the instant is in blue's playlist: reached")
	}
	st.Update("amber", mk(900, 5, t0.Add(6*time.Second)))
	out := parseStitched(string(st.Render()))
	discs := 0
	for i, e := range out {
		if e.disc {
			discs++
			if !strings.HasPrefix(e.uri, "s/amber/") || !e.pdt.Equal(t0.Add(12*time.Second)) ||
				!strings.HasPrefix(out[i-1].uri, "s/blue/") {
				t.Fatalf("the discontinuity is at %+v", e)
			}
		}
		if i > 0 && e.seq != out[i-1].seq+1 {
			t.Fatalf("sequence %d after %d", e.seq, out[i-1].seq)
		}
	}
	if discs != 1 || st.Source() != "amber" || st.Reached() {
		t.Fatalf("%d discontinuities, source %s:\n%s", discs, st.Source(), st.Render())
	}
	st.End()
	if !strings.HasSuffix(string(st.Render()), "#EXT-X-ENDLIST\n") {
		t.Fatal("no ENDLIST for a removed channel")
	}
}

// A second swap inside the first one's grace: the first one's previous slot is
// no rollback target any more and is drained (it would run on, role-less).
func TestSecondSwapInGraceDrainsTheOldPrevious(t *testing.T) {
	g := newSwapRig(t)
	ctx := context.Background()
	now := time.Now()
	g.slots.runOp(ctx, opRow{id: 1, kind: OpSwap, release: "rel-2", created: now}, "amber",
		[]switchRow{{ch: g.ch, at: now, state: switchPlanned}}, now)
	g.slots.advance(ctx, now.Add(swapSettle+time.Second))
	if prev, _ := g.slots.Previous(); prev != "blue" || g.blue.isDrained() {
		t.Fatalf("after the first swap: previous %q, blue drained %v", prev, g.blue.isDrained())
	}
	now = time.Now()
	g.slots.runOp(ctx, opRow{id: 2, kind: OpSwap, release: "rel-3", created: now}, "green",
		[]switchRow{{ch: g.ch, at: now, state: switchPlanned}}, now)
	g.slots.advance(ctx, now.Add(swapSettle+time.Second))
	if live, _ := g.slots.Live(); live != "green" {
		t.Fatalf("live %s after the second swap", live)
	}
	if prev, _ := g.slots.Previous(); prev != "amber" {
		t.Fatalf("previous %q after the second swap", prev)
	}
	if !g.blue.isDrained() {
		t.Fatal("the first swap's previous slot was left running")
	}
	if g.amber.isDrained() {
		t.Fatal("the new previous slot was drained")
	}
}

// A swap cancelled by a rollback (or failed) leaves its target running the
// release that is not going on the air: it is drained; the channels read the
// old live slot.
func TestUnfinishedSwapDrainsItsTarget(t *testing.T) {
	g := newSwapRig(t)
	ctx := context.Background()
	now := time.Now()
	g.slots.runOp(ctx, opRow{id: 4, kind: OpSwap, release: "rel-2", created: now}, "amber",
		[]switchRow{{ch: g.ch, at: now.Add(time.Hour), state: switchPlanned}}, now)
	g.slots.mu.Lock()
	op := g.slots.op
	op.at[g.ch] = now.Add(time.Hour) // a watched channel waiting for its boundary
	g.slots.mu.Unlock()
	g.slots.endOp(ctx, op, "cancelled", map[string]any{"by_rollback": 5})
	if !g.amber.isDrained() {
		t.Fatal("the cancelled swap's target was left running")
	}
	if r := g.slots.Route(g.ch); r != "blue" {
		t.Fatalf("the channel reads %s after the cancelled swap", r)
	}
	if g.blue.isDrained() {
		t.Fatal("the live slot was drained")
	}
}

// A swap operation found again when the live slot already runs its release
// (the front door restarted after it finished, or its 'done' write was lost)
// ends done on the live slot — never failed, which would roll a good swap back.
// A rollback without a release never targets "any healthy slot".
func TestSwapTargetWhenLiveRunsTheRelease(t *testing.T) {
	g := newSwapRig(t)
	ctx := context.Background()
	if to, ok := g.slots.targetFor("rel-2", OpSwap); !ok || to != "amber" {
		t.Fatalf("before the swap the target is %q %v (want the staging slot)", to, ok)
	}
	now := time.Now()
	g.slots.runOp(ctx, opRow{id: 1, kind: OpSwap, release: "rel-2", created: now}, "amber",
		[]switchRow{{ch: g.ch, at: now, state: switchPlanned}}, now)
	g.slots.advance(ctx, now.Add(swapSettle+time.Second))
	if to, ok := g.slots.targetFor("rel-2", OpSwap); !ok || to != "amber" {
		t.Fatalf("after the swap the target is %q %v (want the live slot: nothing to switch)", to, ok)
	}
	if to, ok := g.slots.targetFor("", OpRollback); ok {
		t.Fatalf("a rollback with no release targets %q", to)
	}
}

// A planned switch whose operation ended unfinished (or whose channel was
// skipped) must not hold the playlist at its instant.
func TestStitcherClearedPlanGrowsAgain(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)
	mk := func(first int64, n int, start time.Time) upPlaylist {
		pl, _ := parseMediaPlaylist(slotPlaylist(first, n, start, 2, false))
		return pl
	}
	st := &Stitcher{}
	st.Update("blue", mk(100, 5, t0))
	st.Plan("amber", t0.Add(11*time.Second))
	st.Update("blue", mk(102, 5, t0.Add(4*time.Second))) // to 20:00:14: reached at 12
	n := len(parseStitched(string(st.Render())))
	st.ClearPlan()
	st.Update("blue", mk(104, 5, t0.Add(8*time.Second))) // to 20:00:18
	out := parseStitched(string(st.Render()))
	if last := out[len(out)-1]; !last.pdt.Equal(t0.Add(16*time.Second)) || st.Reached() {
		t.Fatalf("after the plan was cleared the playlist ends at %v (had %d entries)", last.pdt, n)
	}
}
