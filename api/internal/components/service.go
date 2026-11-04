package components

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Service is the one door to the home's desired state (story 51.08): the admin routes
// and the CLI both call it, so the checks, the effect and the audit rows are the same.
// It does not check roles: its callers are admin-only.
type Service struct {
	Store   *store.Store
	Catalog *components.Catalog
	// Licensed reports whether the home's licence has a flag (nil: every flag).
	Licensed func(flag string) bool
	// Tier is the home's tier, named in a licence_missing refusal (nil: free).
	Tier func() string
	// AfterApply runs after a write that changed rows (nil: nothing): the API publishes
	// what the licence allows on a row the write created.
	AfterApply func(ctx context.Context)
	// Now is the clock of a hold's at (nil: time.Now).
	Now    func() time.Time
	Logger *slog.Logger
	// OnMachineChanged is called after a machine's override rewrote its stage switches
	// (51.12): its processes reload their stages at once (nil: at their next refresh).
	OnMachineChanged func(node string)
}

// NewService is the service on db, with the embedded catalog.
func NewService(db *sql.DB) *Service {
	return &Service{Store: New(db), Catalog: components.Default()}
}

func (s *Service) cat() *components.Catalog {
	if s.Catalog != nil {
		return s.Catalog
	}
	return components.Default()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) licensed(flag string) bool {
	return s.Licensed == nil || s.Licensed(flag)
}

func (s *Service) tier() string {
	if s.Tier != nil {
		if t := s.Tier(); t != "" {
			return t
		}
	}
	return "free"
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Known reports whether the catalog has id.
func (s *Service) Known(id string) bool {
	_, ok := s.cat().Get(id)
	return ok
}

// Preview is the effect of req over the rows as they are, changing nothing.
func (s *Service) Preview(ctx context.Context, req Request) (Preview, error) {
	if node, err := machineOf(req); err != nil || node != "" {
		if err != nil {
			return Preview{}, err
		}
		return s.previewMachine(ctx, node, req)
	}
	rows, rev, err := s.Store.Desired(ctx, store.Home)
	if err != nil {
		return Preview{}, err
	}
	p, err := makePlanTier(s.cat(), s.licensed, s.tier(), s.now(), rows, rev, req, Actor{By: "preview"}, true)
	if err != nil {
		return Preview{}, err
	}
	p.preview.QueuedJobsParked = s.queued(ctx, p.goingOff)
	return p.preview, nil
}

// Result is what a write did.
type Result struct {
	BatchID  string          `json:"batch_id,omitempty"`
	Revision int64           `json:"revision"`
	Changed  []store.Desired `json:"changed"`
	Effect   Preview         `json:"effect"`
	// Overrides are the override rows a machine's change wrote (51.12).
	Overrides []store.Override `json:"overrides,omitempty"`
}

// Apply makes every change of req in one transaction, or none: each is checked against
// the rows the earlier ones leave, inside the transaction, under the write lock. A
// refusal is a *Refusal.
func (s *Service) Apply(ctx context.Context, req Request, actor Actor) (Result, error) {
	if strings.TrimSpace(actor.By) == "" {
		return Result{}, errors.New("components: a change needs who makes it")
	}
	if node, err := machineOf(req); err != nil || node != "" {
		if err != nil {
			return Result{}, err
		}
		return s.applyMachine(ctx, node, req, actor)
	}
	var p *plan
	batch := uuid.NewString()
	changed, err := s.Store.SetDesiredBatch(ctx, func(rows map[string]store.Desired, rev int64) (store.BatchPlan, error) {
		var err error
		p, err = makePlanTier(s.cat(), s.licensed, s.tier(), s.now(), rows, rev, req, actor, false)
		if err != nil {
			return store.BatchPlan{}, err
		}
		if len(p.writes) > 1 || p.bundle != "" {
			for i := range p.writes {
				p.writes[i].Audit.BatchID = batch
			}
		}
		// the mirrors (extensions.enabled, hdhr_device.enabled) commit with the rows, so
		// the pipeline and older clients see the change at once (story 51.07)
		return store.BatchPlan{Writes: p.writes, Bundle: p.bundle, AfterWrite: writeMirror}, nil
	})
	if err != nil {
		return Result{}, err
	}
	res := Result{Changed: changed, Effect: p.preview}
	if changed == nil {
		res.Changed = []store.Desired{}
	}
	if len(p.writes) > 1 || p.bundle != "" {
		res.BatchID = batch
	}
	if len(changed) > 0 && s.AfterApply != nil {
		s.AfterApply(ctx)
	}
	res.Effect.QueuedJobsParked = s.queued(ctx, p.goingOff)
	res.Revision, _ = s.Store.Revision(ctx)
	return res, nil
}

// queued counts the waiting jobs of the stages of ids (they stay queued while their
// component is off). A count that cannot be read is 0 and logged.
func (s *Service) queued(ctx context.Context, ids []string) int {
	var stages []any
	for _, id := range ids {
		c, _ := s.cat().Get(id)
		for _, st := range c.Stages {
			stages = append(stages, st)
		}
	}
	if len(stages) == 0 {
		return 0
	}
	marks := make([]string, len(stages))
	for i := range stages {
		marks[i] = "$" + strconv.Itoa(i+1)
	}
	var n int
	err := s.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs WHERE state = 'pending' AND stage IN (`+
		strings.Join(marks, ", ")+`)`, stages...).Scan(&n)
	if err != nil {
		s.log().Warn("components: the queued jobs of a change cannot be counted", "err", err, "event", "components_queued_count_failed")
		return 0
	}
	return n
}

// MachineView is one machine's status of a component: the worst of its parts.
type MachineView struct {
	Machine string `json:"machine"`
	Phase   string `json:"phase"`
	Reason  string `json:"reason,omitempty"`
	// ReasonParams fill the reason's words (components.reason.<reason>): the params of
	// the status condition that gave the reason, as text ({"name":"release"}).
	ReasonParams       map[string]string `json:"reason_params,omitempty"`
	ObservedGeneration int64             `json:"observed_generation"`
	// Health is the machine's uptime, restarts, last error, version, memory and disk
	// (story 51.14, health.go).
	Health *Health        `json:"health,omitempty"`
	Parts  []store.Status `json:"parts,omitempty"`
}

// View is one component as the admin page shows it.
type View struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Group      string     `json:"group"`
	TitleKey   string     `json:"title_key"`
	DescKey    string     `json:"desc_key"`
	Switchable bool       `json:"switchable"`
	Placement  string     `json:"placement"`
	Licence    string     `json:"licence,omitempty"`
	Licensed   bool       `json:"licensed"`
	Requires   [][]string `json:"requires"`
	Wants      []string   `json:"wants"`
	Conflicts  []string   `json:"conflicts"`
	Dependents []string   `json:"dependents"`
	Bundles    []string   `json:"bundles"`
	Features   []string   `json:"features"`
	// Settings are the settings the component owns, as the catalog lists them
	// (rules.<section>, toml:<key>, ext:<name>): the page links to where each is set.
	Settings []string `json:"settings"`
	// Want, Hold and Generation are the desired row (no row: enabled, generation 0, and
	// HasRow false: the catalog default).
	Want       string      `json:"want"`
	Hold       *store.Hold `json:"hold"`
	Generation int64       `json:"generation"`
	HasRow     bool        `json:"has_row"`
	// Phase and Reason are the home's status (the worst of its parts); with no status
	// row yet, what the desired state makes of it (Expected).
	Phase  string `json:"phase"`
	Reason string `json:"reason,omitempty"`
	// ReasonParams are the home's reason's params (MachineView.ReasonParams).
	ReasonParams  map[string]string `json:"reason_params,omitempty"`
	Expected      Effect            `json:"expected"`
	Machines      []MachineView     `json:"machines"`
	RequiredBy    []string          `json:"required_by"`
	InstallSizeMB int64             `json:"install_size_mb"`
	// Health is the home's (machines[0] when it is the home); nil with no status row
	// from the home yet.
	Health *Health `json:"health,omitempty"`
	// Progress is this machine's last or running install or removal of an asset, as its
	// supervisor reports it (story 51.16); nil when there was none or no supervisor answers.
	Progress *InstallProgress `json:"progress,omitempty"`
}

// InstallProgress is the supervisor's progress of an asset's install or removal
// (GET /components/{id}/progress on its loopback admin). Busy is true while it runs; a
// failure has Busy false and a Reason (components.reason.<reason>, with Params). Percent
// is 0 to 100 and Detail a step code (components.detail.<detail>).
type InstallProgress struct {
	Phase   string         `json:"phase"`
	Percent int            `json:"percent"`
	Detail  string         `json:"detail,omitempty"`
	Name    string         `json:"name,omitempty"`
	Done    int64          `json:"done"`
	Total   int64          `json:"total"`
	Reason  string         `json:"reason,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
	Busy    bool           `json:"busy"`
	Started time.Time      `json:"started"`
	Updated time.Time      `json:"updated"`
	// Installed is whether the asset is on this machine now.
	Installed bool `json:"installed"`
}

// BundleLine is "Based on Full, minus YouTube": the bundle the home is based on and the
// components it adds and leaves out.
type BundleLine struct {
	Base  string   `json:"base"`
	Set   string   `json:"set"` // component_meta.bundle as stored (custom, or "")
	Plus  []string `json:"plus"`
	Minus []string `json:"minus"`
}

// ListView is GET /api/components.
type ListView struct {
	Revision   int64      `json:"revision"`
	Bundle     BundleLine `json:"bundle"`
	Components []View     `json:"components"`
}

// Detail is GET /api/components/{id}: the view with each machine's parts and conditions,
// and the last 20 audit rows.
type Detail struct {
	View
	Audit []store.AuditRow `json:"audit"`
}

// ErrUnknown is an id the catalog does not have.
var ErrUnknown = errors.New("components: unknown component")

type snapshot struct {
	rows     map[string]store.Desired
	rev      int64
	gates    map[string]gate
	statuses map[string][]store.Status
	meta     store.Meta
	heard    map[string]time.Time // joined machines' last heartbeat (health.go)
	now      time.Time
}

func (s *Service) snapshot(ctx context.Context) (*snapshot, error) {
	rows, rev, err := s.Store.Desired(ctx, store.Home)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.Statuses(ctx)
	if err != nil {
		return nil, err
	}
	meta, err := s.Store.Meta(ctx)
	if err != nil {
		return nil, err
	}
	st := map[string][]store.Status{}
	nodes := false
	for _, r := range all {
		st[r.ID] = append(st[r.ID], r)
		nodes = nodes || r.Machine != store.Home
	}
	snap := &snapshot{rows: rows, rev: rev, gates: gatesLicensed(s.cat(), rows, s.licensed), statuses: st, meta: meta,
		now: s.now()}
	if nodes {
		snap.heard = s.heardFrom(ctx)
	}
	return snap, nil
}

// List is every component, in catalog order.
func (s *Service) List(ctx context.Context) (ListView, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return ListView{}, err
	}
	out := ListView{Revision: snap.rev, Bundle: s.bundleLine(snap), Components: []View{}}
	for _, c := range s.cat().Components() {
		out.Components = append(out.Components, s.view(snap, c, false))
	}
	return out, nil
}

// Show is one component (ErrUnknown for an id the catalog does not have).
func (s *Service) Show(ctx context.Context, id string) (Detail, error) {
	c, ok := s.cat().Get(id)
	if !ok {
		return Detail{}, ErrUnknown
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		return Detail{}, err
	}
	audit, err := s.Store.AuditOf(ctx, id, 20)
	if err != nil {
		return Detail{}, err
	}
	return Detail{View: s.view(snap, c, true), Audit: audit}, nil
}

func (s *Service) view(snap *snapshot, c *components.Component, parts bool) View {
	cat := s.cat()
	v := View{ID: c.ID, Kind: c.Kind, Group: c.Group, TitleKey: c.TitleKey, DescKey: c.DescKey,
		Switchable: c.Switchable, Placement: c.Placement, Licence: c.Licence,
		Licensed: c.Licence == "" || s.licensed(c.Licence),
		Requires: [][]string{}, Wants: nonNil(c.Wants), Conflicts: conflictsOf(cat, c.ID),
		Dependents: nonNil(cat.Dependents(c.ID)), Bundles: nonNil(c.Bundles), Features: nonNil(c.Features),
		Settings: nonNil(c.Settings),
		Want:     components.WantEnabled, Machines: []MachineView{}}
	if v.Conflicts == nil {
		v.Conflicts = []string{}
	}
	for _, r := range cat.Requires(c.ID) {
		v.Requires = append(v.Requires, r.AnyOf)
	}
	if d, ok := snap.rows[c.ID]; ok && c.Switchable {
		v.Want, v.Hold, v.Generation, v.HasRow = d.Want, d.Hold, d.Generation, true
	}
	g := snap.gates[c.ID]
	v.Expected = Effect{ID: c.ID, To: g.phase, Reason: g.reason}
	v.Phase, v.Reason = g.phase, g.reason
	byMachine := map[string][]store.Status{}
	var machines []string
	for _, r := range snap.statuses[c.ID] {
		if _, seen := byMachine[r.Machine]; !seen {
			machines = append(machines, r.Machine)
		}
		byMachine[r.Machine] = append(byMachine[r.Machine], r)
	}
	slices.SortFunc(machines, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == store.Home:
			return -1
		case b == store.Home:
			return 1
		}
		return strings.Compare(a, b)
	})
	var bytes int64
	for _, f := range c.Files {
		bytes += f.Size
	}
	if bytes > 0 {
		v.InstallSizeMB = (bytes + 1<<20 - 1) >> 20
	} else {
		v.InstallSizeMB = int64(c.Resources.DiskMB)
	}
	for _, m := range machines {
		roll := store.Rollup(byMachine[m])
		mv := MachineView{Machine: m, Phase: roll.Phase, Reason: roll.Reason(), ReasonParams: reasonParams(roll),
			ObservedGeneration: roll.ObservedGeneration,
			Health:             healthOf(c, byMachine[m], roll.Phase, v.InstallSizeMB, snap.stale(m, snap.now))}
		if parts {
			mv.Parts = byMachine[m]
		}
		v.Machines = append(v.Machines, mv)
		if m == store.Home {
			v.Phase, v.Reason, v.ReasonParams, v.Health = mv.Phase, mv.Reason, mv.ReasonParams, mv.Health
		}
	}
	v.RequiredBy = requiredBy(cat, snap.rows, c.ID)
	if v.RequiredBy == nil {
		v.RequiredBy = []string{}
	}
	return v
}

// bundleLine compares what is on with the bundle the home is based on: the stored one,
// or for custom (or none) whichever of Full and Core is nearer.
func (s *Service) bundleLine(snap *snapshot) BundleLine {
	cat := s.cat()
	diff := func(b string) ([]string, []string) {
		in := map[string]bool{}
		for _, id := range cat.InBundle(b) {
			in[id] = true
		}
		plus, minus := []string{}, []string{}
		for _, id := range cat.Order() {
			c, _ := cat.Get(id)
			if !c.Switchable {
				continue
			}
			on := present(cat, snap.rows, id)
			switch {
			case on && !in[id]:
				plus = append(plus, id)
			case !on && in[id]:
				minus = append(minus, id)
			}
		}
		return plus, minus
	}
	line := BundleLine{Set: snap.meta.Bundle}
	base := snap.meta.Bundle
	if !slices.Contains(components.Bundles, base) {
		best := -1
		for _, b := range []string{"full", "core"} {
			p, m := diff(b)
			if best < 0 || len(p)+len(m) < best {
				best, base = len(p)+len(m), b
			}
		}
	}
	line.Base = base
	line.Plus, line.Minus = diff(base)
	return line
}

// reasonParams are the params of the condition that gives s its reason (the first false
// one with a reason, as Status.Reason), each as text; nil when it has none.
func reasonParams(s store.Status) map[string]string {
	for _, c := range s.Conditions {
		if c.Status != "false" || c.Reason == "" {
			continue
		}
		if len(c.Params) == 0 {
			return nil
		}
		out := make(map[string]string, len(c.Params))
		for k, v := range c.Params {
			switch x := v.(type) {
			case string:
				out[k] = x
			case nil:
				out[k] = ""
			default:
				b, err := json.Marshal(x)
				if err != nil {
					b = []byte(fmt.Sprint(x))
				}
				out[k] = string(b)
			}
		}
		return out
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// AsRefusal is err's refusal, if it is one.
func AsRefusal(err error) (*Refusal, bool) {
	var r *Refusal
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// UnknownRefusal is the 404 of an id the catalog does not have.
func UnknownRefusal(id string) *Refusal {
	return refuse(http.StatusNotFound, id, CodeUnknown, "no component "+id)
}
