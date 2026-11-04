package components

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// The dependency and conflict walk over the catalog graph (story 51.08 §3): what a set
// of changes does to every component, and which changes are refused. Preview and every
// write run the same plan.

// Change is one change of a request: a want, a hold (true: stop, false: start), or both.
// Generation, when given, is the row's generation the caller read (0: no row).
type Change struct {
	ID         string `json:"id"`
	Want       string `json:"want,omitempty"`
	Hold       *bool  `json:"hold,omitempty"`
	Generation *int64 `json:"generation,omitempty"`
	// Machine, when set, makes the change one machine's override (story 51.12): Want may
	// then also be "follow" (clear the override's want), Hold is that machine's hold and
	// Generation the override row's (0: none). Every change of a request names the same
	// machine, or none.
	Machine string `json:"machine,omitempty"`
	// Again writes the row even when nothing in it changes (a new generation, audited
	// component.install): the install route's ask to install a wanted asset that is not
	// on disk, or to try a failed install again. Not settable from JSON.
	Again bool `json:"-"`
}

// Request is one batch of changes. Generation, when given, is the revision the caller
// read (component_meta.revision): a batch over a list that moved on is refused.
// WithConflicts turns off what a component being enabled conflicts with, in the same
// transaction. Bundle applies a bundle (full, core, worker) instead of Changes.
type Request struct {
	Changes       []Change `json:"changes"`
	Generation    *int64   `json:"generation,omitempty"`
	WithConflicts bool     `json:"with_conflicts,omitempty"`
	Bundle        string   `json:"bundle,omitempty"`
	// RemoveUnused, with Bundle, also removes the files of the assets the bundle leaves
	// out (want absent); without it they are only turned off (story 51.17).
	RemoveUnused bool `json:"remove_unused,omitempty"`
}

// Actor is who asks: By is the row's updated_by and the hold's by; Source is
// audit_log.actor_source (web, cli, remote).
type Actor struct {
	By     string
	UserID string
	Source string
}

// Effect is what one component becomes.
type Effect struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason,omitempty"`
}

// Preview is the effect of a request, changing nothing.
type Preview struct {
	Effects          []Effect  `json:"effects"`
	Conflicts        []string  `json:"conflicts"`
	Refused          []Refusal `json:"refused"`
	QueuedJobsParked int       `json:"queued_jobs_parked"`
	DrainS           int       `json:"drain_s"`
}

// Refusal is a change the service will not make. It is the error of a write.
type Refusal struct {
	Status     int      `json:"-"`
	ID         string   `json:"id,omitempty"`
	Code       string   `json:"code"`
	Detail     string   `json:"detail"`
	RequiredBy []string `json:"required_by,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	Licence    string   `json:"licence,omitempty"`
	// Tier is the home's tier, with a licence_missing refusal (free, home, pro).
	Tier string `json:"tier,omitempty"`
	// Components are every gated component a refused bundle would turn on.
	Components []string       `json:"components,omitempty"`
	Current    *store.Desired `json:"current,omitempty"`
	Revision   *int64         `json:"revision,omitempty"`
}

func (r *Refusal) Error() string { return "components: " + r.Code + ": " + r.Detail }

// Refusal codes.
const (
	CodeUnknown       = "unknown_component"
	CodeNotSwitchable = "not_switchable"
	CodeBadWant       = "bad_want"
	CodeBadBundle     = "bad_bundle"
	CodeStale         = "stale_generation"
	CodeLicence       = "licence_missing"
	CodeConflicts     = "component_conflicts"
	CodeRequiredBy    = "component_required_by"
	CodeNoUntil       = "hold_has_no_until"
	// a restart (story 51.08 §2): a kind with nothing to restart (an asset, a stage
	// group, an extension), or a component that is off, held or waiting
	CodeNotRestartable = "not_restartable"
	CodeNotRunning     = "not_running"
)

// gate is what the desired state alone makes of one component, the way the runners
// read it (51.05): no row, or a core.* component, is on.
type gate struct {
	on     bool
	phase  string
	reason string
}

// gates computes every component's gate over rows, requirements first, every licence
// granted.
func gates(cat *components.Catalog, rows map[string]store.Desired) map[string]gate {
	return gatesLicensed(cat, rows, nil)
}

// gatesLicensed is gates with the home's licence (nil: every flag): an enabled component
// whose flag it lacks waits, licence_missing (story 51.19). An asset has no run, so on
// disk (installed or enabled) it is on and meets what requires it, as the runners read
// it (runner.GatesAt); only absent is off.
func gatesLicensed(cat *components.Catalog, rows map[string]store.Desired, licensed func(string) bool) map[string]gate {
	return gatesOn(cat, rows, licensed, nil)
}

// ReasonRequirementMissing is the reason of a component that is on but waits for the
// files of an asset it requires (a runtime, a model) the machine does not have yet:
// "requirement_missing:<asset>".
const ReasonRequirementMissing = "requirement_missing"

// gatesOn is gatesLicensed with what the machine has on disk: missing reports an asset
// the machine is known not to have (its status row says Installed false; nil: none).
// An enabled component one of whose requirements is only such assets waits for them
// (requirement_missing:<asset>), still on (its gate is open: the runners start it and
// it waits); a wanted asset not there makes it degraded (want_missing). This is the one
// rule both a preview and the status (settle) read, so they say the same.
func gatesOn(cat *components.Catalog, rows map[string]store.Desired, licensed func(string) bool, missing func(string) bool) map[string]gate {
	out := map[string]gate{}
	for _, id := range cat.Order() {
		c, _ := cat.Get(id)
		d, has := rows[id]
		g := gate{on: true, phase: components.PhaseRunning}
		switch {
		case !c.Switchable || !has:
		case d.Want == components.WantAbsent:
			g = gate{phase: components.PhaseNotInstalled}
		case c.Kind == components.KindAsset:
			// on disk: met (no hold, licence or requirement applies to a file)
		case d.Want != components.WantEnabled:
			g = gate{phase: components.PhaseOff}
		case d.Hold != nil:
			g = gate{phase: components.PhaseStopped, reason: "held"}
		case c.Licence != "" && licensed != nil && !licensed(c.Licence):
			g = gate{phase: components.PhaseWaiting, reason: CodeLicence}
		default:
			for _, req := range cat.Requires(id) {
				if !slices.ContainsFunc(req.AnyOf, func(x string) bool { return out[x].on }) {
					g = gate{phase: components.PhaseWaiting, reason: "dependency_off:" + req.AnyOf[0]}
					break
				}
			}
		}
		// on (by its row or the catalog's default) but a requirement's files are not here:
		// it waits for them
		if a := missingAsset(cat, id, missing); g.on && c.Switchable && c.Kind != components.KindAsset && a != "" {
			g = gate{on: true, phase: components.PhaseWaiting, reason: ReasonRequirementMissing + ":" + a}
		}
		out[id] = g
	}
	// wants do not order the catalog: read them once every gate is known
	for _, id := range cat.Order() {
		c, _ := cat.Get(id)
		g := out[id]
		if !g.on || g.phase != components.PhaseRunning {
			continue
		}
		for _, w := range c.Wants {
			if !out[w].on || isMissingAsset(cat, w, missing) {
				g.phase, g.reason = components.PhaseDegraded, "want_missing:"+w
				out[id] = g
				break
			}
		}
	}
	return out
}

// missingAsset is the first asset id requires that the machine does not have: of a
// requirement whose every alternative is an asset missing reports. "" when each
// requirement has one there, or is no asset, or missing is nil (the files unknown: taken
// as there, as before packs).
func missingAsset(cat *components.Catalog, id string, missing func(string) bool) string {
	if missing == nil {
		return ""
	}
	for _, req := range cat.Requires(id) {
		if len(req.AnyOf) > 0 && !slices.ContainsFunc(req.AnyOf, func(a string) bool { return !isMissingAsset(cat, a, missing) }) {
			return req.AnyOf[0]
		}
	}
	return ""
}

// isMissingAsset reports whether id is an asset missing says the machine does not have.
func isMissingAsset(cat *components.Catalog, id string, missing func(string) bool) bool {
	c, ok := cat.Get(id)
	return ok && missing != nil && c.Kind == components.KindAsset && missing(id)
}

// settle is a machine's status rows of a component after its gate g (gatesOn, with
// that machine's files): the part that runs it (the one with a RequirementsMet
// condition) and reports running while g waits for a requirement's files waits too,
// RequirementsMet false naming the asset (a runner does not check files: the stages of
// transcription run inside the pipeline's worker when it has no stt process, and that
// worker reports running); one that runs while g lacks what it wants is degraded. A
// RequirementsMet that says neither yes nor why is given g's reason, so it names what
// it waits on. Other rows are kept.
func settle(rows []store.Status, g gate) []store.Status {
	code, asset, _ := strings.Cut(g.reason, ":")
	needs := g.phase == components.PhaseWaiting && asset != "" &&
		(code == ReasonRequirementMissing || code == "dependency_off")
	wants := g.phase == components.PhaseDegraded && code == "want_missing"
	if !needs && !wants {
		return rows
	}
	out := make([]store.Status, len(rows))
	for i, r := range rows {
		out[i] = r
		k := slices.IndexFunc(r.Conditions, func(c store.Condition) bool { return c.Type == components.CondRequirementsMet })
		if k < 0 {
			continue
		}
		if wants {
			if r.Phase == components.PhaseRunning {
				out[i].Phase = components.PhaseDegraded
			}
			continue
		}
		runs := r.Phase == components.PhaseRunning || r.Phase == components.PhaseDegraded || r.Phase == components.PhaseStarting
		waits := code == ReasonRequirementMissing && runs
		c := r.Conditions[k]
		if !waits && (c.Status == "true" || c.Reason != "") {
			continue
		}
		conds := slices.Clone(r.Conditions)
		conds[k] = store.Condition{Type: components.CondRequirementsMet, Status: "false", Reason: g.reason,
			Params: map[string]any{"component": asset}, ChangedAt: c.ChangedAt, Generation: c.Generation}
		if waits {
			out[i].Phase = components.PhaseWaiting
			for j := range conds {
				if conds[j].Type == components.CondReady && conds[j].Status == "true" {
					conds[j].Status = "false"
				}
			}
		}
		out[i].Conditions = conds
	}
	return out
}

// present reports whether id is on as rows give it: enabled, or for an asset (which has
// no run) on disk, installed or enabled.
func present(cat *components.Catalog, rows map[string]store.Desired, id string) bool {
	w := wantOf(rows, id)
	if c, ok := cat.Get(id); ok && c.Kind == components.KindAsset {
		return w != components.WantAbsent
	}
	return w == components.WantEnabled
}

// wantOf is the want rows give id: no row is the catalog default, on.
func wantOf(rows map[string]store.Desired, id string) string {
	if d, ok := rows[id]; ok {
		return d.Want
	}
	return components.WantEnabled
}

// conflictsOf are the ids id conflicts with, either way round, in catalog order.
func conflictsOf(cat *components.Catalog, id string) []string {
	c, _ := cat.Get(id)
	var out []string
	for _, x := range cat.Order() {
		o, _ := cat.Get(x)
		if x != id && (slices.Contains(c.Conflicts, x) || slices.Contains(o.Conflicts, id)) {
			out = append(out, x)
		}
	}
	return out
}

// requiredBy are the enabled components that would lose a requirement if id went,
// directly or through another one that would (catalog order). An "any:" group still
// met by another enabled alternative does not count.
func requiredBy(cat *components.Catalog, rows map[string]store.Desired, id string) []string {
	gone := map[string]bool{id: true}
	var out []string
	for _, x := range cat.Order() {
		if gone[x] || wantOf(rows, x) != components.WantEnabled {
			continue
		}
		for _, req := range cat.Requires(x) {
			if !slices.ContainsFunc(req.AnyOf, func(a string) bool { return gone[a] }) {
				continue
			}
			if slices.ContainsFunc(req.AnyOf, func(a string) bool {
				return !gone[a] && present(cat, rows, a)
			}) {
				continue
			}
			gone[x] = true
			out = append(out, x)
			break
		}
	}
	return out
}

// effects are the components whose phase or reason differs between before and after.
func effects(cat *components.Catalog, before, after map[string]gate) []Effect {
	out := []Effect{}
	for _, id := range cat.Order() {
		b, a := before[id], after[id]
		if b.phase != a.phase || b.reason != a.reason {
			out = append(out, Effect{ID: id, From: b.phase, To: a.phase, Reason: a.reason})
		}
	}
	return out
}

// plan is the outcome of a request over rows (read at revision rev).
type plan struct {
	writes   []store.BatchWrite
	rows     map[string]store.Desired // the rows after the plan
	bundle   string
	preview  Preview
	goingOff []string // components that run before and not after
}

func refuse(status int, id, code, detail string) *Refusal {
	return &Refusal{Status: status, ID: id, Code: code, Detail: detail}
}

func validWant(w string) bool {
	return w == components.WantAbsent || w == components.WantInstalled || w == components.WantEnabled
}

// bundleChanges are the changes that make rows match bundle b: what it leaves out is
// turned off (its files kept), then what it holds is turned on. A component whose
// licence flag this home lacks is in it too: makePlan refuses the whole bundle then
// (story 51.19), no half-applied bundle.
func bundleChanges(cat *components.Catalog, rows map[string]store.Desired, b string, removeUnused bool) []Change {
	in := map[string]bool{}
	for _, id := range cat.InBundle(b) {
		in[id] = true
	}
	order := cat.Order()
	var out []Change
	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		c, _ := cat.Get(id)
		if removeUnused && c.Kind == components.KindAsset {
			continue // removed below
		}
		// an asset left out keeps its files unless removeUnused (installed and enabled are
		// the same for a file)
		if c.Switchable && c.Kind != components.KindAsset && !in[id] && wantOf(rows, id) == components.WantEnabled {
			out = append(out, Change{ID: id, Want: components.WantInstalled})
		}
	}
	if removeUnused {
		// an asset left out loses its files too (after what needs it is off)
		for i := len(order) - 1; i >= 0; i-- {
			id := order[i]
			c, _ := cat.Get(id)
			if c.Switchable && c.Kind == components.KindAsset && !in[id] && wantOf(rows, id) != components.WantAbsent {
				out = append(out, Change{ID: id, Want: components.WantAbsent})
			}
		}
	}
	for _, id := range order {
		c, _ := cat.Get(id)
		if !c.Switchable || !in[id] || present(cat, rows, id) {
			continue
		}
		out = append(out, Change{ID: id, Want: components.WantEnabled})
	}
	return out
}

// makePlan checks each change of req against the rows the earlier ones leave. For a
// write (collect false) the first refusal is the answer; for a preview every refusal
// is listed and the refused change is left out.
func makePlan(cat *components.Catalog, licensed func(string) bool, now time.Time,
	rows map[string]store.Desired, rev int64, req Request, actor Actor, collect bool) (*plan, error) {
	return makePlanTier(cat, licensed, "", now, rows, rev, req, actor, collect, nil)
}

// licenceRefusal is the 403 of turning on c, whose flag the home's licence lacks.
func licenceRefusal(c *components.Component, tier string) *Refusal {
	r := refuse(http.StatusForbidden, c.ID, CodeLicence, c.ID+" needs the "+c.Licence+" licence")
	r.Licence, r.Tier = c.Licence, tier
	return r
}

// makePlanTier is makePlan with the home's tier, named in a licence_missing refusal, and
// the assets the home does not have (missing, gatesOn; nil: none known).
func makePlanTier(cat *components.Catalog, licensed func(string) bool, tier string, now time.Time,
	rows map[string]store.Desired, rev int64, req Request, actor Actor, collect bool, missing func(string) bool) (*plan, error) {
	if licensed == nil {
		licensed = func(string) bool { return true }
	}
	p := &plan{rows: map[string]store.Desired{}, preview: Preview{Effects: []Effect{}, Conflicts: []string{}, Refused: []Refusal{}}}
	for id, d := range rows {
		p.rows[id] = d
	}
	fail := func(r *Refusal) error {
		if !collect {
			return r
		}
		p.preview.Refused = append(p.preview.Refused, *r)
		return nil
	}
	if req.Generation != nil && *req.Generation != rev {
		r := refuse(http.StatusConflict, "", CodeStale, fmt.Sprintf("the components changed since revision %d (now %d)", *req.Generation, rev))
		r.Revision = &rev
		if err := fail(r); err != nil {
			return nil, err
		}
	}
	changes := req.Changes
	action := ""
	if req.Bundle != "" {
		if !slices.Contains(components.Bundles, req.Bundle) {
			return nil, refuse(http.StatusBadRequest, "", CodeBadBundle, fmt.Sprintf("no bundle %q (full, core or worker)", req.Bundle))
		}
		if len(req.Changes) > 0 {
			return nil, refuse(http.StatusBadRequest, "", CodeBadBundle, "a bundle is applied on its own, without changes")
		}
		changes = bundleChanges(cat, p.rows, req.Bundle, req.RemoveUnused)
		action = "component.bundle_apply"
		p.bundle = req.Bundle
		// a bundle that would turn on a component the licence lacks is refused whole,
		// naming every such component
		var gated []string
		var first *components.Component
		for _, ch := range changes {
			if c, ok := cat.Get(ch.ID); ok && ch.Want == components.WantEnabled && c.Licence != "" && !licensed(c.Licence) {
				gated = append(gated, ch.ID)
				if first == nil {
					first = c
				}
			}
		}
		if len(gated) > 0 {
			r := licenceRefusal(first, tier)
			r.Components = gated
			r.Detail = "the " + req.Bundle + " bundle turns on components this home's plan does not include"
			if !collect {
				return nil, r
			}
			p.preview.Refused = append(p.preview.Refused, *r)
			changes = slices.DeleteFunc(slices.Clone(changes), func(ch Change) bool { return slices.Contains(gated, ch.ID) })
		}
	}
	var batch []store.BatchWrite
	write := func(id string, next store.Desired, verb string) {
		p.rows[id] = next
		if action != "" {
			verb = action
		}
		batch = append(batch, store.BatchWrite{Desired: next,
			Audit: store.Audit{By: actor.By, ActorUserID: actor.UserID, ActorSource: actor.Source, Action: verb}})
	}
	for _, ch := range changes {
		c, ok := cat.Get(ch.ID)
		if !ok {
			if err := fail(refuse(http.StatusNotFound, ch.ID, CodeUnknown, "no component "+ch.ID)); err != nil {
				return nil, err
			}
			continue
		}
		if !c.Switchable {
			if err := fail(refuse(http.StatusConflict, ch.ID, CodeNotSwitchable, ch.ID+" is part of Maktaba's core and cannot be changed")); err != nil {
				return nil, err
			}
			continue
		}
		if (ch.Want == "" && ch.Hold == nil) || (ch.Want != "" && !validWant(ch.Want)) {
			if err := fail(refuse(http.StatusBadRequest, ch.ID, CodeBadWant, fmt.Sprintf("want %q: enabled, installed or absent", ch.Want))); err != nil {
				return nil, err
			}
			continue
		}
		cur, has := p.rows[ch.ID]
		if ch.Generation != nil && *ch.Generation != cur.Generation {
			r := refuse(http.StatusConflict, ch.ID, CodeStale, fmt.Sprintf("%s changed since generation %d (now %d)", ch.ID, *ch.Generation, cur.Generation))
			if has {
				cc := cur
				r.Current = &cc
			}
			if err := fail(r); err != nil {
				return nil, err
			}
			continue
		}
		was := wantOf(p.rows, ch.ID)
		next := store.Desired{ID: ch.ID, Want: was, Hold: cur.Hold, Generation: cur.Generation}
		if ch.Want != "" {
			next.Want = ch.Want
		}
		if ch.Hold != nil {
			switch {
			case *ch.Hold && next.Hold == nil:
				next.Hold = &store.Hold{By: actor.By, At: now}
			case !*ch.Hold:
				next.Hold = nil
			}
		}
		turningOn := next.Want == components.WantEnabled && was != components.WantEnabled
		// asking for enabled is refused without the licence even when the row already
		// says enabled (a lapsed licence keeps the wish; asking again does not pass)
		if ch.Want == components.WantEnabled && c.Licence != "" && !licensed(c.Licence) {
			if err := fail(licenceRefusal(c, tier)); err != nil {
				return nil, err
			}
			continue
		}
		var others []string
		if turningOn {
			for _, x := range conflictsOf(cat, ch.ID) {
				if wantOf(p.rows, x) == components.WantEnabled {
					others = append(others, x)
				}
			}
		}
		if len(others) > 0 && !req.WithConflicts {
			r := refuse(http.StatusConflict, ch.ID, CodeConflicts, ch.ID+" cannot run with what is on")
			r.Conflicts = others
			p.preview.Conflicts = appendNew(p.preview.Conflicts, others...)
			if err := fail(r); err != nil {
				return nil, err
			}
			continue
		}
		if next.Want == components.WantAbsent && was != components.WantAbsent {
			if by := requiredBy(cat, p.rows, ch.ID); len(by) > 0 {
				r := refuse(http.StatusConflict, ch.ID, CodeRequiredBy, ch.ID+" is required by components that are on")
				r.RequiredBy = by
				if err := fail(r); err != nil {
					return nil, err
				}
				continue
			}
		}
		for _, x := range others {
			o := p.rows[x]
			write(x, store.Desired{ID: x, Want: components.WantInstalled, Hold: o.Hold, Generation: o.Generation}, "component.disable")
			p.preview.Conflicts = appendNew(p.preview.Conflicts, x)
		}
		if has && next.Want == cur.Want && (next.Hold == nil) == (cur.Hold == nil) && !ch.Again {
			continue // nothing changes
		}
		verb := ""
		switch {
		case ch.Again && next.Want == was && (next.Hold == nil) == (cur.Hold == nil):
			verb = ActionInstall
		case next.Want != was && next.Want == components.WantEnabled:
			verb = "component.enable"
		case next.Want != was && next.Want == components.WantInstalled:
			verb = "component.disable"
		case next.Want != was:
			verb = "component.remove"
		case next.Hold != nil && cur.Hold == nil:
			verb = "component.hold"
		case next.Hold == nil && cur.Hold != nil:
			verb = "component.release"
		default:
			verb = "component.enable" // a first row for what ran by default
		}
		write(ch.ID, next, verb)
	}
	p.writes = batch
	before, after := gatesOn(cat, rows, licensed, missing), gatesOn(cat, p.rows, licensed, missing)
	p.preview.Effects = effects(cat, before, after)
	for _, id := range cat.Order() {
		if before[id].on && !after[id].on {
			p.goingOff = append(p.goingOff, id)
			c, _ := cat.Get(id)
			grace := c.StopGraceS // a loop's
			if c.HasPart(components.KindProcess) {
				grace = max(grace, components.DefaultStopGraceS)
			}
			if c.HasPart(components.KindStages) {
				grace = max(grace, c.Drain.GraceS)
			}
			if grace > p.preview.DrainS {
				p.preview.DrainS = grace
			}
		}
	}
	return p, nil
}

func appendNew(list []string, xs ...string) []string {
	for _, x := range xs {
		if !slices.Contains(list, x) {
			list = append(list, x)
		}
	}
	return list
}
