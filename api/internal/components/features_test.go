package components

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// allEnabled is every switchable component enabled, livetv.inprocess off (it conflicts
// with livetv.slots) and cloud.relay off, as a Full home is.
func allEnabled() map[string]store.Desired {
	out := map[string]store.Desired{}
	for _, c := range components.Default().Components() {
		if !c.Switchable {
			continue
		}
		w := components.WantEnabled
		if c.ID == "livetv.inprocess" || c.ID == "cloud.relay" {
			w = components.WantInstalled
		}
		out[c.ID] = store.Desired{ID: c.ID, Want: w, Generation: 1}
	}
	return out
}

func setWant(d map[string]store.Desired, id, want string, hold bool) map[string]store.Desired {
	x := d[id]
	x.ID, x.Want = id, want
	x.Hold = nil
	if hold {
		x.Hold = &store.Hold{By: "maya", At: time.Now()}
	}
	d[id] = x
	return d
}

func resolve(d map[string]store.Desired) Resolved {
	return Resolve(components.Default(), d, nil, false)
}

// T01: live_tv needs livetv and one of the slot kinds.
func TestFeatureLiveTVNeedsOneSlotKind(t *testing.T) {
	d := setWant(setWant(allEnabled(), "livetv.slots", components.WantInstalled, false), "livetv.inprocess", components.WantInstalled, false)
	r := resolve(d)
	if r.Features["live_tv"] {
		t.Fatal("live_tv on with both slot kinds off")
	}
	if by := r.Off["live_tv"]; !slices.Equal(by.Components, []string{"livetv.slots", "livetv.inprocess"}) || by.Reason != ReasonSwitchedOff {
		t.Fatalf("live_tv off by %+v", by)
	}
	if !resolve(setWant(d, "livetv.inprocess", components.WantEnabled, false)).Features["live_tv"] {
		t.Fatal("live_tv off with livetv.inprocess on")
	}
	if !resolve(allEnabled()).Features["live_tv"] {
		t.Fatal("live_tv off with livetv.slots on")
	}
	if r := resolve(setWant(allEnabled(), "livetv", components.WantInstalled, false)); r.Features["live_tv"] || r.Off["live_tv"].Components[0] != "livetv" {
		t.Fatalf("livetv off: %+v", r.Off)
	}
}

// T02: a hold turns the feature off, with the reason held.
func TestFeatureHeldIsOff(t *testing.T) {
	r := resolve(setWant(allEnabled(), "transcription", components.WantEnabled, true))
	if r.Features["transcripts"] || r.Off["transcripts"].Reason != ReasonHeld {
		t.Fatalf("transcripts %v %+v", r.Features["transcripts"], r.Off["transcripts"])
	}
}

// T03: only the desired state is read: a crash loop's status leaves the feature on (there
// is no status input at all), and so does a component with no row.
func TestFeatureIgnoresStatusAndMissingRows(t *testing.T) {
	r := resolve(allEnabled())
	for _, f := range components.Default().Features {
		if f == "remote_access" {
			continue // cloud.relay is off in a Full home
		}
		if !r.Features[f] {
			t.Errorf("%s off in a Full home: %+v", f, r.Off[f])
		}
	}
	if r := resolve(nil); len(r.Off) != 0 {
		t.Fatalf("no rows: %+v", r.Off)
	}
	// chapters and recommendations have no component: always on
	off := map[string]store.Desired{}
	for id, d := range allEnabled() {
		d.Want = components.WantInstalled
		off[id] = d
	}
	r = resolve(off)
	if !r.Features["chapters"] || !r.Features["recommendations"] || r.Features["youtube"] {
		t.Fatalf("all off: %+v", r.Features)
	}
}

// A requirement that is off turns its dependents' features off (the runners' rule) (an asset
// only when absent: installed meets it), and a missing licence flag turns its component's
// feature off.
func TestFeatureDependencyAndLicence(t *testing.T) {
	if r := resolve(setWant(allEnabled(), "runtime.ml", components.WantInstalled, false)); !r.Features["transcripts"] {
		t.Fatalf("transcripts off with runtime.ml installed: %+v", r.Off["transcripts"])
	}
	r := resolve(setWant(allEnabled(), "runtime.ml", components.WantAbsent, false))
	if r.Features["transcripts"] || r.Off["transcripts"].Reason != ReasonDependencyOff {
		t.Fatalf("transcripts %+v", r.Off["transcripts"])
	}
	d := setWant(setWant(allEnabled(), "cloud.relay", components.WantEnabled, false), "cloud.link", components.WantEnabled, false)
	if !Resolve(components.Default(), d, func(string) bool { return true }, false).Features["remote_access"] {
		t.Fatal("remote_access off with the relay on and licensed")
	}
	r = Resolve(components.Default(), d, func(string) bool { return false }, false)
	if r.Features["remote_access"] || r.Off["remote_access"].Reason != ReasonLicenceMissing {
		t.Fatalf("unlicensed relay: %+v", r.Off["remote_access"])
	}
	// the kill switch: all on
	if r := Resolve(components.Default(), d, func(string) bool { return false }, true); len(r.Off) != 0 || len(r.Components) != 0 {
		t.Fatalf("all on: %+v", r)
	}
}

// AC1/AC2: the viewer's body has the features only; the admin's names the components
// and the reason. Both are valid JSON in the catalog's order.
func TestSnapshotBodies(t *testing.T) {
	c := NewCapabilities(CapabilitiesOptions{})
	c.Apply(setWant(allEnabled(), "search.semantic", components.WantInstalled, false), 1842)
	s := c.Snapshot()
	if s.ETag() != "c-1842-1" {
		t.Fatalf("etag %s", s.ETag())
	}
	var viewer, admin map[string]json.RawMessage
	if err := json.Unmarshal(s.Body(false), &viewer); err != nil {
		t.Fatal(err)
	}
	if _, ok := viewer["admin"]; ok || len(viewer) != 2 {
		t.Fatalf("viewer body %s", s.Body(false))
	}
	var feats map[string]string
	_ = json.Unmarshal(viewer["features"], &feats)
	// story 54.20: plugin_panels is the tenth, on while plugins.host is
	if feats["semantic_search"] != "off" || feats["live_tv"] != "on" || feats["plugin_panels"] != "on" || len(feats) != 10 {
		t.Fatalf("features %v", feats)
	}
	if err := json.Unmarshal(s.Body(true), &admin); err != nil {
		t.Fatal(err)
	}
	var adm struct {
		Manage bool                    `json:"manage_components"`
		Off    map[string]FeatureOffBy `json:"off"`
	}
	_ = json.Unmarshal(admin["admin"], &adm)
	if !adm.Manage || !slices.Equal(adm.Off["semantic_search"].Components, []string{"search.semantic"}) ||
		adm.Off["semantic_search"].Reason != ReasonSwitchedOff {
		t.Fatalf("admin body %s", s.Body(true))
	}
	if f, off := s.ComponentOff("search.semantic"); !off || f != "semantic_search" {
		t.Fatalf("ComponentOff %q %v", f, off)
	}
	if _, off := s.ComponentOff("livetv"); off {
		t.Fatal("livetv off")
	}
}

// The ETag moves with the revision and the licence; a change signals once; a licence
// that lapses changes the answer.
func TestCapabilitiesETagAndSignal(t *testing.T) {
	lic := true
	c := NewCapabilities(CapabilitiesOptions{})
	<-c.changed
	c.SetLicensed(func(string) bool { return lic })
	d := setWant(setWant(allEnabled(), "cloud.relay", components.WantEnabled, false), "cloud.link", components.WantEnabled, false)
	c.Apply(d, 7)
	<-c.changed
	e1 := c.Snapshot().ETag()
	c.Apply(d, 7)
	select {
	case <-c.changed:
		t.Fatal("same revision signalled")
	default:
	}
	lic = false
	c.Recheck()
	<-c.changed
	if e2 := c.Snapshot().ETag(); e2 == e1 || c.Snapshot().AnyOff() == false {
		t.Fatalf("licence lapse: %s -> %s", e1, e2)
	}
	if _, off := c.Snapshot().ComponentOff("cloud.relay"); !off {
		t.Fatal("cloud.relay on without its licence")
	}
}

// Many changes inside the coalescing window are one push, with the last ETag.
func TestCapabilitiesCoalesce(t *testing.T) {
	const window = 50 * time.Millisecond
	c := NewCapabilities(CapabilitiesOptions{Window: window})
	got := make(chan string, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx, nil, nil, func(_ context.Context, etag string) error { got <- etag; return nil })
	<-got // the first answer
	for i := int64(1); i <= 5; i++ {
		c.Apply(allEnabled(), 100+i)
	}
	select {
	case e := <-got:
		if e != "c-105-1" {
			t.Fatalf("pushed %s", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no push")
	}
	select {
	case e := <-got:
		t.Fatalf("a second push %s", e)
	case <-time.After(window + 150*time.Millisecond):
	}
}

// T06 (N11): the answer costs no query and little time: p95 of 10,000 reads <= 20 ms,
// the body <= 1 KB with the nine features (an admin's, with two features off).
func TestCapabilitiesBudget(t *testing.T) {
	c := NewCapabilities(CapabilitiesOptions{})
	d := setWant(setWant(allEnabled(), "search.semantic", components.WantInstalled, false), "youtube", components.WantInstalled, false)
	c.Apply(d, 1842)
	times := make([]time.Duration, 10000)
	for i := range times {
		t0 := time.Now()
		s := c.Snapshot()
		_ = s.Body(i%2 == 0)
		_, _ = s.ComponentOff("livetv")
		times[i] = time.Since(t0)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	if p95 := times[len(times)*95/100]; p95 > 20*time.Millisecond {
		t.Fatalf("p95 %v", p95)
	}
	for _, admin := range []bool{false, true} {
		if n := len(c.Snapshot().Body(admin)); n > 1024 {
			t.Fatalf("admin=%v body %d bytes", admin, n)
		}
	}
}

func BenchmarkCapabilities(b *testing.B) {
	c := NewCapabilities(CapabilitiesOptions{})
	c.Apply(setWant(allEnabled(), "search.semantic", components.WantInstalled, false), 1842)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := c.Snapshot()
		_ = s.Body(i%2 == 0)
	}
}
