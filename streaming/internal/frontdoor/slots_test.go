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
	srv      *httptest.Server
	mu       sync.Mutex
	role     string
	epoch    int64
	stations []string
	warm     []slot.WarmRequest
	segs     atomic.Int32
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
		f.warm = append(f.warm, req)
		f.role, f.epoch = req.Role, req.Epoch
		f.mu.Unlock()
		writeJSON(w, 200, f.status())
	})
	r.Get("/stream/channel/{id}/index.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		// 2 s segments on the wall-clock grid, numbered from this slot's first
		now := time.Now()
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
		frame := int64(3000)
		next := time.Now().Truncate(time.Second / 30)
		for i := 0; ; i++ {
			pts := (next.Unix()*90000 + int64(next.Nanosecond())*9/100000) % (1 << 33)
			key := (pts/frame)%60 == 0
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
			next = next.Add(time.Second / 30)
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
	return slot.Status{Slot: f.name, Role: f.role, Epoch: f.epoch, Stations: append([]string{}, f.stations...)}
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

// TC3: the routing decides which slot serves a channel placed on this host:
// the playlist (stitched, segment URIs per slot), its segments, and after a
// switch the new slot — the old slot still serves what an old playlist names.
func TestFrontDoorRoutesToLiveSlot(t *testing.T) {
	if testing.Short() {
		t.Skip("seconds of wall-clock segments and TS; runs without -short")
	}
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
	time.Sleep(2100 * time.Millisecond) // a new 2 s segment on the grid
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
	if testing.Short() {
		t.Skip("seconds of wall-clock segments and TS; runs without -short")
	}
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
	time.Sleep(2500 * time.Millisecond) // past a keyframe: the client is joined
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
	time.Sleep(3 * time.Second)
	mu.Lock()
	n1, gotLast := len(pts), last
	var back int
	for i := 1; i < len(pts); i++ {
		if pts[i] <= pts[i-1] {
			back++
		}
	}
	mu.Unlock()
	if n1-n0 < 30 || time.Since(gotLast) > time.Second || back > 0 {
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
	got := ApplyStagingRules(d, []byte(`{"hot_standby":false,"standby_warm":"demand","enabled":"yes"}`))
	if got.HotStandby || got.StandbyWarm != StandbyWarmDemand || !got.Enabled {
		t.Fatalf("overlay %+v", got)
	}
	if ApplyStagingRules(d, []byte(`{"standby_warm":"all"}`)).StandbyWarm != StandbyWarmViewers {
		t.Fatal("a bad value replaced the default")
	}
	if d.StagingActive() != StagingBuild {
		t.Fatal("staging active without the build flag")
	}
}
