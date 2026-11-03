package runner

import (
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

func relayRow(l *store.Licence) store.Desired {
	return store.Desired{ID: "cloud.relay", Want: components.WantEnabled, Generation: 3, Licence: l}
}

func condOf(s store.Status, typ string) (store.Condition, bool) {
	for _, c := range s.Conditions {
		if c.Type == typ {
			return c, true
		}
	}
	return store.Condition{}, false
}

// Story 51.19 T06: a licence that ends while the component runs stops it at the end of
// the grace, with nobody writing the desired state (the API may be down): Waiting,
// licence_missing, the want kept. A licence published again starts it.
func TestLicenceEndStopsTheLoopOnTime(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk)
	var p polite
	r.Declare("cloudlink_reporter", p.fn)
	until := clk.Now().Add(time.Minute)
	r.Apply(map[string]store.Desired{"cloud.relay": relayRow(&store.Licence{Licensed: true, Until: &until})})
	eventually(t, "the reporter to start", func() bool { return p.starts.Load() == 1 })
	if c, ok := condOf(statusOf(r, "cloud.relay"), components.CondLicensed); !ok || c.Status != "true" {
		t.Fatalf("Licensed condition %+v %v", c, ok)
	}
	if lic, known := r.Licensed("cloud.relay"); !lic || !known {
		t.Fatalf("Licensed %v %v", lic, known)
	}

	eventually(t, "the licence's wake", func() bool { return clk.pending() >= 1 })
	clk.Advance(2 * time.Minute)
	eventually(t, "the reporter to stop", func() bool { return p.stops.Load() == 1 })
	eventually(t, "Waiting", func() bool { return statusOf(r, "cloud.relay").Phase == components.PhaseWaiting })
	s := statusOf(r, "cloud.relay")
	if s.Reason() != ReasonLicenceMissing {
		t.Fatalf("reason %q", s.Reason())
	}
	if c, _ := condOf(s, components.CondLicensed); c.Status != "false" || c.Reason != ReasonLicenceMissing || c.Params["feature"] != "cloud_relay" {
		t.Fatalf("Licensed condition %+v", c)
	}
	if s.ObservedGeneration != 3 {
		t.Fatalf("observed generation %d: the want's generation is kept", s.ObservedGeneration)
	}
	if r.On("cloud.relay") {
		t.Fatal("cloud.relay still on")
	}
	if lic, known := r.Licensed("cloud.relay"); lic || !known {
		t.Fatalf("Licensed %v %v after the grace", lic, known)
	}
	// cloud.link is never gated
	if !r.On("cloud.link") {
		t.Fatal("cloud.link went off with the licence")
	}

	// a licence applied again: the same row with a new until, and it runs
	next := clk.Now().Add(31 * 24 * time.Hour)
	r.Apply(map[string]store.Desired{"cloud.relay": relayRow(&store.Licence{Licensed: true, Until: &next})})
	eventually(t, "the reporter to start again", func() bool { return p.starts.Load() == 2 })
	eventually(t, "Running", func() bool { return statusOf(r, "cloud.relay").Phase == components.PhaseRunning })
}

// A revoked or never granted licence: Waiting at once. Nothing published (a database the
// API has not written yet): it runs as today, and RelayGate is not told anything.
func TestLicenceNotGranted(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk)
	var p polite
	r.Declare("cloudlink_reporter", p.fn)
	r.Apply(map[string]store.Desired{"cloud.relay": relayRow(nil)})
	eventually(t, "the reporter to start", func() bool { return p.starts.Load() == 1 })
	if _, known := r.Licensed("cloud.relay"); known {
		t.Fatal("nothing published, yet known")
	}
	r.Apply(map[string]store.Desired{"cloud.relay": relayRow(&store.Licence{Licensed: false})})
	eventually(t, "the reporter to stop", func() bool { return p.stops.Load() == 1 })
	eventually(t, "Waiting", func() bool {
		s := statusOf(r, "cloud.relay")
		return s.Phase == components.PhaseWaiting && s.Reason() == ReasonLicenceMissing
	})
	// installed and not licensed: Off, not Waiting (it is not wanted)
	row := relayRow(&store.Licence{Licensed: false})
	row.Want = components.WantInstalled
	r.Apply(map[string]store.Desired{"cloud.relay": row})
	eventually(t, "Off", func() bool { return statusOf(r, "cloud.relay").Phase == components.PhaseOff })
}

// With the kill switch the licence is not read: every loop runs, the gate keeps its own
// check (Licensed is not known).
func TestLicenceWithReconcileOff(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk, func(o *Options) { o.NoReconcile = true })
	var p polite
	r.Declare("cloudlink_reporter", p.fn)
	r.Apply(map[string]store.Desired{"cloud.relay": relayRow(&store.Licence{Licensed: false})})
	eventually(t, "the reporter to start", func() bool { return p.starts.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	if p.stops.Load() != 0 || !r.On("cloud.relay") {
		t.Fatal("reconcile off: the licence stopped the loop")
	}
	if _, known := r.Licensed("cloud.relay"); known {
		t.Fatal("reconcile off: Licensed is known")
	}
}
