package components

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

func effectIn(effects []Effect, id string) (Effect, bool) {
	for _, e := range effects {
		if e.ID == id {
			return e, true
		}
	}
	return Effect{}, false
}

// notOnDisk reports ids as the packs runner does for assets this machine does not have.
func notOnDisk(t *testing.T, st *store.Store, machine string, ids ...string) {
	t.Helper()
	var rows []store.Status
	for _, id := range ids {
		rows = append(rows, store.Status{ID: id, Phase: components.PhaseNotInstalled,
			Conditions: []store.Condition{{Type: components.CondInstalled, Status: "false"}}})
	}
	if err := st.ReportStatus(ctx, machine, "packs", rows); err != nil {
		t.Fatal(err)
	}
}

// With no stt process (transcription.workers = 0) the pipeline's main worker runs
// transcription's stages and reports it running, RequirementsMet true. With runtime.ml
// and every Whisper model not on disk, GET says waiting, RequirementsMet false naming
// runtime.ml; a preview and a bundle dry run read the same rule, from and to.
func TestTranscriptionWaitsForItsFiles(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	s := NewService(db)
	notOnDisk(t, s.Store, store.Home, "runtime.ml", "model.whisper-turbo", "model.whisper-large-v3", "model.whisper-medium")
	if err := s.Store.ReportStatus(ctx, store.Home, "pipeline", []store.Status{{ID: "transcription",
		Phase: components.PhaseRunning, Conditions: []store.Condition{
			{Type: components.CondRequirementsMet, Status: "true"}, {Type: components.CondReady, Status: "true"}}}}); err != nil {
		t.Fatal(err)
	}
	wantReason := ReasonRequirementMissing + ":runtime.ml"
	d, err := s.Show(ctx, "transcription")
	if err != nil {
		t.Fatal(err)
	}
	if d.Phase != components.PhaseWaiting || d.Reason != wantReason || d.Expected.To != components.PhaseWaiting {
		t.Fatalf("GET: %s %q, expected %+v", d.Phase, d.Reason, d.Expected)
	}
	var met *store.Condition
	for _, p := range d.Machines[0].Parts {
		for i, c := range p.Conditions {
			if p.Part == "pipeline" && c.Type == components.CondRequirementsMet {
				met = &p.Conditions[i]
			}
		}
	}
	if met == nil || met.Status != "false" || met.Reason != wantReason {
		t.Fatalf("RequirementsMet %+v in %+v", met, d.Machines[0].Parts)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list.Components {
		if v.ID == "transcription" && (v.Phase != components.PhaseWaiting || v.Reason != wantReason) {
			t.Fatalf("list: %s %q", v.Phase, v.Reason)
		}
	}

	p, err := s.Preview(ctx, Request{Changes: []Change{want("transcription", components.WantInstalled)}})
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := effectIn(p.Effects, "transcription"); !ok || e.From != components.PhaseWaiting || e.To != components.PhaseOff {
		t.Fatalf("preview off: %+v (listed %v)", e, ok)
	}
	res, err := s.Apply(ctx, Request{Changes: []Change{want("transcription", components.WantInstalled)}}, who)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := effectIn(res.Effect.Effects, "transcription"); !ok || e.From != components.PhaseWaiting {
		t.Fatalf("apply off: %+v (listed %v)", e, ok)
	}
	p, err = s.Preview(ctx, Request{Changes: []Change{want("transcription", components.WantEnabled)}})
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := effectIn(p.Effects, "transcription"); !ok || e.To != components.PhaseWaiting || e.Reason != wantReason {
		t.Fatalf("preview on: %+v (listed %v)", e, ok)
	}
	b, err := s.Bundle(ctx, "full", false, true, nil, who)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := effectIn(b.Effect.Effects, "transcription"); !ok || e.To != components.PhaseWaiting || e.Reason != wantReason {
		t.Fatalf("bundle dry run: %+v (listed %v)", e, ok)
	}
}

// A preview's from is the phase GET reports now, not the one the desired state alone
// gives: Smart search really waits (its supervisor part says so), and turning it off
// previews waiting -> off, on the home, in a bundle dry run and on a joined machine.
func TestPreviewFromIsTheActualPhase(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	s := NewService(db)
	waiting := []store.Status{{ID: "search.semantic", Phase: components.PhaseWaiting, Conditions: []store.Condition{
		{Type: components.CondRequirementsMet, Status: "unknown", Params: map[string]any{"component": "runtime.ml"}},
		{Type: components.CondReady, Status: "false"}}}}
	if err := s.Store.ReportStatus(ctx, store.Home, "supervisor", waiting); err != nil {
		t.Fatal(err)
	}
	v, err := s.Show(ctx, "search.semantic")
	if err != nil || v.Phase != components.PhaseWaiting {
		t.Fatalf("GET: %s %v", v.Phase, err)
	}
	off := Request{Changes: []Change{want("search.semantic", components.WantInstalled)}}
	p, err := s.Preview(ctx, off)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := effectIn(p.Effects, "search.semantic"); !ok || e.From != components.PhaseWaiting || e.To != components.PhaseOff {
		t.Fatalf("preview: %+v (listed %v)", e, ok)
	}
	b, err := s.Bundle(ctx, "core", false, true, nil, who)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := effectIn(b.Effect.Effects, "search.semantic"); !ok || e.From != components.PhaseWaiting || e.To != components.PhaseOff {
		t.Fatalf("bundle dry run: %+v (listed %v)", e, ok)
	}

	n := joinNode(t, db, dsn, "worker1")
	if err := (&store.Store{DB: n.db}).ReportStatus(ctx, n.id, "pipeline", waiting); err != nil {
		t.Fatal(err)
	}
	mp, err := s.Preview(ctx, Request{Changes: []Change{{ID: "search.semantic", Want: components.WantInstalled, Machine: n.id}}})
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := effectIn(mp.Effects, "search.semantic"); !ok || e.From != components.PhaseWaiting || e.To != components.PhaseOff {
		t.Fatalf("machine preview: %+v (listed %v) in %+v", e, ok, mp)
	}
	res, err := s.Apply(ctx, Request{Changes: []Change{{ID: "search.semantic", Want: components.WantInstalled, Machine: n.id}}}, who)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := effectIn(res.Effect.Effects, "search.semantic"); !ok || e.From != components.PhaseWaiting {
		t.Fatalf("machine apply: %+v (listed %v)", e, ok)
	}
}
