package slot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
)

func TestNamesAndAddrs(t *testing.T) {
	if DefaultAddr(Blue) != "127.0.0.1:8791" || DefaultAddr(Amber) != "127.0.0.1:8793" ||
		DefaultAdminAddr(Green) != "127.0.0.1:9112" {
		t.Fatal("default addresses")
	}
	m := ParseAddrs("blue=127.0.0.1:1, green = 127.0.0.1:2 ,purple=x,amber=")
	if len(m) != 2 || m[Blue] != "127.0.0.1:1" || m[Green] != "127.0.0.1:2" {
		t.Fatalf("parse: %v", m)
	}
	if FormatAddrs(m) != "blue=127.0.0.1:1,green=127.0.0.1:2" {
		t.Fatalf("format: %s", FormatAddrs(m))
	}
	if (Layout{Root: "/c", Name: Green}).HLSDir("channel-x") != "/c/tv/green/channel-x" &&
		(Layout{Root: "/c", Name: Green}).HLSDir("channel-x") != `\c\tv\green\channel-x` {
		t.Fatal("layout")
	}
}

type fakeEngine struct {
	held    []uuid.UUID
	stopped []uuid.UUID
	active  []uuid.UUID
}

func (e *fakeEngine) Hold(_ context.Context, id uuid.UUID) error {
	e.held = append(e.held, id)
	return nil
}
func (e *fakeEngine) StopChannel(id uuid.UUID) bool { e.stopped = append(e.stopped, id); return true }
func (e *fakeEngine) ActiveIDs() []uuid.UUID        { return e.active }
func (e *fakeEngine) StopAll()                      {}

// TC2: a slot answers this machine only.
func TestSlotLoopbackOnly(t *testing.T) {
	srv := &Server{State: NewState(Blue, ""), HostID: "home", Engine: &fakeEngine{},
		Channels: func(r chi.Router) {
			r.Get("/stream/channel/{id}/index.m3u8", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("#EXTM3U")) })
		}}
	h := srv.Handler()
	for _, c := range []struct {
		remote string
		want   int
	}{{"127.0.0.1:5000", 200}, {"[::1]:5000", 200}, {"192.168.1.20:5000", 403}, {"10.0.0.2:1", 403}} {
		for _, path := range []string{"/internal/tv/slot/status", "/stream/channel/" + uuid.NewString() + "/index.m3u8"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = c.remote
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("%s %s: %d, want %d", c.remote, path, rec.Code, c.want)
			}
		}
	}
}

// The front door's warm call sets the role, the budget and the warm set; an
// exclusive set (the hot standby) stops what is not in it.
func TestSlotWarm(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	eng := &fakeEngine{active: []uuid.UUID{a, c}}
	st := NewState(Green, "")
	// the budget reaches the encoder gates at once (the front door lowers it by
	// the sessions VOD is taking)
	var applied []Budget
	srv := &Server{State: st, HostID: "home", Engine: eng,
		OnBudget: func(_ context.Context, b Budget) { applied = append(applied, b) }}
	h := srv.Handler()
	body, _ := json.Marshal(WarmRequest{Role: RoleStandby, Epoch: 3, Channels: []string{a.String(), b.String()},
		Budget: Budget{MaxActive: 2, NVENCMax: 4}, Exclusive: true})
	req := httptest.NewRequest(http.MethodPost, "/internal/tv/slot/warm", strings.NewReader(string(body)))
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("warm: %d %s", rec.Code, rec.Body.String())
	}
	var out Status
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if role, epoch := st.Role(); role != RoleStandby || epoch != 3 || out.Role != RoleStandby {
		t.Fatalf("role %s epoch %d", role, epoch)
	}
	if st.Budget() != (Budget{MaxActive: 2, NVENCMax: 4}) || len(st.Holds(time.Now())) != 2 {
		t.Fatalf("budget %+v holds %v", st.Budget(), st.Holds(time.Now()))
	}
	if len(applied) != 1 || applied[0] != (Budget{MaxActive: 2, NVENCMax: 4}) {
		t.Fatalf("OnBudget got %+v", applied)
	}
	if len(eng.stopped) != 1 || eng.stopped[0] != c {
		t.Fatalf("stopped %v, want only %v", eng.stopped, c)
	}
	// an older epoch does not take the role back
	if st.SetRole(RoleLive, 2) {
		t.Fatal("an older epoch changed the role")
	}
	// holds expire without the front door
	if len(st.Holds(time.Now().Add(HoldTTL+time.Second))) != 0 {
		t.Fatal("holds outlived their TTL")
	}
	if out.PMTPID != 0x1000 || out.VideoPID != 0x100 || out.AudioPID != 0x101 || out.ServiceID != 1 {
		t.Fatalf("status layout %+v", out)
	}
}

// TC4: a slot starts a station only for a channel placed on its host while its
// role allows it, never when bound to another release; only the live slot
// acknowledges and releases placements.
func TestSlotFenceTrackAndRelease(t *testing.T) {
	ctx := context.Background()
	store := placement.NewMemStore()
	store.SetHosts([]placement.Host{{ID: "home", Role: placement.RoleHome, Enabled: true, Capacity: 8, Weight: 1},
		{ID: "pc", Role: placement.RoleNode, Enabled: true, Capacity: 8, Weight: 1}})
	here, there := uuid.New(), uuid.New()
	gen := int64(0)
	place := func(ch uuid.UUID, host string) placement.Placement {
		gen++
		p := placement.Placement{Channel: ch, Host: host, Gen: gen, State: placement.StatePlacing,
			Reason: placement.ReasonFirstRequest, PlacedAt: time.Now()}
		store.Put(p)
		return p
	}
	ph := place(here, "home")
	place(there, "pc")
	st := NewState(Green, "r1")
	f := Fence{Store: store, Self: "home", State: st}
	if _, err := f.Allow(ctx, here); !IsNotRunnable(err) {
		t.Fatalf("an idle slot ran a station: %v", err)
	}
	st.SetRole(RoleStandby, 1)
	g, err := f.Allow(ctx, here)
	if err != nil || g.Gen != ph.Gen {
		t.Fatalf("standby, placed here: %+v %v", g, err)
	}
	if _, err := f.Allow(ctx, there); err == nil {
		t.Fatal("a slot ran a channel placed on another host")
	}
	// the standby neither acknowledges nor releases the placement
	f.Release(ctx, here, ph.Gen)
	if p, _, _ := store.Get(ctx, here); !p.Active() {
		t.Fatal("the standby released the live placement")
	}
	st.SetRole(RoleLive, 2)
	f.Ack(ctx, here, ph.Gen)
	if p, _, _ := store.Get(ctx, here); p.State != placement.StateLive {
		t.Fatalf("live ack: %+v", p)
	}
	if keep, _ := f.Check(ctx, here, ph.Gen); !keep {
		t.Fatal("live slot fenced off its own placement")
	}
	// bound to another release: no station, and running ones are fenced
	st.SetRelease("r2")
	if _, err := f.Allow(ctx, here); !IsNotRunnable(err) || !strings.Contains(err.Error(), "release_mismatch") {
		t.Fatalf("release mismatch: %v", err)
	}
	if keep, _ := f.Check(ctx, here, ph.Gen); keep {
		t.Fatal("a station kept running on a mismatched release")
	}
	st.SetRelease("r1")
	f.Release(ctx, here, ph.Gen)
	if p, _, _ := store.Get(ctx, here); p.Active() {
		t.Fatal("the live slot's release did not release")
	}
	st.Drain()
	if ok, why := st.MayRun(); ok || why != "draining" {
		t.Fatalf("draining: %v %s", ok, why)
	}
}

type readsOnly struct{ look channel.Look }

func (r readsOnly) ProgramsFrom(context.Context, uuid.UUID, time.Time, int) ([]channel.ProgramBlock, error) {
	return []channel.ProgramBlock{{Seq: 42}}, nil
}
func (r readsOnly) Look(context.Context, uuid.UUID) (channel.Look, error) { return r.look, nil }
func (r readsOnly) SetRuntime(context.Context, channel.Runtime) error     { return nil }
func (r readsOnly) ClearRuntime(context.Context, uuid.UUID) error         { return nil }

// A slot bound to a TV release reads its programmes, looks and settings from it.
func TestSlotReadsItsRelease(t *testing.T) {
	r := Repo{State: NewState(Blue, "r1"), Reads: readsOnly{look: channel.Look{Name: "from the release"}}}
	bs, err := r.ProgramsFrom(context.Background(), uuid.New(), time.Now(), 5)
	if err != nil || len(bs) != 1 || bs[0].Seq != 42 {
		t.Fatalf("programmes %v %v", bs, err)
	}
	if l, _ := r.Look(context.Background(), uuid.New()); l.Name != "from the release" {
		t.Fatalf("look %+v", l)
	}
	rs := ReleaseSettings{Raw: func(sec string) json.RawMessage {
		switch sec {
		case "tv_output":
			return json.RawMessage(`{"fps":25,"keyint_seconds":4}`)
		case "tv":
			return json.RawMessage(`{"horizon_days":3}`)
		}
		return json.RawMessage(`{}`)
	}}
	if o := rs.Output(context.Background()); o.FPS != 25 || o.KeyintSeconds != 4 {
		t.Fatalf("release output %+v", o)
	}
	if rs.TV(context.Background()).HorizonDays != 3 {
		t.Fatal("release tv rules")
	}
	// the budget applies over the release's settings
	st := NewState(Green, "r1")
	st.SetBudget(Budget{MaxActive: 3})
	if o := (Settings{Settings: rs, State: st}).Output(context.Background()); o.MaxActive != 3 || o.FPS != 25 {
		t.Fatalf("budget over release %+v", o)
	}
}
