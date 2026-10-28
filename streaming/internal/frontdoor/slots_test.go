package frontdoor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
)

// --- a fake TV slot --------------------------------------------------------

func tsPacket(pid uint16, pusi bool, cc byte, payload []byte, rai bool) []byte {
	p := make([]byte, 188)
	p[0] = 0x47
	p[1] = byte(pid>>8) & 0x1f
	if pusi {
		p[1] |= 0x40
	}
	p[2] = byte(pid)
	off := 4
	if rai {
		p[3] = 0x30 | cc&0x0f // adaptation + payload
		p[4] = 1
		p[5] = 0x40 // random access
		off = 6
	} else {
		p[3] = 0x10 | cc&0x0f
	}
	n := copy(p[off:], payload)
	for i := off + n; i < 188; i++ {
		p[i] = 0xff
	}
	return p
}

func psi(tableID byte, body []byte) []byte {
	sec := []byte{tableID, 0xb0, 0}
	sec = append(sec, body...)
	l := len(sec) - 3 + 4 // + CRC
	sec[1] = 0xb0 | byte(l>>8)&0x0f
	sec[2] = byte(l)
	sec = append(sec, 0, 0, 0, 0) // CRC (not checked by the retimer)
	return append([]byte{0}, sec...)
}

func patPMT(cc byte) []byte {
	pat := psi(0x00, []byte{0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0x00})      // tsid 1; program 1 -> PMT 0x1000
	pmt := psi(0x02, []byte{0, 1, 0xc1, 0, 0, 0xe1, 0x00, 0xf0, 0x00, // program 1, PCR PID 0x100
		0x1b, 0xe1, 0x00, 0xf0, 0x00, // H.264 on 0x100
		0x0f, 0xe1, 0x01, 0xf0, 0x00}) // AAC on 0x101
	return append(tsPacket(0, true, cc, pat, false), tsPacket(0x1000, true, cc, pmt, false)...)
}

func ptsBytes(v int64) []byte {
	return []byte{0x21 | byte(v>>29)&0x0e, byte(v >> 22), byte(v>>14) | 1, byte(v >> 7), byte(v<<1) | 1}
}

func videoFrame(pts int64, key bool, cc byte) []byte {
	pes := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5}
	pes = append(pes, ptsBytes(pts)...)
	nal := byte(0x41)
	if key {
		nal = 0x65
	}
	pes = append(pes, 0, 0, 0, 1, nal, 0x88)
	return tsPacket(0x100, true, cc, pes, key)
}

func readPTS(p []byte) (int64, bool) {
	if len(p) != 188 || p[0] != 0x47 || p[1]&0x40 == 0 || (uint16(p[1]&0x1f)<<8|uint16(p[2])) != 0x100 {
		return 0, false
	}
	off := 4
	if p[3]&0x20 != 0 {
		off += 1 + int(p[4])
	}
	if p[off] != 0 || p[off+1] != 0 || p[off+2] != 1 {
		return 0, false
	}
	q := p[off+9:]
	return int64(q[0]>>1&0x07)<<30 | int64(q[1])<<22 | int64(q[2]>>1)<<15 | int64(q[3])<<7 | int64(q[4]>>1), true
}

type fakeSlot struct {
	name     string
	first    int64
	ahead    time.Duration // the playlist runs this far ahead (a slot further along)
	srv      *httptest.Server
	mu       sync.Mutex
	role     string
	epoch    int64
	stations []string
	warm     []slot.WarmRequest
	segs     atomic.Int32
	pid      int                // the process id the status reports (0: none)
	used     int                // the encoder sessions the status reports
	refuse   bool               // its warm route answers 503 (a slot restarting)
	chans    []slot.StationInfo // what its stations air (output position, block)
}

func newFakeSlot(t *testing.T, name string, first int64, stations ...string) *fakeSlot {
	f := &fakeSlot{name: name, first: first, stations: stations, role: slot.RoleIdle}
	r := chi.NewRouter()
	r.Get("/internal/tv/slot/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, f.status())
	})
	r.Post("/internal/tv/slot/warm", func(w http.ResponseWriter, r *http.Request) {
		var req slot.WarmRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		if f.refuse {
			f.mu.Unlock()
			http.Error(w, "restarting", http.StatusServiceUnavailable)
			return
		}
		f.warm = append(f.warm, req)
		f.role, f.epoch = req.Role, req.Epoch
		f.mu.Unlock()
		writeJSON(w, 200, f.status())
	})
	r.Get("/stream/channel/{id}/index.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		// 2 s segments on the wall-clock grid, numbered from this slot's first
		f.mu.Lock()
		ahead := f.ahead
		f.mu.Unlock()
		now := time.Now().Add(ahead)
		end := now.Truncate(2 * time.Second)
		start := end.Add(-10 * time.Second)
		n := (start.Unix() / 2) - 880000000
		_, _ = w.Write(slotPlaylist(f.first+n, 5, start, 2, false))
	})
	r.Get("/stream/channel/{id}/{seg}", func(w http.ResponseWriter, r *http.Request) {
		seg := chi.URLParam(r, "seg")
		if seg != "live.ts" {
			f.segs.Add(1)
			_, _ = io.WriteString(w, f.name+":"+seg)
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		var cc byte
		// a fast timeline (a frame every 10 ms of wall clock, 3000 ticks apart,
		// an IDR every 5 frames on the tick grid) keeps the test short
		next := time.Now()
		base := next.UnixNano() / int64(10*time.Millisecond)
		for i := 0; ; i++ {
			n := base + int64(i)
			pts := (n * 3000) % (1 << 33)
			key := n%5 == 0
			var b []byte
			if i%10 == 0 || key {
				b = append(b, patPMT(cc)...)
			}
			b = append(b, videoFrame(pts, key, cc)...)
			cc++
			if _, err := w.Write(b); err != nil {
				return
			}
			fl.Flush()
			next = next.Add(10 * time.Millisecond)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Until(next)):
			}
		}
	})
	f.srv = httptest.NewServer(r)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSlot) status() slot.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slot.Status{Slot: f.name, Role: f.role, Epoch: f.epoch, PID: f.pid, Stations: append([]string{}, f.stations...),
		EncodersUsed: f.used, Channels: append([]slot.StationInfo(nil), f.chans...)}
}

func (f *fakeSlot) addr() string { return strings.TrimPrefix(f.srv.URL, "http://") }

// crash ends the fake slot like a killed process: every connection drops.
func (f *fakeSlot) crash() {
	f.srv.CloseClientConnections()
	f.srv.Close()
}

type slotRig struct {
	fd          *Handler
	slots       *Slots
	srv         *httptest.Server
	blue, green *fakeSlot
	ch          uuid.UUID
}

func newSlotRig(t *testing.T) *slotRig {
	ch := uuid.New()
	g := &slotRig{ch: ch}
	g.blue = newFakeSlot(t, "blue", 1000, ch.String())
	g.green = newFakeSlot(t, "green", 7000, ch.String())
	g.green.ahead = 2 * time.Second // green already has the segment after blue's last
	store := placement.NewMemStore()
	store.SetHosts([]placement.Host{{ID: "home", Name: "mac", Role: placement.RoleHome, Enabled: true, Capacity: 8,
		Weight: 1, CanLive: true}})
	s := placement.DefaultSettings()
	hosts := &placement.Hosts{Source: store, Settings: settings{s}}
	hosts.Refresh(context.Background())
	svc := &placement.Service{Store: store, Hosts: hosts, Settings: settings{s}, Self: "home"}
	tracker := NewTracker()
	g.slots = &Slots{Host: "home", Addrs: map[string]string{"blue": g.blue.addr(), "green": g.green.addr()},
		Demand: tracker.Demands, ProbeEvery: 50 * time.Millisecond,
		Budget: func(context.Context) (int, int, int) { return 8, 0, 0 }}
	if err := g.slots.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.fd = &Handler{Svc: svc, Settings: settings{s}, Self: "home", Tracker: tracker, Slots: g.slots,
		SpliceRetry: 20 * time.Millisecond}
	r := chi.NewRouter()
	g.fd.Mount(r)
	g.srv = httptest.NewServer(r)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *slotRig) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(g.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// SyncNow: Run sends the slots their warm sets and budgets at once (VOD's gate
// lowers the NVENC budget by the session it is taking before it takes it) and
// their reports come back; without Run nothing serves it.
func TestSyncNowSendsTheBudget(t *testing.T) {
	g := newSlotRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if g.slots.SyncNow(ctx, 50*time.Millisecond) {
		t.Fatal("SyncNow served without Run")
	}
	var nv atomic.Int64
	nv.Store(6)
	g.slots.Budget = func(context.Context) (int, int, int) { return 8, int(nv.Load()), 0 }
	for _, f := range []*fakeSlot{g.blue, g.green} {
		f.mu.Lock()
		f.used = 2
		f.mu.Unlock()
	}
	go g.slots.Run(ctx)
	nv.Store(4)
	if !g.slots.SyncNow(ctx, 3*time.Second) {
		t.Fatal("SyncNow not served")
	}
	var live *slot.WarmRequest
	for _, f := range []*fakeSlot{g.blue, g.green} {
		f.mu.Lock()
		if n := len(f.warm); n > 0 && f.warm[n-1].Role == slot.RoleLive {
			w := f.warm[n-1]
			live = &w
		}
		f.mu.Unlock()
	}
	if live == nil || live.Budget.NVENCMax != 4 {
		t.Fatalf("the live slot's last warm: %+v", live)
	}
	if used := g.slots.EncodersUsed(); used < 2 {
		t.Errorf("EncodersUsed %d after the sync, want the slots' reports (2 each)", used)
	}

	// the standby down (restarting): it is cold, the live slot's budget is what
	// VOD counts on
	liveName, _ := g.slots.Live()
	liveSlot, standby := g.blue, g.green
	if liveName == "green" {
		liveSlot, standby = g.green, g.blue
	}
	standby.mu.Lock()
	standby.refuse = true
	standby.mu.Unlock()
	if !g.slots.SyncNow(ctx, 3*time.Second) {
		t.Error("SyncNow failed although the live slot took its set (the standby refused)")
	}
	// the live slot does not take its set: the sync is no success, so VOD takes
	// no session on stale reports
	liveSlot.mu.Lock()
	liveSlot.refuse = true
	liveSlot.mu.Unlock()
	if g.slots.SyncNow(ctx, 3*time.Second) {
		t.Error("SyncNow succeeded although the live slot refused its set")
	}
}

// ChannelsNeed: two sessions for each channel on the live slot, and with the hot
// standby on two for each it keeps warm (a channel with viewers), and as much
// again for one more channel while the host has room for one: what VOD must
// leave them.
func TestChannelsNeed(t *testing.T) {
	g := newSlotRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var watched atomic.Bool
	g.slots.Demand = func() []ChannelDemand {
		if watched.Load() {
			return []ChannelDemand{{Channel: g.ch, Humans: 1}}
		}
		return nil
	}
	go g.slots.Run(ctx)
	if !waitFor(3*time.Second, func() bool { nv, _ := g.slots.ChannelsNeed(ctx, 8, 6, 0); return nv == 6 }) {
		nv, _ := g.slots.ChannelsNeed(ctx, 8, 6, 0)
		t.Fatalf("one channel on air, nobody watching: need %d, want 6 (2, and 4 for one more live and warm)", nv)
	}
	if nv, _ := g.slots.ChannelsNeed(ctx, 1, 6, 0); nv != 2 {
		t.Errorf("one on air on a host for one: need %d, want 2 (no room for another)", nv)
	}
	watched.Store(true)
	if nv, vt := g.slots.ChannelsNeed(ctx, 8, 6, 0); nv != 8 || vt != 0 {
		t.Errorf("watched, so warm on the standby too: need %d, %d; want 8, 0", nv, vt)
	}
	if nv, _ := g.slots.ChannelsNeed(ctx, 8, 0, 0); nv != 0 {
		t.Errorf("no NVENC cap: need %d", nv)
	}
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// TC3: the routing decides which slot serves a channel placed on this host:
// the playlist (stitched, segment URIs per slot), its segments, and after a
// switch the new slot — the old slot still serves what an old playlist names.
func TestFrontDoorRoutesToLiveSlot(t *testing.T) {
	g := newSlotRig(t)
	if live, epoch := g.slots.Live(); live != "blue" || epoch != 1 {
		t.Fatalf("bootstrap: live %s epoch %d", live, epoch)
	}
	pl := "/stream/channel/" + g.ch.String() + "/index.m3u8"
	code, body := g.get(t, pl)
	if code != 200 || !strings.Contains(body, "s/blue/seg-") || strings.Contains(body, "green") {
		t.Fatalf("playlist from blue: %d\n%s", code, body)
	}
	uri := ""
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "s/blue/") {
			uri = l
		}
	}
	if code, seg := g.get(t, "/stream/channel/"+g.ch.String()+"/"+uri); code != 200 || !strings.HasPrefix(seg, "blue:seg-") {
		t.Fatalf("segment %s: %d %q", uri, code, seg)
	}
	// the swap API (story 33.7 calls it): green under epoch 2
	if err := g.slots.Switch(context.Background(), "green", 1); err == nil {
		t.Fatal("a stale epoch was accepted")
	}
	if err := g.slots.Switch(context.Background(), "green", 2); err != nil {
		t.Fatal(err)
	}
	_, body = g.get(t, pl)
	if !strings.Contains(body, "#EXT-X-DISCONTINUITY\n") || !strings.Contains(body, "s/green/seg-") {
		t.Fatalf("after the switch:\n%s", body)
	}
	// an old playlist's blue segment is still blue's
	if code, seg := g.get(t, "/stream/channel/"+g.ch.String()+"/"+uri); code != 200 || !strings.HasPrefix(seg, "blue:") {
		t.Fatalf("old segment after the switch: %d %q", code, seg)
	}
	if _, standby, _, _ := g.slots.Roles(); standby != "blue" {
		t.Fatalf("the old live slot is the standby now, got %q", standby)
	}
	// the slots heard their roles
	g.slots.SyncWarm(context.Background())
	if g.green.status().Role != slot.RoleLive || g.blue.status().Role != slot.RoleStandby || g.green.status().Epoch != 2 {
		t.Fatalf("roles told: green %+v blue %+v", g.green.status(), g.blue.status())
	}
}

// TC7 (unit): killing the live slot fails every channel over to the standby
// within a probe or two: the HLS playlist continues with one discontinuity and
// no media-sequence reset; a TS client's connection is kept and its timestamps
// stay monotonic (the retiming splicer).
func TestFailoverToHotStandby(t *testing.T) {
	g := newSlotRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var switched atomic.Int64
	g.slots.OnSwitch = func(_, _ string, epoch int64) { switched.Store(epoch) }
	go g.slots.Run(ctx)
	pl := "/stream/channel/" + g.ch.String() + "/index.m3u8"
	_, before := g.get(t, pl)

	// a TS client through the front door
	resp, err := http.Get(g.srv.URL + "/stream/channel/" + g.ch.String() + "/live.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var mu sync.Mutex
	var pts []int64
	var last time.Time
	go func() {
		r := bufio.NewReader(resp.Body)
		buf := make([]byte, 188)
		for {
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			if v, ok := readPTS(buf); ok {
				mu.Lock()
				pts = append(pts, v)
				last = time.Now()
				mu.Unlock()
			}
		}
	}()
	time.Sleep(200 * time.Millisecond) // past a keyframe: the client is joined
	mu.Lock()
	n0 := len(pts)
	mu.Unlock()
	if n0 == 0 {
		t.Fatal("the TS client got no frames from blue")
	}

	killed := time.Now()
	g.blue.crash()
	deadline := time.Now().Add(5 * time.Second)
	for switched.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	took := time.Since(killed)
	if live, epoch := g.slots.Live(); live != "green" || epoch != 2 || took > 2*time.Second {
		t.Fatalf("after the kill: live %s epoch %d after %v", live, epoch, took)
	}
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	n1, gotLast := len(pts), last
	var back int
	for i := 1; i < len(pts); i++ {
		if pts[i] <= pts[i-1] {
			back++
		}
	}
	mu.Unlock()
	if n1-n0 < 15 || time.Since(gotLast) > 300*time.Millisecond || back > 0 {
		t.Fatalf("TS after the failover: %d new frames, last %v ago, %d timestamps not increasing", n1-n0, time.Since(gotLast), back)
	}
	_, after := g.get(t, pl)
	seq := func(b string) int64 {
		var v int64
		for _, l := range strings.Split(b, "\n") {
			if strings.HasPrefix(l, "#EXT-X-MEDIA-SEQUENCE:") {
				_, _ = fmt.Sscanf(l, "#EXT-X-MEDIA-SEQUENCE:%d", &v)
			}
		}
		return v
	}
	if strings.Count(after, "#EXT-X-DISCONTINUITY\n") != 1 || !strings.Contains(after, "s/green/") || seq(after) < seq(before) {
		t.Fatalf("playlist after the failover (before %d):\n%s", seq(before), after)
	}
	t.Logf("failover after %v; TS client: %d frames, monotonic; playlist media sequence %d → %d",
		took.Round(time.Millisecond), n1, seq(before), seq(after))
}

// No failover without a healthy standby (a crash then waits for the restart).
func TestFailoverNeedsAHealthyStandby(t *testing.T) {
	g := newSlotRig(t)
	g.green.crash()
	g.slots.probeAll(context.Background())
	if g.slots.Failover(context.Background(), "blue", "test") {
		t.Fatal("failed over to a dead standby")
	}
	if live, _ := g.slots.Live(); live != "blue" {
		t.Fatalf("live %s", live)
	}
	// staging is off in this build (and never a failover target)
	if _, err := g.slots.StartStaging(context.Background()); err == nil && !StagingBuild {
		t.Fatal("staging started without the build flag")
	}
}

// slotEvents records the events a front door emits for the bell.
type slotEvents struct {
	mu  sync.Mutex
	got []SlotEvent
}

func (r *slotEvents) on(_ context.Context, e SlotEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, e)
}

func (r *slotEvents) all() []SlotEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]SlotEvent(nil), r.got...)
}

// Story 33.13 AC1: a failover to the hot standby is reported for the bell.
func TestFailoverEmitsEvent(t *testing.T) {
	g := newSlotRig(t)
	ev := &slotEvents{}
	g.slots.OnEvent = ev.on
	ctx := context.Background()
	g.slots.probeAll(ctx)
	if !g.slots.Failover(ctx, "blue", "test: killed") {
		t.Fatal("no failover")
	}
	got := ev.all()
	if len(got) != 1 || got[0].Kind != SlotEventFailover || got[0].Slot != "blue" || got[0].Detail["to"] != "green" ||
		got[0].Detail["why"] != "test: killed" {
		t.Fatalf("events %+v", got)
	}
	// a failover that does not happen (blue is not live any more) reports nothing
	if g.slots.Failover(ctx, "blue", "again") {
		t.Fatal("failed over from a slot that is not live")
	}
	if n := len(ev.all()); n != 1 {
		t.Fatalf("%d events", n)
	}
}

// Story 33.13 AC1: a staging slot restarted three times within the window is a
// crash loop, reported once; the live slot's restarts are not counted.
func TestStagingCrashLoopEvent(t *testing.T) {
	t.Run("restarts seen by the probes", func(t *testing.T) {
		g := newSlotRig(t)
		ev := &slotEvents{}
		g.slots.OnEvent = ev.on
		amber := newFakeSlot(t, "amber", 3000, g.ch.String())
		g.slots.mu.Lock()
		g.slots.Addrs["amber"] = amber.addr()
		g.slots.staging = "amber"
		g.slots.mu.Unlock()
		ctx := context.Background()
		restart := func(pid int) {
			amber.mu.Lock()
			amber.pid = pid
			amber.mu.Unlock()
			g.slots.check(ctx, "amber")
		}
		restart(100) // the first process: no restart yet
		restart(101)
		restart(102)
		if n := len(ev.all()); n != 0 {
			t.Fatalf("two restarts reported as a loop: %+v", ev.all())
		}
		restart(103)
		got := ev.all()
		if len(got) != 1 || got[0].Kind != SlotEventCrashLoop || got[0].Slot != "amber" || got[0].Detail["restarts"] != 3 {
			t.Fatalf("events %+v", got)
		}
		restart(104) // still looping: reported once
		restart(104) // the same process: no restart
		if n := len(ev.all()); n != 1 {
			t.Fatalf("%d events", n)
		}
		// the live slot restarting is no staging crash loop
		for pid := 1; pid <= 5; pid++ {
			g.blue.mu.Lock()
			g.blue.pid = pid
			g.blue.mu.Unlock()
			g.slots.check(ctx, "blue")
		}
		if n := len(ev.all()); n != 1 {
			t.Fatalf("live restarts counted: %+v", ev.all())
		}
	})
	t.Run("exits the supervisor reports", func(t *testing.T) {
		g := newSlotRig(t)
		ev := &slotEvents{}
		g.slots.OnEvent = ev.on
		g.slots.mu.Lock()
		g.slots.staging = "amber"
		g.slots.mu.Unlock()
		ctx := context.Background()
		for i := 0; i < 2; i++ {
			g.slots.Exited(ctx, "amber")
		}
		if n := len(ev.all()); n != 0 {
			t.Fatalf("two exits reported as a loop")
		}
		g.slots.Exited(ctx, "amber")
		g.slots.Exited(ctx, "amber")
		if got := ev.all(); len(got) != 1 || got[0].Kind != SlotEventCrashLoop {
			t.Fatalf("events %+v", got)
		}
	})
}

// TC8: cap 16, W = 22 watched channels: live gets 16 (the rest spill or are
// refused by placement), the standby what is left — viewers first, then pushes
// by remote viewers; with demand, also what live keeps warm.
func TestStandbyBudgetOrder(t *testing.T) {
	ids := make([]uuid.UUID, 22)
	for i := range ids {
		ids[i] = uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
	}
	var demands []ChannelDemand
	remote := map[uuid.UUID]int{}
	for i, id := range ids {
		d := ChannelDemand{Channel: id}
		switch {
		case i < 4:
			d.Humans = 4 - i // 4 channels with people watching
		default:
			d.Pushes = 1 // 18 always-on pushes
			remote[id] = i
		}
		demands = append(demands, d)
	}
	set := DefaultStagingSettings()
	// live airs 16 of them: no room left for the standby
	p := planWarm(set, demands, remote, ids[:16], 16, 0, 0)
	if len(p.live) != 16 || len(p.standby) != 0 || p.liveBudget.MaxActive != 16 {
		t.Fatalf("full host: live %d standby %d %+v", len(p.live), len(p.standby), p.liveBudget)
	}
	if p.live[0] != ids[0] || p.live[3] != ids[3] || p.live[4] != ids[21] {
		t.Fatalf("live order (viewers, then remote viewers): %v", p.live[:5])
	}
	// live airs 10 (the rest went to a node): 6 left, viewers first, then the
	// pushes with the most remote viewers
	onAir := append(append([]uuid.UUID{}, ids[:4]...), ids[16:]...)
	p = planWarm(set, demands, remote, onAir, 16, 0, 0)
	want := []uuid.UUID{ids[0], ids[1], ids[2], ids[3], ids[21], ids[20]}
	if fmt.Sprint(p.standby) != fmt.Sprint(want) || p.stBudget.MaxActive != 6 {
		t.Fatalf("standby %v budget %+v, want %v", p.standby, p.stBudget, want)
	}
	// NVENC: live's sessions first (two per channel), the standby gets what is left
	p = planWarm(set, demands, remote, onAir, 16, 24, 0)
	if len(p.standby) != 2 || p.stBudget.NVENCMax != 4 || p.liveBudget.NVENCMax != 24 {
		t.Fatalf("NVENC 24, live 10: standby %d %+v", len(p.standby), p.stBudget)
	}
	// hot standby off: nothing
	off := set
	off.HotStandby = false
	if p = planWarm(off, demands, remote, onAir, 16, 0, 0); len(p.standby) != 0 {
		t.Fatalf("hot standby off: %v", p.standby)
	}
	// demand: also a channel live keeps warm after its last viewer
	extra := uuid.MustParse("00000000-0000-4000-8000-999999999999")
	d := set
	d.StandbyWarm = StandbyWarmDemand
	if p = planWarm(d, demands[:2], nil, []uuid.UUID{ids[0], ids[1], extra}, 16, 0, 0); len(p.standby) != 3 {
		t.Fatalf("demand mode: %v", p.standby)
	}
	if p = planWarm(set, demands[:2], nil, []uuid.UUID{ids[0], ids[1], extra}, 16, 0, 0); len(p.standby) != 2 {
		t.Fatalf("viewers mode warmed a channel nobody watches: %v", p.standby)
	}
}

// The rules.tv_staging overlay; staging needs the build flag too.
func TestStagingSettings(t *testing.T) {
	d := DefaultStagingSettings()
	if !d.Enabled || !d.HotStandby || d.StandbyWarm != StandbyWarmViewers {
		t.Fatalf("defaults %+v", d)
	}
	got := ApplyStagingRules(d, []byte(`{"hot_standby":false,"standby_warm":"demand","enabled":true}`))
	if got.HotStandby || got.StandbyWarm != StandbyWarmDemand || !got.Enabled || got.StagingActive() != StagingBuild {
		t.Fatalf("overlay %+v", got)
	}
	if ApplyStagingRules(d, []byte(`{"standby_warm":"all"}`)).StandbyWarm != StandbyWarmViewers {
		t.Fatal("a bad value replaced the default")
	}
	if d.StagingActive() != StagingBuild {
		t.Fatal("staging active by default only in a staging build")
	}
}

// A crashed slot's listed segments come from its directory on this disk; the
// path is built only from the known parts.
func TestSlotSegmentFile(t *testing.T) {
	h := &Handler{CacheRoot: t.TempDir()}
	id := uuid.New()
	p, ok := h.slotSegmentFile("green", id, "seg-42.ts")
	if !ok || !strings.HasSuffix(filepathToSlash(p), "/tv/green/channel-"+id.String()+"/seg-42.ts") {
		t.Fatalf("path %q %v", p, ok)
	}
	for _, c := range [][2]string{{"purple", "seg-1.ts"}, {"green", "seg-..ts"}, {"green", "seg-1/../x.ts"}, {"green", "seg-x.ts"}} {
		if _, ok := h.slotSegmentFile(c[0], id, c[1]); ok {
			t.Errorf("%v accepted", c)
		}
	}
	if _, ok := (&Handler{}).slotSegmentFile("green", id, "seg-1.ts"); ok {
		t.Error("no cache root, but a path")
	}
}

func filepathToSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// The routing view, the staging slot through the supervisor (in a staging build),
// the supervisor's exit report and the standby switch with hot_standby.
func TestSlotsViewAndSupervisor(t *testing.T) {
	g := newSlotRig(t)
	var mu sync.Mutex
	var asked []string
	sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer sup.Close()
	g.slots.Supervisor = sup.URL
	on := DefaultStagingSettings()
	on.Enabled = true
	g.slots.Settings = func(context.Context) StagingSettings { return on }
	g.slots.probeAll(context.Background())
	g.slots.SyncWarm(context.Background())
	if h := g.slots.Healthy(); !h["blue"] || !h["green"] {
		t.Fatalf("healthy %v", h)
	}
	if len(g.slots.LiveStations()) != 1 {
		t.Fatalf("live stations %v", g.slots.LiveStations())
	}
	v := g.slots.View()
	if v["live"] != "blue" || v["standby"] != "green" || len(v["slots"].([]SlotView)) != 2 {
		t.Fatalf("view %v", v)
	}
	g.slots.Addrs["amber"] = newFakeSlot(t, "amber", 4000).addr() // the third instance
	name, err := g.slots.StartStaging(context.Background())
	if StagingBuild {
		if err != nil || name != "amber" {
			t.Fatalf("staging: %q %v", name, err)
		}
		_ = g.slots.StopStaging(context.Background())
	} else if err == nil {
		t.Fatal("staging without the build flag")
	}
	// hot standby off: the supervisor stops the standby instance
	on.HotStandby = false
	g.slots.SyncWarm(context.Background())
	mu.Lock()
	got := strings.Join(asked, ",")
	mu.Unlock()
	if !strings.Contains(got, "POST /slots/green/stop") {
		t.Fatalf("supervisor asked %q", got)
	}
	// the supervisor's report fails over at once
	on.HotStandby = true
	g.slots.SyncWarm(context.Background())
	g.blue.crash()
	g.slots.Exited(context.Background(), "blue")
	if live, _ := g.slots.Live(); live != "green" || g.slots.Switches() != 1 {
		t.Fatalf("after the exit report: %s", live)
	}
}
