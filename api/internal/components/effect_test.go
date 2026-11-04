package components

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

var who = Actor{By: "maya", Source: "web"}

// allOn is every switchable component with an enabled row (generation 1).
func allOn() map[string]store.Desired {
	out := map[string]store.Desired{}
	for _, c := range components.Default().Components() {
		if c.Switchable {
			out[c.ID] = store.Desired{ID: c.ID, Want: components.WantEnabled, Generation: 1}
		}
	}
	return out
}

func with(rows map[string]store.Desired, id, want string) map[string]store.Desired {
	rows[id] = store.Desired{ID: id, Want: want, Generation: rows[id].Generation}
	return rows
}

func planOf(t *testing.T, rows map[string]store.Desired, req Request, licensed func(string) bool) (*plan, error) {
	t.Helper()
	return makePlan(components.Default(), licensed, time.Unix(1700000000, 0), rows, 7, req, who, false)
}

func effectOf(p *plan, id string) (Effect, bool) {
	for _, e := range p.preview.Effects {
		if e.ID == id {
			return e, true
		}
	}
	return Effect{}, false
}

// refusalOf is err's refusal (a value: it is no error to check), which must be code.
func refusalOf(t *testing.T, err error, code string, status int) Refusal {
	t.Helper()
	r, ok := AsRefusal(err)
	if !ok || r.Code != code || r.Status != status {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
	return *r
}

func want(id, w string) Change { return Change{ID: id, Want: w} }

// T01: turning a component off sends what wants it to degraded; its requirements and
// their wants stay.
func TestEffectOfTurningARequirementOff(t *testing.T) {
	p, err := planOf(t, allOn(), Request{Changes: []Change{want("llm", components.WantInstalled)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for id, w := range map[string]Effect{
		"llm":      {To: components.PhaseOff},
		"classify": {To: components.PhaseDegraded, Reason: "want_missing:llm"},
	} {
		e, ok := effectOf(p, id)
		if !ok || e.To != w.To || e.Reason != w.Reason || e.From != components.PhaseRunning {
			t.Errorf("%s: %+v (listed %v), want %+v", id, e, ok, w)
		}
	}
	if _, ok := effectOf(p, "transcription"); ok {
		t.Error("an unrelated component is listed")
	}
	if _, ok := effectOf(p, "runtime.ollama"); ok {
		t.Error("a requirement of what goes off is listed")
	}
	if len(p.writes) != 1 || p.writes[0].Desired.ID != "llm" || p.writes[0].Audit.Action != "component.disable" {
		t.Fatalf("writes %+v: only the change itself, the dependents keep their wish", p.writes)
	}
	if p.preview.DrainS < components.DefaultStopGraceS {
		t.Errorf("drain_s %d", p.preview.DrainS)
	}
}

// T02: removing a requirement of something on is refused with every enabled component
// that needs it, directly or not; an "any:" group met by another model is not.
func TestRemoveRefusedWhileRequired(t *testing.T) {
	_, err := planOf(t, allOn(), Request{Changes: []Change{want("runtime.ml", components.WantAbsent)}}, nil)
	r := refusalOf(t, err, CodeRequiredBy, http.StatusConflict)
	for _, id := range []string{"transcription", "subtitles", "search.semantic", "model.whisper-turbo"} {
		if !slices.Contains(r.RequiredBy, id) {
			t.Errorf("required_by %v lacks %s", r.RequiredBy, id)
		}
	}
	// two Whisper models on disk: either can go, not both
	rows := allOn()
	rows = with(rows, "model.whisper-medium", components.WantAbsent)
	if _, err := planOf(t, rows, Request{Changes: []Change{want("model.whisper-turbo", components.WantAbsent)}}, nil); err != nil {
		t.Fatalf("another enabled Whisper model is there: %v", err)
	}
	_, err = planOf(t, rows, Request{Changes: []Change{
		want("model.whisper-turbo", components.WantAbsent), want("model.whisper-large-v3", components.WantAbsent)}}, nil)
	r = refusalOf(t, err, CodeRequiredBy, http.StatusConflict)
	if !slices.Equal(r.RequiredBy, []string{"transcription", "subtitles"}) || r.ID != "model.whisper-large-v3" {
		t.Fatalf("second removal: %+v", r)
	}
	// disabling (files kept) is never refused for a dependency
	if _, err := planOf(t, allOn(), Request{Changes: []Change{want("runtime.ml", components.WantInstalled)}}, nil); err != nil {
		t.Fatal(err)
	}
}

// T03: conflicts. In one batch, slots off then in-process on passes; the other order is
// refused unless with_conflicts, which turns slots off in the same batch.
func TestConflictsInABatch(t *testing.T) {
	rows := with(allOn(), "livetv.inprocess", components.WantInstalled)
	p, err := planOf(t, rows, Request{Changes: []Change{
		want("livetv.slots", components.WantInstalled), want("livetv.inprocess", components.WantEnabled)}}, nil)
	if err != nil || len(p.writes) != 2 {
		t.Fatalf("slots off, then in-process on: %v %+v", err, p)
	}
	_, err = planOf(t, rows, Request{Changes: []Change{want("livetv.inprocess", components.WantEnabled)}}, nil)
	r := refusalOf(t, err, CodeConflicts, http.StatusConflict)
	if !slices.Equal(r.Conflicts, []string{"livetv.slots"}) {
		t.Fatalf("conflicts %v", r.Conflicts)
	}
	p, err = planOf(t, rows, Request{WithConflicts: true, Changes: []Change{want("livetv.inprocess", components.WantEnabled)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.writes) != 2 || p.writes[0].Desired.ID != "livetv.slots" || p.writes[0].Desired.Want != components.WantInstalled ||
		p.writes[1].Desired.ID != "livetv.inprocess" || !slices.Equal(p.preview.Conflicts, []string{"livetv.slots"}) {
		t.Fatalf("with_conflicts: %+v %v", p.writes, p.preview.Conflicts)
	}
}

// T04: a row's generation, or the batch's revision, that moved on is refused with the
// current row.
func TestStaleGeneration(t *testing.T) {
	old := int64(0)
	_, err := planOf(t, allOn(), Request{Changes: []Change{{ID: "llm", Want: components.WantInstalled, Generation: &old}}}, nil)
	r := refusalOf(t, err, CodeStale, http.StatusConflict)
	if r.Current == nil || r.Current.Generation != 1 || r.Current.Want != components.WantEnabled {
		t.Fatalf("current %+v", r.Current)
	}
	cur := int64(1)
	if _, err := planOf(t, allOn(), Request{Changes: []Change{{ID: "llm", Want: components.WantInstalled, Generation: &cur}}}, nil); err != nil {
		t.Fatal(err)
	}
	rev := int64(6)
	_, err = planOf(t, allOn(), Request{Generation: &rev, Changes: []Change{want("llm", components.WantInstalled)}}, nil)
	r = refusalOf(t, err, CodeStale, http.StatusConflict)
	if r.Revision == nil || *r.Revision != 7 {
		t.Fatalf("revision %v", r.Revision)
	}
}

// T06 and the other plain refusals: core, an unknown id, a bad want.
func TestPlainRefusals(t *testing.T) {
	_, err := planOf(t, allOn(), Request{Changes: []Change{want("core.api", components.WantInstalled)}}, nil)
	refusalOf(t, err, CodeNotSwitchable, http.StatusConflict)
	_, err = planOf(t, allOn(), Request{Changes: []Change{want("nope", components.WantInstalled)}}, nil)
	refusalOf(t, err, CodeUnknown, http.StatusNotFound)
	_, err = planOf(t, allOn(), Request{Changes: []Change{want("llm", "on")}}, nil)
	refusalOf(t, err, CodeBadWant, http.StatusBadRequest)
	_, err = planOf(t, allOn(), Request{Changes: []Change{{ID: "llm"}}}, nil)
	refusalOf(t, err, CodeBadWant, http.StatusBadRequest)
}

// Turning on a component whose licence flag the home lacks is 403 licence_missing.
func TestLicenceGate(t *testing.T) {
	rows := with(allOn(), "cloud.relay", components.WantInstalled)
	none := func(string) bool { return false }
	_, err := planOf(t, rows, Request{Changes: []Change{want("cloud.relay", components.WantEnabled)}}, none)
	if r := refusalOf(t, err, CodeLicence, http.StatusForbidden); r.Licence != "cloud_relay" {
		t.Fatalf("licence %q", r.Licence)
	}
	if _, err := planOf(t, rows, Request{Changes: []Change{want("cloud.relay", components.WantEnabled)}}, nil); err != nil {
		t.Fatal(err)
	}
	// cloud.link is never gated
	rows = with(rows, "cloud.link", components.WantInstalled)
	if _, err := planOf(t, rows, Request{Changes: []Change{want("cloud.link", components.WantEnabled)}}, none); err != nil {
		t.Fatal(err)
	}
}

// A hold is {by, at} with no end; start clears it; holding what is held writes nothing.
func TestHoldAndRelease(t *testing.T) {
	on, off := true, false
	p, err := planOf(t, allOn(), Request{Changes: []Change{{ID: "transcription", Hold: &on}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := p.writes[0]
	if w.Audit.Action != "component.hold" || w.Desired.Hold == nil || w.Desired.Hold.By != "maya" || w.Desired.Want != components.WantEnabled {
		t.Fatalf("hold write %+v", w)
	}
	if e, _ := effectOf(p, "transcription"); e.To != components.PhaseStopped || e.Reason != "held" {
		t.Fatalf("effect %+v", e)
	}
	if e, _ := effectOf(p, "subtitles"); e.To != components.PhaseWaiting {
		t.Fatalf("subtitles %+v", e)
	}
	held := p.rows
	p, err = planOf(t, held, Request{Changes: []Change{{ID: "transcription", Hold: &on}}}, nil)
	if err != nil || len(p.writes) != 0 {
		t.Fatalf("a second stop wrote %+v %v", p.writes, err)
	}
	p, err = planOf(t, held, Request{Changes: []Change{{ID: "transcription", Hold: &off}}}, nil)
	if err != nil || len(p.writes) != 1 || p.writes[0].Audit.Action != "component.release" || p.writes[0].Desired.Hold != nil {
		t.Fatalf("start: %+v %v", p.writes, err)
	}
}

// A preview lists every refusal and leaves the refused change out of the effect.
func TestPreviewCollects(t *testing.T) {
	p, err := makePlan(components.Default(), nil, time.Now(), allOn(), 1, Request{Changes: []Change{
		want("core.api", components.WantInstalled), want("llm", components.WantInstalled)}}, who, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.preview.Refused) != 1 || p.preview.Refused[0].Code != CodeNotSwitchable {
		t.Fatalf("refused %+v", p.preview.Refused)
	}
	if e, ok := effectOf(p, "llm"); !ok || e.To != components.PhaseOff {
		t.Fatalf("llm %+v", e)
	}
}

// A bundle turns off what it leaves out and on what it holds, as one batch.
func TestBundleApply(t *testing.T) {
	p, err := planOf(t, allOn(), Request{Bundle: "core"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.bundle != "core" {
		t.Fatalf("bundle %q", p.bundle)
	}
	ids := map[string]string{}
	for _, w := range p.writes {
		if w.Audit.Action != "component.bundle_apply" {
			t.Fatalf("action %s", w.Audit.Action)
		}
		ids[w.Desired.ID] = w.Desired.Want
	}
	if ids["transcription"] != components.WantInstalled || ids["llm"] != components.WantInstalled {
		t.Fatalf("core keeps transcription or llm: %v", ids)
	}
	if _, ok := ids["media.thumbnails"]; ok {
		t.Fatal("thumbnails were on already")
	}
	// back to full, from core: the licence-gated relay is left as it is
	rows := p.rows
	p, err = planOf(t, rows, Request{Bundle: "full"}, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range p.writes {
		if w.Desired.Want != components.WantEnabled {
			t.Fatalf("going to full turned %s off", w.Desired.ID)
		}
	}
	if _, err := planOf(t, rows, Request{Bundle: "nope"}, nil); err == nil {
		t.Fatal("an unknown bundle was taken")
	}
}

// No row is the catalog default (on), as the runners read it.
func TestNoRowIsOn(t *testing.T) {
	g := gates(components.Default(), map[string]store.Desired{})
	for _, c := range components.Default().Components() {
		if !g[c.ID].on {
			t.Fatalf("%s is off with no rows", c.ID)
		}
	}
	g = gates(components.Default(), with(allOn(), "transcription", components.WantAbsent))
	if g["transcription"].phase != components.PhaseNotInstalled || g["subtitles"].phase != components.PhaseWaiting {
		t.Fatalf("absent: %+v %+v", g["transcription"], g["subtitles"])
	}
}

// An asset has no run: installed (on disk) it meets what requires it, as enabled does.
// The seed leaves assets installed, so a home where everything runs previews from
// running, not waiting; removing one is refused while it is required, and a bundle
// neither counts an installed asset as missing nor turns it on again.
func TestInstalledAssetMeetsRequirements(t *testing.T) {
	rows := allOn()
	for _, c := range components.Default().Components() {
		if c.Switchable && c.Kind == components.KindAsset {
			rows = with(rows, c.ID, components.WantInstalled)
		}
	}
	g := gates(components.Default(), rows)
	for _, id := range []string{"llm", "transcription", "search.semantic", "classify", "runtime.ollama"} {
		if g[id].phase != components.PhaseRunning || g[id].reason != "" {
			t.Errorf("%s: %+v, want running", id, g[id])
		}
	}
	p, err := planOf(t, rows, Request{Changes: []Change{want("llm", components.WantInstalled)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := effectOf(p, "llm"); !ok || e.From != components.PhaseRunning || e.To != components.PhaseOff {
		t.Fatalf("llm: %+v (listed %v)", e, ok)
	}
	_, err = planOf(t, rows, Request{Changes: []Change{want("runtime.ollama", components.WantAbsent)}}, nil)
	if r := refusalOf(t, err, CodeRequiredBy, http.StatusConflict); !slices.Contains(r.RequiredBy, "llm") {
		t.Fatalf("required_by %v", r.RequiredBy)
	}
	s := &Service{Catalog: components.Default()}
	d := s.diffOf(rows, "full", false)
	for _, id := range append(append([]string{}, d.Enable...), d.Disable...) {
		if c, _ := components.Default().Get(id); c.Kind == components.KindAsset {
			t.Errorf("bundle full changes the installed asset %s", id)
		}
	}
	line := s.bundleLine(&snapshot{rows: rows})
	for _, id := range line.Minus {
		if c, _ := components.Default().Get(id); c.Kind == components.KindAsset {
			t.Errorf("bundle line names the installed asset %s missing", id)
		}
	}
}

// The install route's rule: a switchable asset the home wants is asked again unless the
// home has it on disk or is installing it; a failed install is asked again (Retry).
func TestInstallAgain(t *testing.T) {
	asset := func(want string) Detail {
		return Detail{View: View{ID: "runtime.ml", Kind: components.KindAsset, Switchable: true, Want: want}}
	}
	onDisk := asset(components.WantEnabled)
	onDisk.Machines = []MachineView{{Machine: store.Home, Parts: []store.Status{{ID: "runtime.ml", Phase: components.PhaseOff,
		Conditions: []store.Condition{{Type: components.CondInstalled, Status: "true"}}}}}}
	failed := asset(components.WantEnabled)
	failed.Progress = &InstallProgress{Phase: components.PhaseFailed}
	busy := asset(components.WantEnabled)
	busy.Progress = &InstallProgress{Phase: components.PhaseInstalling, Busy: true}
	notAsset := asset(components.WantEnabled)
	notAsset.Kind = "stages"
	for name, c := range map[string]struct {
		d    Detail
		want bool
	}{
		"no status":    {asset(components.WantEnabled), true},
		"installed":    {asset(components.WantInstalled), true},
		"absent":       {asset(components.WantAbsent), false},
		"on disk":      {onDisk, false},
		"failed":       {failed, true},
		"busy":         {busy, false},
		"not an asset": {notAsset, false},
	} {
		if got := InstallAgain(c.d); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// Again writes a row whose want does not change (a new generation), audited
// component.install; without it nothing is written.
func TestAgainWritesTheRow(t *testing.T) {
	rows := allOn()
	p, err := makePlan(components.Default(), nil, time.Now(), rows, 1,
		Request{Changes: []Change{{ID: "model.embedder", Want: components.WantEnabled}}}, who, false)
	if err != nil || len(p.writes) != 0 {
		t.Fatalf("a change to the same want wrote: %v %+v", err, p.writes)
	}
	p, err = makePlan(components.Default(), nil, time.Now(), rows, 1,
		Request{Changes: []Change{{ID: "model.embedder", Want: components.WantEnabled, Again: true}}}, who, false)
	if err != nil || len(p.writes) != 1 || p.writes[0].Audit.Action != ActionInstall {
		t.Fatalf("again: %v %+v", err, p.writes)
	}
}
